package wisp

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
)

// HomeSession is the picker's own tmux session: the place you come back to.
//
// Deliberately not prefixed with SessionPrefix, so LiveSessions never picks it up and home
// never appears in its own list as an item.
const HomeSession = "wisp"

// Home switches to the picker session, creating it if it is not running.
//
// This exists because the picker used to be a popup over whatever session you happened to be
// in, which made it transient and translucent rather than somewhere you navigate to. As a real
// session it is a fixed destination: switch to it, pick an item, switch to that item, switch
// back to home. The tmux client stack does the work and nothing overlays anything.
func (c Config) Home() error {
	if !hasRawSession(HomeSession) {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot locate the wisp binary: %w", err)
		}
		// Looping is what makes it a home rather than a one-shot. Picking an item replaces this
		// process with `tmux switch-client`, and quitting exits it; either way the loop draws
		// the picker again, so returning here always lands on the list.
		//
		// Explicitly `pick`, never bare `wisp`: bare wisp means home, so a bare invocation here
		// would have the home session spawning home sessions forever.
		loop := fmt.Sprintf("while true; do %q pick; done", self)
		if err := exec.Command("tmux", "new-session", "-d",
			"-s", HomeSession, "-n", "wisp", "-c", c.Workspace, loop).Run(); err != nil {
			return fmt.Errorf("could not create the wisp home session: %w", err)
		}
		_ = exec.Command("tmux", "set-option", "-t", HomeSession, "status", "off").Run()
	}
	return Attach(HomeSession)
}

func hasRawSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// Cycle moves to the next (+1) or previous (-1) item session, wrapping at both ends.
//
// tmux has switch-client -n and -p, but those walk every session on the server. This walks only
// wisp's, in a stable sorted order, so flipping between work is unaffected by whatever else
// happens to be running. Home is not in the rotation: it is a destination, not a stop.
func Cycle(delta int) error {
	if !InsideTmux() {
		return fmt.Errorf("not inside tmux")
	}
	live := LiveSessions()
	if len(live) == 0 {
		return nil
	}
	sort.Strings(live)

	// From home, or from anywhere that is not an item session, enter the ring at the end that
	// matches the direction travelled rather than jumping to an arbitrary member.
	idx := -1
	current := CurrentSession()
	for i, s := range live {
		if s == current {
			idx = i
			break
		}
	}
	var target string
	switch {
	case idx < 0 && delta > 0:
		target = live[0]
	case idx < 0:
		target = live[len(live)-1]
	default:
		// Positive modulo: Go's % keeps the sign of the dividend, so -1 % n is -1, not n-1.
		target = live[((idx+delta)%len(live)+len(live))%len(live)]
	}
	return exec.Command("tmux", "switch-client", "-t", "="+target).Run()
}

// LeaveHome is what quitting the picker does when the picker is home.
//
// Home runs the picker in a loop, so plain exit is invisible: the loop redraws it and esc looks
// broken. Leaving means moving the client somewhere else, back to the session you came from, or
// off tmux entirely when there is nowhere to go back to. The loop still restarts the picker
// behind you, so the next visit gets a freshly loaded list.
func LeaveHome() error {
	if !InsideTmux() || CurrentSession() != HomeSession {
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
