package wisp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HomeSession is this workspace's picker session: the place you come back to.
//
// One per workspace, because the picker is scoped to a workspace and a shared home would have
// to keep asking which one it was showing. Deliberately joined with a dash rather than the
// underscore of SessionPrefix, so AllSessions never picks a home up and no home ever appears in
// its own list as an item.
func (c Config) HomeSession() string { return "wisp-" + wsToken(c.Name) }

// Home switches to this workspace's picker session, creating it if it is not running.
//
// This exists because the picker used to be a popup over whatever session you happened to be
// in, which made it transient and translucent rather than somewhere you navigate to. As a real
// session it is a fixed destination: switch to it, pick an item, switch to that item, switch
// back to home. The tmux client stack does the work and nothing overlays anything.
func (c Config) Home() error {
	if err := c.ensureHome(); err != nil {
		return err
	}
	return Attach(c.HomeSession())
}

func (c Config) ensureHome() error {
	if hasRawSession(c.HomeSession()) {
		// Tagged on every visit, not only at creation. The tmux server outlives an upgrade, so a
		// home session already standing would otherwise never carry the option and would keep
		// resolving to the default workspace for as long as it ran.
		c.tagHome()
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the wisp binary: %w", err)
	}
	// Looping is what makes it a home rather than a one-shot. Picking an item replaces this
	// process with `tmux switch-client`, and quitting exits it; either way the loop draws the
	// picker again, so returning here always lands on the list.
	//
	// Explicitly `pick`, never bare `wisp`: bare wisp means home, so a bare invocation here
	// would have the home session spawning home sessions forever. And explicitly pinned to this
	// workspace, because the loop must keep showing the same one even if the directory it
	// started in stops resolving there.
	//
	// The picker runs here even for a remote workspace. Drawing it on the far side would make
	// every keystroke a network round trip for the sake of redrawing a list; only the agent
	// session needs to be over there. So the loop is pinned by name, and the directory is one
	// that exists on this machine rather than the remote path.
	// shellQuote, not %q: tmux hands this line to /bin/sh, and Go's quoting leaves $, backtick and
	// backslash live inside the double quotes it produces. A workspace path or an install prefix
	// holding any of them would be expanded by the shell rather than passed through.
	dir := c.Workspace
	loop := "while true; do WISP_WORKSPACE=" + shellQuote(c.Workspace) + " " + shellQuote(self) + " pick; done"
	if c.IsRemote() {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		} else {
			dir = "/"
		}
		loop = "while true; do " + shellQuote(self) + " -w " + shellQuote(c.Name) + " pick; done"
	}
	if err := exec.Command("tmux", "new-session", "-d",
		"-s", c.HomeSession(), "-n", "wisp", "-c", dir, loop).Run(); err != nil {
		return fmt.Errorf("could not create the wisp home session: %w", err)
	}
	_ = exec.Command("tmux", "set-option", "-t", c.HomeSession(), "status", "off").Run()
	c.tagHome()
	return nil
}

// tagHome puts this workspace's name on its home session, the way Open puts it on an item's.
//
// It is what lets wisp run from inside the picker know which workspace it is looking at without
// re-deriving it from a directory. The home of a remote workspace runs in $HOME, since the
// workspace path is on another machine, and deriving it there finds the local default instead.
//
// AllSessions never picks a home up: it matches on the underscore of SessionPrefix and a home
// name is joined with a dash, so no home appears in its own list as an item.
func (c Config) tagHome() {
	_ = exec.Command("tmux", "set-option", "-t", c.HomeSession(), WSOption, c.Name).Run()
}

// Cycle moves to the next (+1) or previous (-1) item session in this workspace, wrapping at
// both ends. This is the inner ring; Hop is the outer one.
//
// tmux has switch-client -n and -p, but those walk every session on the server. This walks only
// this workspace's, in a stable sorted order, so flipping between work is unaffected by whatever
// else happens to be running, including the sessions of another workspace. Home is not in the
// rotation: it is a destination, not a stop.
func (c Config) Cycle(delta int) error {
	if !InsideTmux() {
		return fmt.Errorf("not inside tmux")
	}
	live := c.claim(AllSessions())
	if len(live) == 0 {
		return nil
	}

	// From home, or from anywhere that is not an item session, enter the ring at the end that
	// matches the direction travelled rather than jumping to an arbitrary member.
	idx := -1
	current := CurrentSession()
	for i, s := range live {
		if s.Name == current {
			idx = i
			break
		}
	}
	var target string
	switch {
	case idx < 0 && delta > 0:
		target = live[0].Name
	case idx < 0:
		target = live[len(live)-1].Name
	default:
		// Positive modulo: Go's % keeps the sign of the dividend, so -1 % n is -1, not n-1.
		target = live[((idx+delta)%len(live)+len(live))%len(live)].Name
	}
	return c.switchTo(target)
}

// switchTo moves the client and records where it landed, so a later hop back into this
// workspace can return to the same place.
func (c Config) switchTo(session string) error {
	if err := switchClient(session); err != nil {
		return err
	}
	Remember(c.Name, session)
	return nil
}

// Hop walks the workspace ring: the outer layer, above the item sessions Cycle moves between.
// The target is "next", "prev", or a workspace name.
//
// Where it lands matters more than that it moves. Hopping back into a workspace returns you to
// the session you were last in there, not to its picker, so a trip out and back is a round trip
// rather than a reset. The picker is the fallback, for a workspace you have not opened anything
// in yet or whose last session has since been killed.
func (c Config) Hop(target string) error {
	if !InsideTmux() {
		return fmt.Errorf("not inside tmux")
	}
	switch target {
	case "next", "", "+":
		return c.walk(1)
	case "prev", "-":
		return c.walk(-1)
	case c.Name:
		return nil
	default:
		// Named outright, so it has to work. Nothing is skipped and nothing is created: a
		// workspace configured but never made is an error worth reading, not a stop to step
		// over quietly.
		next, err := Load(target)
		if err != nil {
			return err
		}
		return c.enter(next)
	}
}

// walk moves to the next workspace in the ring that is actually set up, stepping over any whose
// vault does not exist yet.
//
// A ring walk should land somewhere you can work. A workspace named in the config but never
// created is a stop with nothing in it, and stopping there would strand you on an error message
// with no way onward but to name the next one by hand.
func (c Config) walk(delta int) error {
	// The same list the picker shows, which for a configured machine means the workspaces it
	// reports rather than only its name. Costs a round trip per machine; a ring that visited a
	// different set of places than the list in front of you would be worse.
	peers := c.Peers()
	if len(peers) < 2 {
		return fmt.Errorf("only one workspace (%s); add a machine under `hosts:` or a workspace under `workspaces:` in %s",
			c.Name, UserConfigPath())
	}
	names := make([]string, len(peers))
	ready := make(map[string]bool, len(peers))
	for i, p := range peers {
		names[i], ready[p.Name] = p.Name, p.Ready
	}

	name := c.Name
	var skipped []string
	for range names[1:] {
		name = step(names, name, delta)
		if ready[name] {
			if next, err := Load(name); err == nil {
				return c.enter(next)
			}
		}
		skipped = append(skipped, name)
	}
	return fmt.Errorf("nowhere to hop: %s %s not reachable; `wisp ws` shows why",
		strings.Join(skipped, ", "), plural(len(skipped), "is", "are"))
}

// enter moves the client into another workspace, landing on the session you were last in there.
func (c Config) enter(next Config) error {
	if next.Name == c.Name {
		return nil
	}
	// Record the session being left before moving, so hopping straight back returns here rather
	// than to whatever was remembered before this visit. Only if it is actually one of this
	// workspace's: `wisp hop` run from an unrelated session would otherwise file that session as
	// the place to come back to, and the next hop in would land somewhere with nothing to do
	// with the workspace.
	if cur := CurrentSession(); c.owns(cur) {
		Remember(c.Name, cur)
	}
	if last := lastVisited(next.Name); next.owns(last) {
		return switchClient(last)
	}
	if err := next.RequireWorkspace(); err != nil {
		return err
	}
	if err := next.ensureHome(); err != nil {
		return err
	}
	return switchClient(next.HomeSession())
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// owns reports whether a session is one of this workspace's item sessions. It guards both ends
// of the recorded last-visit: a session that is not this workspace's must never be filed as
// where you were, and a name recorded earlier must still resolve to that workspace before a hop
// will return to it.
func (c Config) owns(session string) bool {
	if session == "" {
		return false
	}
	for _, s := range c.claim(AllSessions()) {
		if s.Name == session {
			return true
		}
	}
	return false
}

// switchClient moves the attached client, naming the destination when it cannot. The bare exit
// status tmux returns is not something anyone can act on.
func switchClient(session string) error {
	if err := exec.Command("tmux", "switch-client", "-t", "="+session).Run(); err != nil {
		return fmt.Errorf("could not switch to %s: %w", session, err)
	}
	return nil
}

// step walks a ring, wrapping at both ends, entering at the start when the current member is
// not in it at all.
func step(names []string, current string, delta int) string {
	idx := 0
	for i, n := range names {
		if n == current {
			idx = i
			break
		}
	}
	// Positive modulo: Go's % keeps the sign of the dividend, so -1 % n is -1, not n-1.
	return names[((idx+delta)%len(names)+len(names))%len(names)]
}

// LeaveHome is what quitting the picker does when the picker is home.
//
// Home runs the picker in a loop, so plain exit is invisible: the loop redraws it and esc looks
// broken. Leaving means moving the client somewhere else, back to the session you came from, or
// off tmux entirely when there is nowhere to go back to. The loop still restarts the picker
// behind you, so the next visit gets a freshly loaded list.
func (c Config) LeaveHome() error {
	if !InsideTmux() || CurrentSession() != c.HomeSession() {
		return nil // a one-shot `wisp pick` just exits, which is already correct
	}
	// Detach, rather than switching to some other session. esc means quit: leave tmux and get
	// the terminal back.
	//
	// Nothing is lost by doing so. The tmux server keeps running, so every item session stays
	// exactly where it was, agents included, and the home session stays up too. The next
	// `wisp` re-attaches to a picker that is already warm.
	return exec.Command("tmux", "detach-client").Run()
}
