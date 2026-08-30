package wisp

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// SessionPrefix namespaces wisp's tmux sessions so AllSessions never picks up unrelated ones.
const SessionPrefix = "wisp_"

// The session options that carry a session's true identity. The name is lossy twice over: tmux
// forbids "." and ":" in session names, so sanitize rewrites them, and a name alone cannot say
// which workspace an item came from once two workspaces hold the same slug.
const (
	ItemOption = "@wisp_item"
	WSOption   = "@wisp_ws"
)

// lastOption prefixes a server-level option per workspace, holding the session you were last in
// there. Server options live exactly as long as the tmux server does, which is the same lifetime
// as the sessions they point at, so hopping between workspaces needs no state file and cannot
// outlive the thing it describes.
const lastOption = "@wisp_last_"

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// Session is one live wisp session with its identity resolved.
type Session struct {
	Name  string
	Item  string
	WS    string
	State State
}

// sanitize keeps a string to what tmux accepts in a session name.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// wsToken is sanitize with underscores folded to dashes as well. A session is named
// wisp_<ws>_<item>, so a workspace whose own name held an underscore would make the boundary
// between the two halves ambiguous.
func wsToken(s string) string { return strings.ReplaceAll(sanitize(s), "_", "-") }

// SessionName is the tmux session name for an item in this workspace.
func (c Config) SessionName(item string) string {
	return SessionPrefix + wsToken(c.Name) + "_" + sanitize(item)
}

// AllSessions lists every wisp session on the server, across all workspaces, in one tmux call.
//
// The identity options are read through the format string rather than with a show-option per
// session. tmux exposes user options to formats, and the per-session shape cost one process
// spawn for every session on every load of the picker.
func AllSessions() []Session {
	format := strings.Join([]string{"#{session_name}", "#{" + ItemOption + "}", "#{" + WSOption + "}"}, "\t")
	out, err := tmux("ls", "-F", format)
	if err != nil {
		return nil // no server running is the normal case, not an error
	}
	var live []Session
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 || !strings.HasPrefix(f[0], SessionPrefix) {
			continue
		}
		s := Session{Name: f[0], Item: f[1], WS: f[2]}
		if s.Item == "" {
			// A session from before the item option existed. Its name is all there is.
			s.Item = strings.TrimPrefix(s.Name, SessionPrefix)
		}
		live = append(live, s)
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Name < live[j].Name })
	return live
}

// Sessions is this workspace's live sessions, with the needs-input check already done.
func (c Config) Sessions() []Session {
	live := c.claim(AllSessions())
	resolveStates(live)
	return live
}

// claim decides which of the server's sessions belong to this workspace.
//
// A session created before workspaces existed carries no @wisp_ws. The default workspace adopts
// those, so upgrading wisp with work already running does not strand it: the sessions stay in
// the picker rather than becoming invisible ones nobody can reach.
func (c Config) claim(all []Session) []Session {
	adopt := c.Name == c.DefaultName()
	var out []Session
	for _, s := range all {
		if s.WS == c.Name || (s.WS == "" && adopt) {
			out = append(out, s)
		}
	}
	return out
}

// resolveStates fills in each session's state. The needs-input check is a capture-pane per
// session, so they run together rather than each in turn.
func resolveStates(sessions []Session) {
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessions[i].State = StateLive
			if NeedsInput(sessions[i].Name) {
				sessions[i].State = StateNeedsInput
			}
		}(i)
	}
	wg.Wait()
}

// FindSession is the live session for an item, matched on identity rather than on the session
// name. That is what lets a session created by an older wisp, under the old naming, still be
// found and reattached rather than quietly duplicated alongside a second one.
func (c Config) FindSession(item string) string {
	for _, s := range c.claim(AllSessions()) {
		if s.Item == item {
			return s.Name
		}
	}
	return ""
}

func (c Config) HasSession(item string) bool { return c.FindSession(item) != "" }

// KillSession stops the work, which for a remote item means the session on the machine doing it
// and not merely the terminal pointed at it.
//
// Dropping the wrapper alone would leave an agent running that nothing lists any more. Walking
// away and leaving it running is what esc already does, so kill has to mean the heavier thing or
// there is no way to say it.
func (c Config) KillSession(item string) error {
	session := c.FindSession(item)
	if c.IsRemote() {
		if _, err := c.Location.run("kill", item); err != nil && session == "" {
			return err
		}
	} else if session == "" {
		return fmt.Errorf("no session for %s", item)
	}
	if session == "" {
		return nil
	}
	// A wrapper usually dies on its own: killing the far side ends the ssh, which closes the
	// window, which takes the session with it. Losing that race is not a failure, so the error
	// only counts if the session is still standing afterwards.
	if err := exec.Command("tmux", "kill-session", "-t", "="+session).Run(); err != nil && hasRawSession(session) {
		return fmt.Errorf("could not kill %s: %w", session, err)
	}
	return nil
}

// CurrentSession is the session wisp itself is running inside, or "" when it is not in tmux.
func CurrentSession() string {
	if !InsideTmux() {
		return ""
	}
	out, err := tmux("display-message", "-p", "#{session_name}")
	if err != nil {
		return ""
	}
	return out
}

// IsCurrentSession reports whether killing this item would kill the session wisp is drawn on.
//
// This is the difference between "kill that session" and "the picker vanished": tmux tears down
// the client along with its session, so killing your own session from a popup looks exactly
// like wisp crashing rather than like the kill succeeding.
func (c Config) IsCurrentSession(item string) bool {
	cur := CurrentSession()
	return cur != "" && cur == c.FindSession(item)
}

func hasRawSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// Remember records a session as this workspace's most recent, so hopping back into the
// workspace lands where you left it rather than resetting you to the picker.
func Remember(ws, session string) {
	if ws == "" || session == "" {
		return
	}
	_ = exec.Command("tmux", "set-option", "-s", lastOption+wsToken(ws), session).Run()
}

// lastVisited is the session Remember last recorded for a workspace, or "" if there is none.
func lastVisited(ws string) string {
	out, err := tmux("show-option", "-sqv", lastOption+wsToken(ws))
	if err != nil {
		return ""
	}
	return out
}

// CapturePane returns a session's visible pane content, used for both the preview and the
// needs-input check.
func CapturePane(session string, ansi bool) string {
	args := []string{"capture-pane", "-p", "-J", "-t", session}
	if ansi {
		args = append(args, "-e")
	}
	out, _ := tmux(args...)
	return out
}

// needsInputMarker is a string from Claude Code's permission dialog, borrowed from
// claude-squad. It is best-effort by construction: if that copy is reworded, the "?" state
// silently stops appearing. The preview pane is the reliable signal.
const needsInputMarker = "No, and tell Claude what to do differently"

func NeedsInput(session string) bool {
	return strings.Contains(CapturePane(session, false), needsInputMarker)
}

// InsideTmux reports whether wisp itself was launched from within tmux, which decides between
// switching the current client and attaching a new one.
func InsideTmux() bool { return os.Getenv("TMUX") != "" }
