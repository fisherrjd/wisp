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
  wisp new <name|url>     make an item: a name, or a gitlab link to derive
                          <repo>/<iid>-<slug> from
  wisp ls                 list live sessions, every workspace
  wisp done <item> [-m <line>]
                          close an item out: it leaves the picker and the line
                          is written into its notes.md. Refused with neither
                          when the note is still empty; --anyway closes it bare.
                          --undo reopens, --list shows what has been closed out
  wisp ws                 list workspaces
  wisp ws new [-p] <name> [path]
                          make a directory a workspace and register it;
                          the path may be local or host:path, and -p
                          creates the directory too
  wisp ws rm <name>       forget a workspace; nothing on disk is touched
  wisp host               list the machines wisp can reach
  wisp host add [name] <ssh target>
                          register a machine; every workspace it holds
                          joins the ring
  wisp host rm <name>     forget a machine and everything it holds
  wisp kill <item>        kill an item's session
  wisp workflow [<item>]  the workflow in effect, key by key, and where each
                          key came from. list, show, init, use, edit and
                          accept live under it
  wisp repos              list workspace repos
  wisp version            print the version

  -w <workspace>          act on a named workspace instead of the one you are in

three levels: next/prev walks the sessions inside a workspace, hop walks the
workspaces, and a workspace on another machine is host/name. Hopping lands on
the session you were last in there.

workspace resolution, in order:
  -w <name>, if given
  WISP_WORKSPACE, if set
  the nearest ancestor holding a .wisp.yaml or working_items/
  the default workspace from ~/.config/wisp/config.yaml

config:
  <workspace>/.wisp.yaml, then ~/.config/wisp/config.yaml
  workspaces:, hosts: and default: are read from the user config only
  WISP_PROGRAM    agent command for the agent window (default: claude)
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
		// Checked here and not inside Open: the picker only ever hands Open a row it found, so
		// this is the one caller that can name something that does not exist, and a typo there
		// used to build a whole session around itself.
		it := wisp.Item{Name: args[1]}
		if err := cfg.RequireItem(it); err != nil {
			return err
		}
		// --workflow is the one-shot: it sits above every configured layer and is written
		// nowhere, for "open this one differently, just this once".
		return cfg.Open(it, flagStr(args, "--workflow", ""), func(msg string) {
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
		// --json is how another machine asks what this one holds. Its own workspaces only: a
		// machine that enumerated its remote ones would let two of them holding each other
		// enumerate forever.
		if hasFlag(args, "--json") {
			out := wisp.HostJSON{Wire: wisp.WireVersion, Wisp: wisp.Version, Default: cfg.DefaultName()}
			for _, p := range cfg.LocalPeers() {
				out.Workspaces = append(out.Workspaces, wisp.WSJSON{
					Name: p.Name, Path: p.Path, Ready: p.Ready, Live: p.Live, Attn: p.Attn,
				})
			}
			return json.NewEncoder(os.Stdout).Encode(out)
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

	// Machines are their own family of commands, not a spelling of `ws new`. They sit a level
	// above workspaces and adding one gets you everything on it, which is a different act from
	// making a directory somewhere.
	case "host", "hosts":
		return hostCommand(cfg, args[1:])

	// The workflow is resolved per key across five layers, which is what makes a four line
	// workflow useful and also what makes "why did my session open like that" unanswerable from
	// any one file. This command is where that is readable.
	case "workflow", "wf":
		return cfg.WorkflowCommand(args[1:])

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

	// The counterpart to `new`. It writes one line of frontmatter into the item's notes.md, so
	// whatever closes an item out, a person or the agent finishing its write-up, can end the
	// work and clear the row in the same action rather than leaving the list to be tidied later.
	case "done":
		if hasFlag(args, "--list") {
			items, err := cfg.BoardItems()
			if err != nil {
				return err
			}
			for _, it := range wisp.DoneOnly(items) {
				fmt.Println(it.Name)
			}
			return nil
		}
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp done <item> [-m <line>] [--undo] | wisp done --list")
		}
		item := args[1]
		if hasFlag(args, "--undo") {
			if err := cfg.SetDone(item, false); err != nil {
				return err
			}
			fmt.Printf("reopened %s\n", item)
			return nil
		}
		// Refused only when there is nothing written down at all. An item you have already
		// taken notes on closes with no ceremony; the friction lands exactly where the record
		// would otherwise be lost, which is the case that produced a vault full of notes
		// holding one line of frontmatter and nothing else.
		note := flagStr(args, "-m", "")
		if note == "" && !hasFlag(args, "--anyway") && cfg.NoteIsEmpty(item) {
			return fmt.Errorf("nothing is written down about %s\n\n"+
				"A closed-out item is one you stop seeing, so the note is the only thing left\n"+
				"of it. Say what happened:\n"+
				"  wisp done %s -m 'what it turned out to be'\n\n"+
				"Or close it out bare, for work there is nothing to say about:\n"+
				"  wisp done %s --anyway", item, item, item)
		}
		if err := cfg.CloseOut(item, true, note); err != nil {
			return err
		}
		if note != "" {
			fmt.Printf("closed out %s, and wrote it up in %s\n", item, cfg.NotesPath(item))
		} else {
			fmt.Printf("closed out %s; it is out of the picker, nothing on disk was touched\n", item)
		}
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
			remote, err := cfg.RemoteItems()
			if err != nil && out.Note == "" {
				out.Note = err.Error()
			}
			items = wisp.MergeAll(items, remote)
		}
		for _, it := range items {
			out.Items = append(out.Items, wisp.ItemJSON{Name: it.Name, State: int(it.State), Title: it.Title, Done: it.Done})
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

// flagStr is the string half of flagInt: `-m <line>`, with the value as the next argument.
func flagStr(args []string, flag, fallback string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return fallback
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

// hostCommand handles `wisp host`, `wisp host add [name] <target>` and `wisp host rm <name>`.
func hostCommand(cfg wisp.Config, args []string) error {
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	switch verb {
	case "add":
		name, target := "", ""
		switch rest := args[1:]; len(rest) {
		case 0:
			return fmt.Errorf("usage: wisp host add [name] <ssh target>")
		case 1:
			target = rest[0] // named after the machine
		default:
			name, target = rest[0], rest[1]
		}
		summary, err := cfg.AddHost(name, target)
		if err != nil {
			return err
		}
		if name == "" {
			name = wisp.HostName(target)
		}
		fmt.Printf("%s: %s\n\n  wisp ws        its workspaces\n  wisp -w %s%s go to it\n",
			name, summary, name, strings.Repeat(" ", max(1, 8-len(name))))
		return nil

	case "rm", "forget":
		if len(args) < 2 {
			return fmt.Errorf("usage: wisp host rm <name>")
		}
		if err := cfg.Unregister(args[1]); err != nil {
			return err
		}
		fmt.Printf("forgot %s; nothing on disk was touched\n", args[1])
		return nil

	case "":
		for _, n := range cfg.HostNames() {
			fmt.Printf("%-12s %s\n", n, cfg.Hosts[n])
		}
		return nil

	default:
		return fmt.Errorf("unknown host command %q\n\nusage:\n  wisp host\n  wisp host add [name] <ssh target>\n  wisp host rm <name>", verb)
	}
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

	// A remote one was registered, not built: the vault and the config over there are the far
	// side's, and this end never made them. Saying "edit its .wisp.yaml" would be pointing at a
	// file that does not exist.
	if loc := wisp.ParseLocation(created); loc.IsRemote() {
		if mkdir {
			fmt.Printf("\nmade on %s as well. Both ends need wisp %s or newer.\n", loc.Host, wisp.Version)
		} else {
			// Registered only, so say so: the vault over there is the far side's and this end
			// did not make one. Naming the flag beats naming the ssh command it stands for.
			fmt.Printf("\nregistered only; it has to exist on %s already.\nAdd -p to make it there too.\n", loc.Host)
		}
		fmt.Printf("\n  wisp ws        check that it answers\n  wisp -w %s%s go there\n",
			name, strings.Repeat(" ", max(1, 8-len(name))))
		return nil
	}

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
