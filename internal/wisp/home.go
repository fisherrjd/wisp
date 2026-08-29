package wisp

import (
	"fmt"
	"os"
	"os/exec"
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
