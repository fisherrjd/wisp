package wisp

import (
	"os"
	"os/exec"
	"strings"
)

// SessionPrefix namespaces wisp's tmux sessions so live_sessions never picks up unrelated ones.
const SessionPrefix = "wisp_"

// ItemOption is the tmux session option holding the item's true name. The session name itself is
// lossy: tmux forbids "." and ":" in session names, so sanitize rewrites them.
const ItemOption = "@wisp_item"

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// SessionFor is the tmux session name for an item.
func SessionFor(item string) string {
	var b strings.Builder
	for _, r := range item {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return SessionPrefix + b.String()
}

// LiveSessions returns wisp's running sessions, newest tmux ordering preserved.
func LiveSessions() []string {
	out, err := tmux("ls", "-F", "#{session_name}")
	if err != nil {
		return nil // no server running is the normal case, not an error
	}
	var live []string
	for _, s := range strings.Split(out, "\n") {
		if strings.HasPrefix(s, SessionPrefix) {
			live = append(live, s)
		}
	}
	return live
}

// ItemFor recovers the item name a session was created for.
func ItemFor(session string) string {
	// Note the missing "=" prefix: tmux accepts the exact-match marker for session targets like
	// has-session, but rejects it for show-option and capture-pane.
	out, err := tmux("show-option", "-qv", "-t", session, ItemOption)
	if err != nil {
		return ""
	}
	return out
}

func HasSession(item string) bool {
	err := exec.Command("tmux", "has-session", "-t", "="+SessionFor(item)).Run()
	return err == nil
}

func KillSession(item string) error {
	return exec.Command("tmux", "kill-session", "-t", "="+SessionFor(item)).Run()
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
