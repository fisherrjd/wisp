package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/fisherrjd/wisp/internal/ui"
	"github.com/fisherrjd/wisp/internal/wisp"
)

const version = "0.6.0"

const usage = `wisp - one work item, one tmux session

usage:
  wisp                    go to the picker session, creating it if needed
  wisp pick               run the picker once, here, without a session
  wisp open <item>        open an item directly
  wisp ls                 list live sessions
  wisp kill <item>        kill an item's session
  wisp repos              list workspace repos
  wisp version            print the version

workspace resolution, in order:
  WISP_WORKSPACE, if set
  the nearest ancestor holding a .wisp.yaml or working_items/
  a "workspace:" key in ~/.config/wisp/config.yaml

config:
  <workspace>/.wisp.yaml, then ~/.config/wisp/config.yaml
  WISP_PROGRAM    agent command for window 0 (default: claude)
  WISP_INSTALL    set to install dependencies when provisioning
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "wisp: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	// Handled before config loads, so they work from anywhere.
	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("wisp %s\n", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}

	cfg, err := wisp.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireWorkspace(); err != nil {
		return err
	}

	switch cmd {
	// Bare `wisp` is home: the picker in its own session, which is the thing you come back to.
	// `wisp pick` is the one-shot picker in the current terminal, for scripts and for the
	// loop inside the home session itself.
	case "", "home":
		return cfg.Home()

	case "pick":
		return ui.Run(cfg)

	case "open", "o":
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp open <item>")
		}
		return cfg.Open(wisp.Item{Name: args[1]}, func(msg string) {
			fmt.Fprintf(os.Stderr, "--- %s\n", msg)
		})

	case "provision":
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp provision <item>")
		}
		err := cfg.ProvisionItem(wisp.Item{Name: args[1]}, func(msg string) { fmt.Printf("--- %s\n", msg) })
		if err != nil {
			// Stay on screen. This runs in its own tmux window, which closes the moment the
			// command returns, and a failure that vanishes is one nobody can read.
			fmt.Fprintf(os.Stderr, "\nwisp: %v\n\npress enter to close\n", err)
			_, _ = fmt.Scanln()
		}
		return nil

	case "kill":
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp kill <item>")
		}
		if err := wisp.KillSession(args[1]); err != nil {
			return fmt.Errorf("no session for %s", args[1])
		}
		fmt.Printf("killed %s\n", args[1])
		return nil

	case "ls":
		for _, s := range wisp.LiveSessions() {
			name := wisp.ItemFor(s)
			if name == "" {
				name = s
			}
			fmt.Printf("%-46s %s\n", name, s)
		}
		return nil

	case "repos":
		repos, err := cfg.Repos()
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(repos, "\n"))
		return nil

	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}
