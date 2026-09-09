package wisp

import (
	"io/fs"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// `wisp workflow` is the command that answers "why did my session open like that".
//
// Resolution runs across five layers, per key, which is what makes a four line workflow useful
// and also what makes the answer unguessable from any one file. Every subcommand here exists to
// put that back within reach: what is in effect and where each key came from, what else is
// addressable, what one bundle sets, how to make one, how to bind one, and what letting a
// workspace's own bundle run actually means.

const workflowUsage = `usage:
  wisp workflow [<item>]      the workflow in effect, key by key, and where each key came from
  wisp workflow list          every workflow addressable from here
  wisp workflow show <name>   what one workflow sets, on its own
  wisp workflow init <name> [--here]
                              write a starting point: the built-in, spelled out
  wisp workflow use <name> [--here]
                              bind to a workflow
  wisp workflow edit [<name>]
                              open its workflow.yaml in $EDITOR
  wisp workflow accept [-y]   read everything this workspace would run, and allow it
  wisp workflow accept ./<name> [-y]
                              just the workflow this workspace ships
  wisp workflow accept .wisp.yaml [-y]
                              just the workspace config's own program and hooks
  wisp workflow accept <item> [-y]
                              just an item whose orchestration.md runs something
  wisp workflow push <name> <host>
                              copy one of yours to another machine
  wisp workflow list --host <host>
                              what is over there, and whether it matches here

a bare name is one of yours, under ~/.config/wisp/workflows.
a leading ./ is one this workspace ships, which is accepted before it runs.
--here writes to the workspace instead of your user config.
`

// WorkflowCommand is `wisp workflow` and everything under it.
func (c Config) WorkflowCommand(args []string) error {
	// Every workflow is a file, and for a remote workspace every one of those files is on the
	// other machine. Answering from here would describe this machine's config against that
	// machine's paths, which is worse than not answering.
	if c.IsRemote() {
		return fmt.Errorf("workspace %q is on %s, and its workflows are files over there\n\nask the wisp that owns them:\n  ssh %s wisp workflow", c.Name, c.Location.Host, c.Location.Host)
	}

	// Bare `wisp workflow` is the common case and has no verb at all, so the tail has to be
	// taken from a length that exists rather than by slicing past the end of an empty argv.
	verb, rest := "", []string(nil)
	if len(args) > 0 {
		verb, rest = args[0], args[1:]
	}

	switch verb {
	case "help", "-h", "--help":
		fmt.Print(workflowUsage)
		return nil

	case "list", "ls":
		names, flags, err := workflowFlags(rest, "--host")
		if err != nil {
			return err
		}
		if flags["--host"] {
			if len(names) == 0 {
				return fmt.Errorf("usage: wisp workflow list --host <host>\n\n`wisp host` is what there is to name")
			}
			return c.printHostWorkflows(names[0])
		}
		c.printWorkflowList()
		return nil

	// A workflow does not cross a host boundary on its own: the far side runs its own wisp and
	// reads its own config, and nothing is shipped over ssh. So across machines a workflow is
	// necessarily a copy, and this is wisp making one for you rather than leaving it to scp.
	case "push":
		names, _, err := workflowFlags(rest)
		if err != nil {
			return err
		}
		if len(names) < 2 {
			return fmt.Errorf("usage: wisp workflow push <name> <host>\n\n`wisp host` is the machines wisp can reach")
		}
		summary, err := c.PushWorkflow(names[0], names[1])
		if err != nil {
			return err
		}
		fmt.Printf("%s\n\ncheck it landed:\n  wisp workflow list --host %s\n", summary, names[1])
		return nil

	case "show":
		names, _, err := workflowFlags(rest)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return fmt.Errorf("usage: wisp workflow show <name>\n\n`wisp workflow list` is what there is to name")
		}
		c.printWorkflow(c.resolveAddr(names[0]), "")
		return nil

	case "init":
		// --from takes a value, which the flag parser does not know how to do; it is lifted out
		// first so the rest can be checked as before.
		from := ""
		var trimmed []string
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--from" && i+1 < len(rest) {
				from = rest[i+1]
				i++
				continue
			}
			trimmed = append(trimmed, rest[i])
		}
		names, flags, err := workflowFlags(trimmed, "--here")
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return fmt.Errorf("usage: wisp workflow init <name> [--from <shipped>] [--here]\n\nthe name is a directory: `wisp workflow init solo` makes solo yours;\n--from starts from one of the bundles wisp ships (%s)", strings.Join(shippedNames, ", "))
		}
		return c.workflowInit(names[0], from, flags["--here"])

	case "use":
		names, flags, err := workflowFlags(rest, "--here")
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return fmt.Errorf("usage: wisp workflow use <name> [--here]\n\n`wisp workflow list` is what there is to bind to")
		}
		return c.workflowUse(names[0], flags["--here"])

	case "edit":
		names, _, err := workflowFlags(rest)
		if err != nil {
			return err
		}
		addr := ""
		if len(names) > 0 {
			addr = names[0]
		}
		return c.workflowEdit(addr)

	case "accept":
		names, flags, err := workflowFlags(rest, "-y", "--yes")
		if err != nil {
			return err
		}
		// No argument is the whole workspace, not a usage error. The script gate refuses a script
		// whoever named it, and one of the things it refuses is the built-in's own `provision:`
		// default, which no file names and so has no address to type.
		if len(names) == 0 {
			return c.acceptEverything(flags["-y"] || flags["--yes"])
		}
		return c.workflowAccept(names[0], flags["-y"] || flags["--yes"])

	default:
		// Anything else names an item. The per-item answer is the one worth having: an item may
		// override the workflow in its own frontmatter, so "why did this one open like that" is a
		// different question from "what does this workspace do".
		item := Item{}
		if verb != "" {
			item = Item{Name: verb}
			if err := c.RequireItem(item); err != nil {
				return fmt.Errorf("%v\n\n%s", err, workflowUsage)
			}
		}
		c.printWorkflow(c.WorkflowFor(item, ""), item.Name)
		return nil
	}
}

// workflowFlags splits a subcommand's arguments into names and flags, refusing a flag that
// subcommand does not take.
//
// Refusing rather than ignoring, because of `accept`: a mistyped -Y that silently means "no flag
// at all" is the difference between recording an acceptance and being asked about one, and the
// two must never be one keystroke apart.
func workflowFlags(args []string, allowed ...string) ([]string, map[string]bool, error) {
	var names []string
	flags := map[string]bool{}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			names = append(names, a)
			continue
		}
		ok := false
		for _, f := range allowed {
			ok = ok || a == f
		}
		if !ok {
			return nil, nil, fmt.Errorf("unknown flag %q\n\n%s", a, workflowUsage)
		}
		flags[a] = true
	}
	return names, flags, nil
}

// printHostWorkflows compares this machine's bundles with another's. The comparison is a hash of
// workflow.yaml, which is what names every script, so a change to it is the change worth
// reporting: push is idempotent, and this is what keeps the drift it reintroduces visible.
func (c Config) printHostWorkflows(host string) error {
	rows, err := c.HostWorkflows(host)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Printf("neither %s nor this machine has any workflows of its own\n", host)
		return nil
	}
	for _, r := range rows {
		fmt.Printf("  %-20s %s\n", r.Name, r.State)
	}
	return nil
}

// workflowKeys is what the table prints and how each value is read, in one list.
//
// One list rather than a name list beside a switch. They were two, in different orders with
// nothing holding them together, so a key added to one and not the other either printed as "-"
// forever or never printed at all, and neither failed a test or a compile.
//
// Deliberately not sorted: the agent first, then where the work lands, then the layout, then the
// programs wisp hands off to. `program` first is worth more than alphabetical.
var workflowKeys = []struct {
	key string
	get func(Config, Workflow) string
}{
	{"program", func(_ Config, w Workflow) string { return w.Program }},
	{"branch", func(_ Config, w Workflow) string { return w.Branch }},
	{"worktree", func(_ Config, w Workflow) string { return w.Worktree }},
	{"parent", func(_ Config, w Workflow) string { return w.Item.Parent }},
	{"seed", func(c Config, w Workflow) string {
		// A shipped seed has no path to print, so the files themselves are the value.
		if w.SeedFS != nil {
			var names []string
			if ents, err := fs.ReadDir(w.SeedFS, "."); err == nil {
				for _, e := range ents {
					names = append(names, e.Name())
				}
			}
			return strings.Join(names, ", ") + " (shipped)"
		}
		return c.displayPath(w.Item.Seed)
	}},
	{"layout", func(_ Config, w Workflow) string { return layoutSummary(w.Layout) }},
	{"source", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Source) }},
	{"new", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.New) }},
	{"context", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Context) }},
	{"close", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Close) }},
	{"provision", func(c Config, w Workflow) string {
		if w.ProvisionsInGo() {
			return "built-in (git worktree)"
		}
		return c.displayPath(w.Hooks.Provision)
	}},
	{"open", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Open) }},
	{"kill", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Kill) }},
	{"preview", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Preview) }},
	{"remote_label", func(_ Config, w Workflow) string { return w.Picker.RemoteLabel }},
	{"needs_input", func(_ Config, w Workflow) string { return w.Status.NeedsInput }},
}

// overrides names the keys some layer other than the built-in supplied, in the order the table
// below prints them. It is what the header says when nothing named a bundle: the built-in's
// description is no longer true of a resolution that has had half its keys replaced.
func overrides(w Workflow) []string {
	var out []string
	for _, k := range workflowKeys {
		if src := w.From[k.key]; src != "" && src != "built-in" {
			out = append(out, k.key)
		}
	}
	return out
}

// displayPath is a hook path as a person would like to read it: relative to the workspace when it
// is inside one, with ~ for the home directory otherwise.
//
// Hooks are resolved to absolute paths so wisp can run them from anywhere, and printing them that
// way put a hundred-character temp path in a nine-row table and pushed the `from` column, which is
// the column anybody ran this command for, off the side of every row.
func (c Config) displayPath(p string) string {
	if p == "" {
		return ""
	}
	if rel, err := filepath.Rel(c.Workspace, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return shortPath(p)
}

// ellipsize keeps a value inside the column, cutting from the left because the identifying end of
// a path or a command is its tail.
func ellipsize(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "..." + s[len(s)-(max-3):]
}

// value renders one key for the table. A key nothing sets prints as "-" rather than as an empty
// column, so "wisp runs no close hook" is visibly an answer.
func (c Config) value(w Workflow, key string) string {
	for _, k := range workflowKeys {
		if k.key == key {
			if v := k.get(c, w); v != "" {
				return v
			}
			return "-"
		}
	}
	return "-"
}

func layoutSummary(wins []Window) string {
	if len(wins) == 0 {
		return ""
	}
	names := make([]string, 0, len(wins))
	for _, win := range wins {
		names = append(names, win.Window)
	}
	return fmt.Sprintf("%d %s: %s", len(wins), plural(len(wins), "window", "windows"), strings.Join(names, ", "))
}

// printWorkflow is the one renderer every form of this command shares. The third column is the
// whole point of it: a value on its own says what wisp will do, and says nothing about which of
// five files to edit to change it.
func (c Config) printWorkflow(w Workflow, item string) {
	head := w.Name
	switch {
	// A bundle named this workflow, so its own description is the description of what runs.
	case w.From["name"] != "":
		if w.Description != "" {
			head += ": " + w.Description
		}
	// Nothing named one, so this is the built-in with keys taken off it by a config file. Its
	// description describes the built-in, and printing it above rows that say `program` came from
	// .wisp.yaml is the header contradicting the table.
	case len(overrides(w)) > 0:
		head += ": the built-in, with " + strings.Join(overrides(w), ", ") + " overridden"
	default:
		if w.Description != "" {
			head += ": " + w.Description
		}
	}
	if item != "" {
		head += "\nfor " + item
	}
	fmt.Println(head)

	addr, selected, lives := w.Addr, w.From["workflow"], shortPath(w.Dir)
	if addr == "" {
		addr = "default"
	}
	if selected == "" {
		selected = "nothing names one, so the built-in"
	}
	if lives == "" {
		lives = "compiled into wisp"
		if w.Addr != "" && w.Addr != "default" && isShipped(w.Addr) && w.From["name"] != "" {
			lives = "shipped with wisp"
		}
	}
	fmt.Printf("\n  address   %s\n  selected  %s\n  lives in  %s\n\n", addr, selected, lives)

	// Padded to the widest value rather than to a fixed column: the needs_input marker is a whole
	// sentence, and a fixed width either wraps it or wastes half the screen on every other row.
	// Capped, because one absolute path to a hook would otherwise push the from column off the
	// side for all nine rows, and that column is why anyone ran this.
	const widest = 44
	wide := len("value")
	// Rendered once and measured, rather than computed again for the print pass: shortPath and
	// layoutSummary are not free, and the two passes would have to agree exactly for the columns
	// to line up.
	vals := make([]string, len(workflowKeys))
	for i, k := range workflowKeys {
		// Cut to the cap rather than merely excluded from measuring it. Excluding a long value
		// kept the header narrow and then printed the long row anyway, which is the one thing
		// worse than a wide column: eight rows that line up and one that does not.
		vals[i] = ellipsize(c.value(w, k.key), widest)
		if n := len(vals[i]); n > wide {
			wide = n
		}
	}
	fmt.Printf("  %-12s %-*s %s\n", "key", wide, "value", "from")
	for i, k := range workflowKeys {
		from := w.From[k.key]
		if from == "" {
			from = "-"
		}
		fmt.Printf("  %-12s %-*s %s\n", k.key, wide, vals[i], from)
	}

	// The layout summary above names the windows and stops there, which is enough to see that the
	// layout changed and not enough to see how. The detail is why anybody looked.
	if len(w.Layout) > 0 {
		fmt.Println()
		for _, win := range w.Layout {
			cwd := win.Cwd
			if cwd == "" {
				cwd = "workspace"
			}
			run := win.Run
			if run == "" {
				run = "(a shell)"
			}
			var extra []string
			if win.For != "" {
				extra = append(extra, "for "+win.For)
			}
			if win.When != "" {
				extra = append(extra, "when "+win.When)
			}
			if win.Focus {
				extra = append(extra, "focus")
			}
			line := fmt.Sprintf("  %-12s %-10s %-26s", win.Window, cwd, run)
			if len(extra) > 0 {
				line += " [" + strings.Join(extra, ", ") + "]"
			}
			line = strings.TrimRight(line, " ")
			fmt.Println(line)
		}
	}

	if len(w.Notes) > 0 {
		fmt.Println()
		for _, n := range w.Notes {
			fmt.Printf("  note: %s\n", n)
		}
	}
}

// resolveAddr resolves one address on its own: the built-in, overlaid by that bundle and nothing
// else.
//
// Deliberately not the whole stack. `wisp workflow` already answers "what is in effect here",
// and folding the config layers in here as well would answer that question twice while leaving
// "what does this bundle actually set" unanswerable, which is the question you have when you are
// choosing between two of them or reading someone else's.
func (c Config) resolveAddr(addr string) Workflow {
	w := c.builtinResolved()
	if addr == "" {
		w.applyBuiltinBundle()
		c.gateScripts(&w)
		return w
	}
	w.Addr, w.From["workflow"] = addr, "the command line"

	bundle, err := c.loadBundle(addr)
	if err != nil {
		if !errors.Is(err, errSilentBuiltin) {
			w.Notes = append(w.Notes, err.Error())
		}
		w.applyBuiltinBundle()
		c.gateScripts(&w)
		return w
	}
	w.applyBundle(bundle, addr)
	// Gated here too, though `show` runs nothing. The command's job is to say what this bundle
	// would do in this workspace, and a row naming a script the gate will refuse is the command
	// answering a question nobody asked.
	c.gateScripts(&w)
	w.Notes = append(w.Notes, w.validate()...)
	return w
}

// printWorkflowList shows what is addressable, not what is in effect. Two rows may share a name
// and be two different workflows, which is exactly what the addresses are for.
func (c Config) printWorkflowList() {
	for _, e := range c.ListWorkflows(c.WorkflowFor(Item{}, "").Addr) {
		mark := " "
		if e.InUse {
			mark = "*"
		}
		fmt.Printf("%s %-20s %-42s %s\n", mark, e.Addr, e.Where, e.Note)
	}
}

// workflowInit writes a starting point: a copy of one of the bundles wisp ships, `default`
// unless --from names another. It never writes over one that is there.
//
// A copy of a shipped bundle rather than a template of its own, so `init` and `show default`
// cannot drift: the file init writes is the file the built-in is documented by. An empty file
// would work just as well, since every key falls back on its own; it would also teach nothing,
// and the spelling of the keys is the part nobody can guess.
func (c Config) workflowInit(name, from string, here bool) error {
	name = strings.TrimPrefix(strings.TrimSpace(name), "./")
	if !safeWorkflowName(name) {
		return fmt.Errorf("%q is not a workflow name: one directory segment, no slashes\n\n  wisp workflow init solo", name)
	}
	if from == "" {
		from = "default"
	}
	if !isShipped(from) {
		return fmt.Errorf("wisp does not ship a workflow called %q\n\nthe ones it does: %s", from, strings.Join(shippedNames, ", "))
	}

	root, addr := UserWorkflowsDir(), name
	if here {
		root, addr = c.WorkspaceWorkflowsDir(), "./"+name
	}
	if root == "" {
		return fmt.Errorf("cannot locate a config directory to put it in\n\nset XDG_CONFIG_HOME, or use --here to keep it in the workspace")
	}
	dir := filepath.Join(root, name)

	// Nothing wisp does destroys work. A workflow already there is a file somebody has edited,
	// and there is no version of "start it over" worth the one time that was not what was meant.
	if exists(dir) {
		return fmt.Errorf("%s already exists\n\nedit it:\n  wisp workflow edit %s\n\nor pick another name", shortPath(dir), addr)
	}
	if err := copyShipped(from, dir); err != nil {
		return fmt.Errorf("could not write %s: %w", shortPath(dir), err)
	}
	path := filepath.Join(dir, WorkflowFile)
	// Through the YAML node rather than a template substitution, so every comment in the
	// shipped file survives into the copy: those comments are the documentation.
	if err := setConfigKey(path, "name", name); err != nil {
		return fmt.Errorf("could not name %s: %w", shortPath(path), err)
	}

	what := "the built-in"
	if from != "default" {
		what = "the shipped " + from + " workflow"
	}
	fmt.Printf("%s\n\nit is a copy of %s, so it changes nothing until you edit it:\n  wisp workflow edit %s\n  wisp workflow use %s\n", shortPath(path), what, addr, addr)
	if here {
		// A workspace's own workflow is the one that arrives with a repo rather than with you, so
		// it is the one that has to be read before it runs. Said here, at the moment it is made,
		// rather than left to surface as a note on the next session.
		fmt.Printf("\nthis one is the workspace's, so it also has to be accepted before it runs:\n  wisp workflow accept %s\n", addr)
	}
	return nil
}

// workflowUse binds to a workflow: yours by default, this workspace's with --here.
//
// The two files answer different questions. The user config is "this is how I work", inherited by
// every workspace that does not say otherwise; the workspace file is "this is how work happens
// here", and travels with the repos to everyone else who checks them out.
func (c Config) workflowUse(addr string, here bool) error {
	summary, err := c.bindWorkflow(addr, here)
	if err != nil {
		return err
	}
	fmt.Print(summary)
	fmt.Printf("\n  wisp workflow   what that changed, key by key\n")
	return nil
}

// BindWorkflow writes `workflow: <addr>` into this workspace's .wisp.yaml, for the picker's
// first-run question. The summary is what workflowUse would have printed.
func (c Config) BindWorkflow(addr string) (string, error) { return c.bindWorkflow(addr, true) }

// Unbound reports that no layer names a workflow here, so the built-in is running by default
// rather than by choice. A remote workspace is never unbound from this end: its files are the
// far side's to bind.
func (c Config) Unbound() bool {
	if c.IsRemote() {
		return false
	}
	return c.WorkflowFor(Item{}, "").From["workflow"] == ""
}

func (c Config) bindWorkflow(addr string, here bool) (string, error) {
	addr = strings.TrimSpace(addr)
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return "", fmt.Errorf("%v\n\na workflow is one name: `solo` for one of yours, `./solo` for one this workspace ships", err)
	}
	// `default` with no directory of yours by that name is the built-in, which is always there,
	// and so is anything else wisp ships.
	if !isDir(dir) && !isShipped(addr) {
		flag := ""
		if IsWorkspaceWorkflow(addr) {
			flag = " --here"
		}
		return "", fmt.Errorf("no workflow %q at %s\n\nmake it first:\n  wisp workflow init %s%s\n\nor see what there is:\n  wisp workflow list",
			addr, shortPath(dir), strings.TrimPrefix(addr, "./"), flag)
	}

	path := UserConfigPath()
	if here {
		path = filepath.Join(c.Workspace, MarkerFile)
	}
	if path == "" {
		return "", fmt.Errorf("cannot locate a config directory to record this in\n\nset XDG_CONFIG_HOME, or use --here to record it in the workspace")
	}
	if err := setConfigKey(path, "workflow", addr); err != nil {
		return "", err
	}

	summary := fmt.Sprintf("workflow: %s\n  in %s\n", addr, shortPath(path))
	if IsWorkspaceWorkflow(addr) {
		if ok, _ := c.WorkflowAccepted(addr); !ok {
			summary += fmt.Sprintf("\nnothing of it runs until it has been read and accepted:\n  wisp workflow accept %s\n", addr)
		}
	}
	return summary, nil
}

// setConfigKey writes one top-level key into a config file, leaving the rest of it as it was.
//
// Through a yaml.Node round trip rather than by marshalling a struct, for the reason writeInto
// gives: the file belongs to whoever wrote it, and binding a workflow must not reformat their
// comments away. The append path is there because yaml.v3 carries comments across a round trip
// but not blank lines, so a file that does not hold the key yet is better off gaining a line at
// the end than being re-emitted. This is the usual case: `.wisp.yaml` ships entirely commented
// out, and a config with no `workflow:` in it is every config until the first time this runs.
func setConfigKey(path, key, value string) error {
	var doc yaml.Node
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w\n\nfix the file by hand; wisp will not rewrite one it cannot read", shortPath(path), err)
		}
	case !os.IsNotExist(err):
		return err
	}

	root := documentRoot(&doc)
	if mapValue(root, key) == nil {
		return writeConfig(path, appendBlock(raw, fmt.Sprintf("%s: %s\n", key, quoteYAML(value))))
	}
	setMapValue(root, key, &yaml.Node{Kind: yaml.ScalarNode, Value: value})

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeConfig(path, out.Bytes())
}

// workflowEdit opens a bundle's manifest and re-reads it afterwards.
//
// The re-read is the point of doing this through wisp at all rather than through the editor
// alone: a manifest that no longer parses costs its keys silently, and the moment to be told is
// while you still remember what you changed.
func (c Config) workflowEdit(addr string) error {
	if addr == "" {
		addr = c.WorkflowFor(Item{}, "").Addr
	}
	dir := ""
	if addr != "" {
		var err error
		if dir, err = c.WorkflowDir(addr); err != nil {
			return err
		}
	}
	// The built-in is compiled in, so there is no file to open. `init` is the answer, and it is a
	// copy of the built-in, which is what somebody asking to edit the built-in wanted.
	if dir == "" || !isDir(dir) {
		if addr == "" || addr == "default" {
			return fmt.Errorf("that is the built-in workflow, which is compiled into wisp\n\nmake your own copy of it and edit that:\n  wisp workflow init solo\n  wisp workflow use solo\n  wisp workflow edit solo")
		}
		if isShipped(addr) && !isDir(filepath.Join(UserWorkflowsDir(), addr)) {
			return fmt.Errorf("%s is shipped with wisp, so there is no file of yours to open\n\nmake your own copy of it and edit that:\n  wisp workflow init mine --from %s\n  wisp workflow use mine\n  wisp workflow edit mine", addr, addr)
		}
		return fmt.Errorf("no workflow %q at %s\n\nmake it first:\n  wisp workflow init %s", addr, shortPath(dir), strings.TrimPrefix(addr, "./"))
	}

	path := filepath.Join(dir, WorkflowFile)
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	// Split on spaces rather than handed to a shell, so an EDITOR of `code -w` works without a
	// path holding a space becoming two arguments somewhere inside sh -c.
	// Fields, then checked: an EDITOR of " " passes a non-empty test and splits into nothing,
	// which is one index away from a panic in the middle of someone opening a file.
	argv := strings.Fields(editor)
	if len(argv) == 0 {
		return fmt.Errorf("EDITOR is set to whitespace, so there is nothing to run\n\nset it to an editor that is installed, or unset it and wisp uses vi:\n  EDITOR=vi wisp workflow edit")
	}
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v\n\nset EDITOR to something that is installed, or edit %s directly", editor, err, shortPath(path))
	}

	edited, err := LoadWorkflowFile(dir)
	if err != nil {
		return fmt.Errorf("%v\n\nwisp falls back to the built-in for every key it cannot read there, so this\nis worth fixing now:\n  wisp workflow edit %s", err, addr)
	}
	// Straight after the editor exits is when a typo is cheapest to fix, and the note naming it
	// is already in hand: an unknown key parses fine and then quietly does nothing, which is the
	// worst thing this file can be.
	for _, note := range edited.Notes {
		fmt.Fprintf(os.Stderr, "wisp: %s\n", note)
	}
	fmt.Printf("%s\n\n  wisp workflow show %s   what it sets now\n", shortPath(path), addr)
	return nil
}

// printHookBodies prints every hook script a file names, in full, above the prompt that asks about
// it. base is what a relative path is relative to: the bundle for a bundle, the workspace for the
// two config files.
//
// One copy, shared by all three things that can be accepted. It was two copies and an omission:
// the bundle path and the workspace-config path each had their own loop, and the item path had
// none at all, so accepting an item authorised scripts it never showed you. The printing is not
// decoration here, it is the entire content of the decision, and a version of it that one caller
// can forget to do is a gate that quietly asks you to agree to nothing in particular.
//
// A script that is not there yet is said rather than skipped: a hook naming a file that does not
// exist is still a file whoever ships this decides the contents of later. Never truncated, because
// the interesting line in a script somebody else wrote is exactly as likely to be the last one as
// the first.
func (c Config) printHookBodies(hooks Hooks, base string) {
	for _, hook := range hooks.slots() {
		if *hook.dst == "" {
			continue
		}
		script := *hook.dst
		if !filepath.IsAbs(script) {
			script = filepath.Join(base, script)
		}
		printScript(script, hook.key+" hook")
	}
}

// printScript is the shape every script goes on screen in, wherever the list of them came from.
// Shared with the whole-workspace accept, which has absolute paths rather than a Hooks to walk.
func printScript(path, label string) {
	body, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("\n--- %s (%s): not there yet\n", shortPath(path), label)
		return
	}
	fmt.Printf("\n--- %s (%s)\n%s\n", shortPath(path), label, strings.TrimRight(string(body), "\n"))
}

// askOnce is the prompt every accept path ends in.
//
// One copy. There were three, and a fourth was about to be written for the whole-workspace form:
// each held its own spelling of "there is no terminal to ask on", and the one thing they must all
// agree about is that -y is required rather than assumed when stdin cannot answer. A prompt
// written to something that cannot answer is either a hang or a silent yes, and both are worse
// than a refusal that names the flag.
func askOnce(yes bool, question, command, refusal string) error {
	if yes {
		return nil
	}
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("this needs an answer and there is no terminal to ask on\n\nread the above and say so outright:\n  %s", command)
	}
	fmt.Printf("\n%s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return errors.New(refusal)
	}
	return nil
}

// recordScripts writes a content hash for every hook script this file names that the script gate
// will check.
//
// This is the other half of the rule, and it is why accepting a file is now accepting more than
// the file. `wisp workflow accept .wisp.yaml` printed the scripts to you and then recorded only
// the YAML, so a later commit could rewrite `scripts/setup.sh` and it stayed accepted and still
// ran: the prompt and the record described different bytes. They describe the same bytes now.
//
// Only what the gate checks, which is what is inside the workspace and on disk. A hook pointing at
// ~/bin/brief.sh is yours and is not gated, so recording it would write a line nothing reads.
func (c Config) recordScripts(hooks Hooks, base string) error {
	for _, s := range c.gatedScripts(hooks, base) {
		if err := c.writeInto("accepted", s.Key, s.Sum); err != nil {
			return err
		}
	}
	return nil
}

// acceptEverything is `wisp workflow accept` with no argument: everything this workspace would
// run, printed in full, authorised in one answer.
//
// Bare `accept` used to be a usage error, and changing that is not a convenience. The script gate
// refuses a script whoever named it, and the sharpest thing it refuses is the built-in's own
// `provision:` default, which no file names and therefore has no address anybody could type. A
// gate with no way to say yes is a gate that only ever says no, and the answer to that is not to
// invent an address for a compiled-in default: it is a command that means "everything here".
//
// It stops at the workspace. Items are accepted one at a time, because a vault holds hundreds of
// them and almost none set an executable key: enumerating them would ask you to authorise items
// you may never open, in a prompt long enough that nobody reads the part that mattered.
func (c Config) acceptEverything(yes bool) error {
	// The two config files are read here rather than taken off a resolution, because a resolution
	// that has not been allowed to load them cannot say what they hold.
	path := filepath.Join(c.Workspace, MarkerFile)
	space, spaceRaw, _ := readOverlayFile(path)
	spaceFolded, _ := space.workflow()
	user, _, _ := readOverlayFile(UserConfigPath())

	// probe is this config with the file gates already satisfied, so that resolution can be asked
	// what the *script* gate then refuses. The alternative was to walk the layers here and work
	// the hook list out a second time, and two derivations of "what would run" is exactly how a
	// prompt comes to describe something other than what the record covers.
	probe := c
	probe.Accepted = maps.Clone(c.Accepted)
	if probe.Accepted == nil {
		probe.Accepted = map[string]string{}
	}
	probe.wfCache = nil

	// pending is one file being offered, and the hooks it names: a file and the scripts it points
	// at are one decision, so they are printed together and recorded together.
	type pending struct {
		label, key, sum, body string
		hooks                 Hooks
		base                  string
		notes                 []string
	}
	var files []pending

	if len(executableKeys(spaceFolded)) > 0 && !c.acceptedBytes(MarkerFile, spaceRaw) {
		_, spaceNotes := space.workflow()
		files = append(files, pending{MarkerFile, c.acceptKey(MarkerFile), sumOf(spaceRaw), string(spaceRaw), spaceFolded.Hooks, c.Workspace, spaceNotes})
	}
	// The address the same way resolution picks it: the nearest layer that named one wins, and
	// only a ./ one is the workspace's to be accepted.
	addr := normalizeAddr(user.Workflow)
	if v := normalizeAddr(space.Workflow); v != "" {
		addr = v
	}
	// known is "this workspace has something the gate has an opinion about", accepted or not. It is
	// what tells "nothing here runs" apart from "all of it is already allowed", and those two must
	// not share a sentence: telling somebody their workspace runs nothing, about a workspace whose
	// close hook they authorised last week, is the command lying to make one branch shorter.
	known := len(executableKeys(spaceFolded)) > 0
	if IsWorkspaceWorkflow(addr) {
		if dir, err := c.WorkflowDir(addr); err == nil && isDir(dir) {
			known = true
			if sum, manifest, err := hashBundle(dir); err == nil && c.Accepted[c.acceptKey(addr)] != sum {
				bundle, perr := parseWorkflow(dir, manifest)
				if perr != nil {
					return fmt.Errorf("%v\n\nwisp cannot tell you what %s would run, so it will not record that you\nagreed to it. Fix the file, or ask whoever ships it to", perr, addr)
				}
				files = append(files, pending{addr, c.acceptKey(addr), sum, string(manifest), bundle.Hooks, dir, bundle.Notes})
			}
		}
	}
	for _, f := range files {
		probe.Accepted[f.key] = f.sum
	}

	w := probe.WorkflowFor(Item{}, "")
	var scripts []scriptRef
	seen := map[string]bool{}
	// A file's own scripts first, so they are printed under the file that named them, and marked as
	// shown there so the flat list below does not print them twice.
	shownWithFile := map[string]bool{}
	for _, f := range files {
		for _, s := range c.gatedScripts(f.hooks, f.base) {
			shownWithFile[s.Path] = true
			if !seen[s.Path] {
				seen[s.Path], scripts = true, append(scripts, s)
			}
		}
	}
	// Then what the gate actually refused, which is the list that includes the scripts no file
	// named at all: the built-in's `provision:` is the whole reason this command exists.
	for _, p := range w.Unaccepted {
		if seen[p] {
			continue
		}
		seen[p] = true
		if key, sum, ok := c.scriptRecord(p); ok {
			scripts = append(scripts, scriptRef{Path: p, Key: key, Sum: sum})
		}
	}

	total := len(files) + len(scripts)
	if total == 0 {
		if known || len(c.gatedScripts(w.Hooks, c.Workspace)) > 0 {
			fmt.Printf("everything %s would run is already accepted, exactly as it stands now\n\neach is recorded by its own contents, so editing any one of them asks again.\n", c.Name)
			return nil
		}
		fmt.Printf("%s runs nothing that has to be accepted\n\nno hook script inside this workspace, no `program:` and no layout command in a file\nthat arrived with a repository. There is nothing here to say yes to.\n", c.Name)
		return nil
	}

	for _, f := range files {
		fmt.Printf("--- %s\n%s\n", f.label, strings.TrimRight(f.body, "\n"))
		// What the file asked for and is not getting, said here rather than only in `wisp workflow`.
		// A hook refused for reaching outside its bundle is not on the list you are authorising, and
		// somebody reading this prompt is entitled to know that before they wonder why it never ran.
		for _, n := range f.notes {
			fmt.Printf("  note: %s\n", n)
		}
		c.printHookBodies(f.hooks, f.base)
	}
	for _, s := range scripts {
		if shownWithFile[s.Path] {
			continue
		}
		printScript(s.Path, "hook script")
	}

	fmt.Printf("\nthat is %d %s wisp would run in %s, all of it supplied by this workspace\nrather than by you.\n",
		total, plural(total, "thing", "things"), c.Name)
	if err := askOnce(yes,
		fmt.Sprintf("let all of it run in %s?", c.Name),
		"wisp workflow accept -y",
		"not accepted, and nothing was written\n\nuntil then wisp uses the built-in for those keys, and the session still opens"); err != nil {
		return err
	}
	for _, f := range files {
		if err := c.writeInto("accepted", f.key, f.sum); err != nil {
			return err
		}
	}
	for _, s := range scripts {
		if err := c.writeInto("accepted", s.Key, s.Sum); err != nil {
			return err
		}
	}
	fmt.Printf("\naccepted %d in %s, recorded in %s\n\neach is recorded by its own contents, so editing any one of them puts that one back\nto unaccepted. This is not a standing permission for whatever they become later.\n",
		total, c.Name, shortPath(UserConfigPath()))
	return nil
}

// acceptWorkspaceConfig records that this workspace's own .wisp.yaml has been read and may run
// the programs it names.
//
// The same decision as accepting a bundle, about the same kind of file: one that arrives with a
// repo and can start a process. It is the sharper of the two, because a bundle has to be named
// before it does anything and this file can set `provision:` or a `layout[].run` on its own.
func (c Config) acceptWorkspaceConfig(yes bool) error {
	path := filepath.Join(c.Workspace, MarkerFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%v\n\nthis workspace has no %s, so there is nothing in it to accept", err, MarkerFile)
	}
	var ov workflowOverlay
	if err := yaml.Unmarshal(raw, &ov); err != nil {
		return fmt.Errorf("%s: %v\n\nwisp cannot tell you what this would run, so it will not record that you agreed to it", shortPath(path), err)
	}
	folded, _ := ov.workflow()
	keys := executableKeys(folded)
	if len(keys) == 0 {
		fmt.Printf("%s runs nothing, so there is nothing to accept\n\nit sets no program, no hooks and no layout command, and every other key in it\nalready applies.\n", MarkerFile)
		return nil
	}
	if c.acceptedBytes(MarkerFile, raw) {
		fmt.Printf("%s is already accepted, exactly as it stands now\n", MarkerFile)
		return nil
	}

	fmt.Printf("--- %s\n%s\n", shortPath(path), strings.TrimRight(string(raw), "\n"))
	fmt.Printf("\nthis would let %s run: %s\n", MarkerFile, strings.Join(keys, ", "))
	c.printHookBodies(folded.Hooks, c.Workspace)

	if err := askOnce(yes,
		fmt.Sprintf("let this run in %s?", c.Name),
		"wisp workflow accept "+MarkerFile+" -y",
		"not accepted, and nothing was written\n\nuntil then wisp uses the built-in for those keys, and the session still opens"); err != nil {
		return err
	}
	if err := c.writeInto("accepted", c.acceptKey(MarkerFile), sumOf(raw)); err != nil {
		return err
	}
	// The scripts as well as the file. They were printed above, and a record of only the YAML was
	// a record of the names rather than of what was read.
	if err := c.recordScripts(folded.Hooks, c.Workspace); err != nil {
		return err
	}
	fmt.Printf("\naccepted %s in %s, and the scripts it names, recorded in %s\n\nediting it, or any of those scripts, puts it back to unaccepted, which is the point.\n",
		MarkerFile, c.Name, shortPath(UserConfigPath()))
	return nil
}

// acceptItemManifest records that an item's own orchestration.md has been read and may run the
// programs it names.
func (c Config) acceptItemManifest(name string, yes bool) error {
	path := filepath.Join(c.ItemDir(name), "orchestration.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%v\n\nthat item has no orchestration.md, so there is nothing in it to accept", err)
	}
	var ov workflowOverlay
	if err := yaml.Unmarshal(raw, &ov); err != nil {
		return fmt.Errorf("%s: %v\n\nwisp cannot tell you what this would run, so it will not record that you agreed to it", shortPath(path), err)
	}
	folded, _ := ov.workflow()
	keys := executableKeys(folded)
	if len(keys) == 0 {
		fmt.Printf("%s runs nothing, so there is nothing to accept\n", name)
		return nil
	}
	if c.acceptedBytes(name, raw) {
		fmt.Printf("%s is already accepted, exactly as it stands now\n", name)
		return nil
	}

	fmt.Printf("--- %s\n%s\n", shortPath(path), strings.TrimRight(string(raw), "\n"))
	fmt.Printf("\nthis would let %s run: %s\n", name, strings.Join(keys, ", "))
	// The scripts, in full, for the same reason the other two accept paths print theirs: the
	// decision is about what runs, and the manifest only says where to look. This one used to stop
	// at the key list, so it asked you to authorise `provision:` and `close:` scripts it never
	// showed you, and those paths resolve against the workspace, which is the side that wrote them.
	c.printHookBodies(folded.Hooks, c.Workspace)
	if err := askOnce(yes,
		fmt.Sprintf("let this run for %s?", name),
		"wisp workflow accept "+name+" -y",
		"not accepted, and nothing was written\n\nuntil then the item opens with the workspace's workflow, which is the normal one"); err != nil {
		return err
	}
	if err := c.writeInto("accepted", c.acceptKey(name), sumOf(raw)); err != nil {
		return err
	}
	if err := c.recordScripts(folded.Hooks, c.Workspace); err != nil {
		return err
	}
	fmt.Printf("\naccepted %s, and the scripts it names, recorded in %s\n\nediting its orchestration.md, or any of those scripts, puts it back to unaccepted.\n",
		name, shortPath(UserConfigPath()))
	return nil
}

// workflowAccept records that a workflow this workspace ships has been read and may run.
//
// The printing above the prompt is not decoration. This is the only security decision wisp has,
// and an accept that does not show what is being accepted is a keystroke that means nothing: the
// manifest names the scripts, so the manifest and those scripts are what goes on screen.
func (c Config) workflowAccept(addr string, yes bool) error {
	addr = strings.TrimSpace(addr)
	if addr == MarkerFile || addr == "./"+MarkerFile {
		return c.acceptWorkspaceConfig(yes)
	}
	// An item name is the third thing that can be accepted, and it looks like neither of the
	// others: two levels, and it exists in the vault.
	if strings.Contains(addr, "/") && !IsWorkspaceWorkflow(addr) && isDir(c.ItemDir(addr)) {
		return c.acceptItemManifest(addr, yes)
	}
	if !IsWorkspaceWorkflow(addr) {
		if isDir(filepath.Join(c.WorkspaceWorkflowsDir(), addr)) {
			return fmt.Errorf("%q names one of yours; this workspace ships one by that name too\n\nthe workspace's one is the one that needs accepting:\n  wisp workflow accept ./%s", addr, addr)
		}
		// An address holding a slash was reaching for an item, and got here because no item by
		// that name exists. Answering about workflows would send someone looking in the wrong
		// place for a directory that is missing from the other one.
		if strings.Contains(addr, "/") {
			return fmt.Errorf("no item %q in %s\n\n  wisp ls   what there is to name", addr, shortPath(c.VaultDir()))
		}
		// Nothing anywhere by that name. "yours already run" is true of a workflow of yours and
		// misleading about one you never made: it describes the rule and implies the directory.
		if dir := UserWorkflowsDir(); dir == "" || !isDir(filepath.Join(dir, addr)) {
			return fmt.Errorf("no workflow %q anywhere: not one of yours under %s, and not one this workspace ships\n\n  wisp workflow list   what there is to name",
				addr, shortPath(UserWorkflowsDir()))
		}
		return fmt.Errorf("only a workflow this workspace supplies has to be accepted, and those are addressed ./%s\n\n%q is one of yours, under %s, and yours already run",
			addr, addr, shortPath(UserWorkflowsDir()))
	}
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, WorkflowFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%v\n\nthere is nothing to accept: a workflow is a directory holding a %s", err, WorkflowFile)
	}
	sum, err := WorkflowSum(dir)
	if err != nil {
		return err
	}
	if c.Accepted[c.acceptKey(addr)] == sum {
		fmt.Printf("%s is already accepted, exactly as it stands now\n", addr)
		return nil
	}
	// A manifest that will not parse cannot be shown honestly: wisp cannot say which scripts it
	// names, so there is nothing here to make an informed decision about.
	bundle, err := LoadWorkflowFile(dir)
	if err != nil {
		return fmt.Errorf("%v\n\nwisp cannot tell you what this would run, so it will not record that you\nagreed to it. Fix the file, or ask whoever ships it to", err)
	}

	fmt.Printf("--- %s\n%s\n", shortPath(path), strings.TrimRight(string(raw), "\n"))
	// Relative to the bundle here, rather than to the workspace: that is what makes a bundle
	// copyable, and it is the one thing that differs between the three things you can accept.
	for _, n := range bundle.Notes {
		fmt.Printf("  note: %s\n", n)
	}
	c.printHookBodies(bundle.Hooks, dir)

	if err := askOnce(yes,
		fmt.Sprintf("let this run in %s?", c.Name),
		"wisp workflow accept "+addr+" -y",
		"not accepted, and nothing was written\n\nrun it again once you have read it:\n  wisp workflow accept "+addr); err != nil {
		return err
	}

	// The user config, never the workspace one: a workspace that could write this would be
	// accepting itself. writeInto owns that file's shape and keeps the comments in it.
	if err := c.writeInto("accepted", c.acceptKey(addr), sum); err != nil {
		return err
	}
	// The tree hash already covers every file in the bundle, so this looks redundant and is not.
	// The script gate asks its question of the path that came out, and a bundle inside the
	// workspace produces scripts inside the workspace: they are gated like any other, which is what
	// "whoever named it" means. Recording them here is what keeps `accept ./ship` a single answer
	// rather than one answer followed by four more.
	if err := c.recordScripts(bundle.Hooks, dir); err != nil {
		return err
	}
	// "anything in it", not "the manifest": the hash covers the whole directory, which is what
	// makes showing you the scripts above worth anything. Naming only workflow.yaml here would
	// describe a narrower record than the one being written.
	fmt.Printf("\naccepted %s in %s, recorded in %s\n\nediting anything in it, the %s or a script it names, puts it back to\nunaccepted, which is the point: this is not a standing permission for whatever\nthe bundle becomes later.\n",
		addr, c.Name, shortPath(UserConfigPath()), WorkflowFile)
	return nil
}
