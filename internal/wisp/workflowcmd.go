package wisp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
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
  wisp workflow accept ./<name> [-y]
                              read a workflow this workspace ships, and allow it to run
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
		names, flags, err := workflowFlags(rest, "--here")
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return fmt.Errorf("usage: wisp workflow init <name> [--here]\n\nthe name is a directory: `wisp workflow init solo` makes solo yours")
		}
		return c.workflowInit(names[0], flags["--here"])

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
		if len(names) == 0 {
			return fmt.Errorf("usage: wisp workflow accept ./<name> [-y]\n\n`wisp workflow list` marks the ones still waiting on this")
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
	{"layout", func(_ Config, w Workflow) string { return layoutSummary(w.Layout) }},
	{"source", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Source) }},
	{"context", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Context) }},
	{"close", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Close) }},
	{"provision", func(c Config, w Workflow) string { return c.displayPath(w.Hooks.Provision) }},
	{"needs_input", func(_ Config, w Workflow) string { return w.Status.NeedsInput }},
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
	if w.Description != "" {
		head += ": " + w.Description
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
	fmt.Printf("  %-11s %-*s %s\n", "key", wide, "value", "from")
	for i, k := range workflowKeys {
		from := w.From[k.key]
		if from == "" {
			from = "-"
		}
		fmt.Printf("  %-11s %-*s %s\n", k.key, wide, vals[i], from)
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
		return w
	}
	w.Addr, w.From["workflow"] = addr, "the command line"

	bundle, err := c.loadBundle(addr)
	if err != nil {
		if !errors.Is(err, errSilentBuiltin) {
			w.Notes = append(w.Notes, err.Error())
		}
		return w
	}
	w.applyBundle(bundle, addr)
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

// workflowYAMLTemplate is what `init` writes: the built-in's own values, spelled out, with the
// reasoning beside each key.
//
// An empty file would work just as well, since every key falls back to the built-in on its own.
// It would also teach nothing, and the spelling of the keys is the part nobody can guess. Filled
// in with the name and with the needs-input marker, so the copy cannot drift from the built-in it
// claims to be.
const workflowYAMLTemplate = `# a wisp workflow. The directory is the unit: this file plus any scripts it
# names is the whole thing, and copying the directory copies the workflow.
#
# Every key is optional. Anything left out falls back to wisp's built-in
# workflow, one key at a time, so a workflow that changes one thing is four
# lines long.

name: %s
description: say what this one is for

# The agent. It is what {program} means in the layout below.
program: claude

# Where the work lands. Substitutions: {item} {slug} {repo}.
# worktree names a directory inside the workspace's worktrees dir, so it holds
# no slashes.
branch: "feature/{slug}"
worktree: "{repo}--{slug}"

# The session, one entry per tmux window, in order.
#
#   window  the name, truncated to 12 characters, as tmux window names are
#   cwd     workspace, worktree or home
#   run     handed to /bin/sh; leave it out for an interactive shell
#   for     each-worktree repeats the window once per repo in the item
#   when    provisioning limits it to items whose worktrees are not there yet
#   focus   the window selected when the session opens; the first one wins
#
# Substitutions: the ones above, plus {branch} {base} {worktree} {workspace}
# {program} {prompt} {wisp}. There is no other control flow on purpose: a
# config language that grows conditionals has become a bad programming
# language, and that is what the hooks below are for.
layout:
  - window: agent
    cwd: workspace
    run: "{program} {prompt}"
    focus: true
  - window: "{repo}"
    for: each-worktree
    cwd: worktree
  - window: provision
    when: provisioning
    cwd: workspace
    run: "{wisp} provision {item}"

# The programs wisp hands off to. A relative path here is relative to this
# directory rather than to the workspace, which is what lets the bundle be
# copied to another machine and still find its own scripts.
#
# hooks:
#   source: bin/items.sh        # where items come from, instead of the vault
#   context: bin/context.sh     # what the agent is told when a session opens
#   close: bin/close.sh         # what happens when an item is closed out
#   provision: bin/worktree.sh  # how a worktree is built

# How this workflow recognises its agent waiting on you, matched against the
# pane. The default below is a line out of Claude Code's permission dialog, so
# it is worth changing the moment program: is not Claude Code.
#
# status:
#   needs_input: %q
`

// workflowInit writes a starting point. It never writes over one that is there.
func (c Config) workflowInit(name string, here bool) error {
	name = strings.TrimPrefix(strings.TrimSpace(name), "./")
	if !safeWorkflowName(name) {
		return fmt.Errorf("%q is not a workflow name: one directory segment, no slashes\n\n  wisp workflow init solo", name)
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", shortPath(dir), err)
	}
	path := filepath.Join(dir, WorkflowFile)
	body := fmt.Sprintf(workflowYAMLTemplate, name, needsInputMarker)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("could not write %s: %w", shortPath(path), err)
	}

	fmt.Printf("%s\n\nit is a copy of the built-in, so it changes nothing until you edit it:\n  wisp workflow edit %s\n  wisp workflow use %s\n", shortPath(path), addr, addr)
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
	addr = strings.TrimSpace(addr)
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return fmt.Errorf("%v\n\na workflow is one name: `solo` for one of yours, `./solo` for one this workspace ships", err)
	}
	// `default` with no directory of yours by that name is the built-in, which is always there.
	if !isDir(dir) && addr != "default" {
		flag := ""
		if IsWorkspaceWorkflow(addr) {
			flag = " --here"
		}
		return fmt.Errorf("no workflow %q at %s\n\nmake it first:\n  wisp workflow init %s%s\n\nor see what there is:\n  wisp workflow list",
			addr, shortPath(dir), strings.TrimPrefix(addr, "./"), flag)
	}

	path := UserConfigPath()
	if here {
		path = filepath.Join(c.Workspace, MarkerFile)
	}
	if path == "" {
		return fmt.Errorf("cannot locate a config directory to record this in\n\nset XDG_CONFIG_HOME, or use --here to record it in the workspace")
	}
	if err := setConfigKey(path, "workflow", addr); err != nil {
		return err
	}

	fmt.Printf("workflow: %s\n  in %s\n", addr, shortPath(path))
	if IsWorkspaceWorkflow(addr) {
		if ok, _ := c.WorkflowAccepted(addr); !ok {
			fmt.Printf("\nnothing of it runs until it has been read and accepted:\n  wisp workflow accept %s\n", addr)
		}
	}
	fmt.Printf("\n  wisp workflow   what that changed, key by key\n")
	return nil
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

	if _, err := LoadWorkflowFile(dir); err != nil {
		return fmt.Errorf("%v\n\nwisp falls back to the built-in for every key it cannot read there, so this\nis worth fixing now:\n  wisp workflow edit %s", err, addr)
	}
	fmt.Printf("%s\n\n  wisp workflow show %s   what it sets now\n", shortPath(path), addr)
	return nil
}

// workflowAccept records that a workflow this workspace ships has been read and may run.
//
// The printing above the prompt is not decoration. This is the only security decision wisp has,
// and an accept that does not show what is being accepted is a keystroke that means nothing: the
// manifest names the scripts, so the manifest and those scripts are what goes on screen.
func (c Config) workflowAccept(addr string, yes bool) error {
	addr = strings.TrimSpace(addr)
	if !IsWorkspaceWorkflow(addr) {
		if isDir(filepath.Join(c.WorkspaceWorkflowsDir(), addr)) {
			return fmt.Errorf("%q names one of yours; this workspace ships one by that name too\n\nthe workspace's one is the one that needs accepting:\n  wisp workflow accept ./%s", addr, addr)
		}
		return fmt.Errorf("only a workflow this workspace supplies has to be accepted, and those are addressed ./%s\n\n%q would be one of yours, under %s, and yours already run",
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
	for _, hook := range []struct{ key, val string }{
		{"source", bundle.Hooks.Source},
		{"context", bundle.Hooks.Context},
		{"close", bundle.Hooks.Close},
		{"provision", bundle.Hooks.Provision},
	} {
		if hook.val == "" {
			continue
		}
		script := hook.val
		if !filepath.IsAbs(script) {
			script = filepath.Join(dir, script)
		}
		body, err := os.ReadFile(script)
		if err != nil {
			// Worth saying rather than skipping: a hook naming a file that is not there yet is
			// still a file this workspace decides the contents of later.
			fmt.Printf("\n--- %s (%s hook): not there yet\n", shortPath(script), hook.key)
			continue
		}
		// In full, never truncated. The interesting line in a script somebody else wrote is
		// exactly as likely to be the last one as the first.
		fmt.Printf("\n--- %s (%s hook)\n%s\n", shortPath(script), hook.key, strings.TrimRight(string(body), "\n"))
	}

	if !yes {
		// A prompt written to something that cannot answer is a hang or a silent yes, and both are
		// worse than a refusal that names the flag.
		st, err := os.Stdin.Stat()
		if err != nil || st.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("this needs an answer and there is no terminal to ask on\n\nread the above and say so outright:\n  wisp workflow accept %s -y", addr)
		}
		fmt.Printf("\nlet this run in %s? [y/N] ", c.Name)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return fmt.Errorf("not accepted, and nothing was written\n\nrun it again once you have read it:\n  wisp workflow accept %s", addr)
		}
	}

	// The user config, never the workspace one: a workspace that could write this would be
	// accepting itself. writeInto owns that file's shape and keeps the comments in it.
	if err := c.writeInto("accepted", c.acceptKey(addr), sum); err != nil {
		return err
	}
	fmt.Printf("\naccepted %s in %s, recorded in %s\n\nediting %s puts it back to unaccepted, which is the point: this is not a\nstanding permission for whatever the file becomes later.\n",
		addr, c.Name, shortPath(UserConfigPath()), WorkflowFile)
	return nil
}
