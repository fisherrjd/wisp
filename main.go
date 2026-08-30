package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fisherrjd/wisp/internal/ui"
	"github.com/fisherrjd/wisp/internal/wisp"
)

// The version lives in the wisp package: the two ends of a remote workspace are separate
// installs, and each has to be able to say what it is.
const version = wisp.Version

const usage = `wisp - one work item, one tmux session

usage:
  wisp                    go to the picker session, creating it if needed
  wisp pick               run the picker once, here, without a session
  wisp next / wisp prev   cycle to the next or previous item session
  wisp hop [next|prev|<ws>]  move to another workspace
  wisp open <item>        open an item directly
  wisp ls                 list live sessions
  wisp ws                 list workspaces
  wisp ws new [-p] <name> [path]
                          make a directory a workspace and register it;
                          -p creates the directory too
  wisp ws rm <name>       forget a workspace; nothing on disk is touched
  wisp kill <item>        kill an item's session
  wisp repos              list workspace repos
  wisp version            print the version

  -w <workspace>          act on a named workspace instead of the one you are in

two rings: next/prev walks the item sessions inside a workspace, hop walks the
workspaces themselves. Hopping lands on the session you were last in there.

workspace resolution, in order:
  -w <name>, if given
  WISP_WORKSPACE, if set
  the nearest ancestor holding a .wisp.yaml or working_items/
  the default workspace from ~/.config/wisp/config.yaml

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
	// Handled before anything else, so they work from anywhere, config or no config.
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Printf("wisp %s\n", version)
			return nil
		case "help", "--help", "-h":
			fmt.Print(usage)
			return nil
		}
	}

	// A leading -w selects the workspace for whatever follows, so every command below can be
	// aimed at a workspace you are not currently in. Bare `wisp -w side` is that workspace's
	// home, which is the useful shorthand.
	ws := ""
	for len(args) > 0 && (args[0] == "-w" || args[0] == "--workspace") {
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp -w <workspace> [command]")
		}
		ws, args = args[1], args[2:]
	}

	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	cfg, err := wisp.Load(ws)
	if err != nil {
		return err
	}
	// Two commands have to work from outside a workspace. `ws` because listing them is how you
	// find out none is set up and creating one is how you fix that; `board` because its whole
	// job is reporting state, and "there is no workspace here" is a state a caller across the
	// network needs told rather than inferred from a failure.
	if cmd != "ws" && cmd != "board" {
		if err := cfg.RequireWorkspace(); err != nil {
			return err
		}
	}

	switch cmd {
	// Bare `wisp` is home: the picker in its own session, which is the thing you come back to.
	// `wisp pick` is the one-shot picker in the current terminal, for scripts and for the
	// loop inside the home session itself.
	case "", "home":
		return cfg.Home()

	case "pick":
		return ui.Run(cfg)

	case "next":
		return cfg.Cycle(1)

	case "prev":
		return cfg.Cycle(-1)

	case "hop":
		target := "next"
		if len(args) > 1 {
			target = args[1]
		}
		return cfg.Hop(target)

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
		if err := cfg.KillSession(args[1]); err != nil {
			return err
		}
		fmt.Printf("killed %s\n", args[1])
		return nil

	// Every workspace, not just this one: the point of the list is to see what is running
	// across all of them without having to hop to each in turn.
	case "ls":
		def := cfg.DefaultName()
		for _, s := range wisp.AllSessions() {
			owner := s.WS
			if owner == "" {
				owner = def // predates the workspace option; the default workspace claims it
			}
			fmt.Printf("%-12s %-46s %s\n", owner, s.Item, s.Name)
		}
		return nil

	case "ws":
		if len(args) > 1 && args[1] == "new" {
			return newWorkspace(cfg, args[2:])
		}
		if len(args) > 1 && (args[1] == "rm" || args[1] == "forget") {
			if len(args) < 3 {
				return fmt.Errorf("usage: wisp ws rm <name>")
			}
			if err := cfg.Unregister(args[2]); err != nil {
				return err
			}
			fmt.Printf("forgot %s; nothing on disk was touched\n", args[2])
			return nil
		}
		for _, p := range cfg.Peers() {
			mark := " "
			if p.Current {
				mark = "*"
			}
			state := fmt.Sprintf("%d live, %d waiting", p.Live, p.Attn)
			switch {
			case p.Unreachable:
				state = p.Detail // names the host and what ssh said
			case !p.Ready:
				state = "does not exist yet"
			}
			fmt.Printf("%s %-12s %-40s %s\n", mark, p.Name, p.Path, state)
		}
		return nil

	case "repos":
		repos, err := cfg.Repos()
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(repos, "\n"))
		return nil

	// The three commands a remote workspace is driven through. They are ordinary commands, not
	// a mode: the far side is just wisp, answering about the workspace it owns.
	case "board":
		return emitBoard(cfg, hasFlag(args, "--gitlab"))

	case "preview":
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp preview <item> [--width n]")
		}
		fmt.Print(cfg.Preview(wisp.Item{Name: args[1]}, flagInt(args, "--width", 80)))
		return nil

	case "new":
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp new <name|url> [--json]")
		}
		it, err := cfg.NewItem(args[1])
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Name string `json:"name"`
			}{it.Name})
		}
		fmt.Println(it.Name)
		return nil

	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

// emitBoard prints this workspace's state as JSON, for the wisp on another machine that has it
// registered as a remote workspace.
//
// A missing workspace is reported rather than raised. "the host answered and there is no vault
// there" and "the host did not answer" are different problems with different fixes, and
// collapsing them into one non-zero exit would throw that away.
func emitBoard(cfg wisp.Config, gitlab bool) error {
	out := wisp.BoardJSON{Wire: wisp.WireVersion, Wisp: wisp.Version, Ready: cfg.Ready()}
	if out.Ready {
		// BoardItems, not Local: a board is about this workspace alone. Local also builds the
		// tally, which probes every remote workspace this machine has configured, and a board
		// request that did that would walk from host to host with nothing to stop it.
		items, err := cfg.BoardItems()
		if err != nil {
			out.Note = err.Error()
		}
		if gitlab {
			remote, err := cfg.GitLabItems()
			if err != nil && out.Note == "" {
				out.Note = err.Error()
			}
			items = wisp.MergeAll(items, remote)
		}
		for _, it := range items {
			out.Items = append(out.Items, wisp.ItemJSON{Name: it.Name, State: int(it.State), Title: it.Title})
			switch it.State {
			case wisp.StateNeedsInput:
				out.Attn++
				out.Live++
			case wisp.StateLive:
				out.Live++
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func flagInt(args []string, flag string, fallback int) int {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				return n
			}
		}
	}
	return fallback
}

// newWorkspace handles `wisp ws new [-p] <name> [path]`. The path defaults to the current
// directory, since the usual moment for this is standing in the tree you want to adopt.
func newWorkspace(cfg wisp.Config, args []string) error {
	// Accepted in any position. `wisp ws new side -p ~/side` is at least as natural to type as
	// putting the flag first, and treating a trailing -p as the path would create a directory
	// called "-p" in the current one.
	mkdir := false
	rest := args[:0:0]
	for _, a := range args {
		if a == "-p" || a == "--parents" {
			mkdir = true
			continue
		}
		rest = append(rest, a)
	}
	args = rest
	if len(args) == 0 {
		return fmt.Errorf("usage: wisp ws new [-p] <name> [path]")
	}
	name := args[0]
	path := "."
	if len(args) > 1 {
		path = args[1]
	}

	created, err := cfg.CreateWorkspace(name, path, mkdir)
	if err != nil {
		return err
	}
	fmt.Printf("workspace %s at %s\n", name, created)
	// Said once, here, rather than left to be discovered. The upward search stops at the
	// nearest workspace, so anything below this point now resolves here and the outer one
	// becomes unreachable from inside it.
	if outer := cfg.NestedIn(created); outer != "" {
		fmt.Printf("\nnote: this sits inside the workspace at %s.\nRunning wisp anywhere below here will find this one, not that one.\n", outer)
	}
	fmt.Printf("\nedit %s for its gitlab group, then:\n  wisp -w %s\n",
		filepath.Join(created, wisp.MarkerFile), name)
	return nil
}
