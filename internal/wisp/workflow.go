package wisp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A workflow is a directory. That is the whole idea, and it is what turns a handful of loose
// config keys into something you can name, copy, version and hand to someone else.
//
// wisp used to compile one workflow in: branch `feature/<slug>`, one agent window at the
// workspace root, a shell per worktree, a briefing naming two files in an order somebody chose.
// Every one of those is a preference wearing the clothes of a fact. They now live in a bundle,
// and the bundle wisp ships is one of the ones you can replace.
//
// Resolution is per key, across five layers, so a workflow that sets one thing is four lines
// long and everything it does not say falls back to the built-in. That is what makes the
// built-in complete rather than merely first: every key always has an answer, which is why a
// malformed bundle costs you one key rather than a session.

// WorkflowFile is the manifest inside a bundle directory.
const WorkflowFile = "workflow.yaml"

// Hooks are the programs a workflow hands wisp. Paths are resolved to absolute at load, against
// whichever layer supplied them: a bundle's paths are relative to the bundle, a config file's
// are relative to the workspace. Getting that wrong would make a bundle uncopyable.
type Hooks struct {
	Source    string `yaml:"source"`
	Context   string `yaml:"context"`
	Close     string `yaml:"close"`
	Provision string `yaml:"provision"`
}

// Window is one tmux window in a session's layout.
//
// For and When are the only control flow there is. Anything wanting more than substitution
// should be a script, which is the point of the hooks: a config language that grows conditionals
// has become a bad programming language.
type Window struct {
	// Window is the name, templated. Truncated to 12 characters at creation, as tmux window
	// names have always been here.
	Window string `yaml:"window"`
	// Cwd is workspace, worktree or home. worktree only means anything with For set.
	Cwd string `yaml:"cwd"`
	// Run is the command line, templated, handed to /bin/sh. Empty leaves an interactive shell.
	Run string `yaml:"run"`
	// For is "" for one window, or "each-worktree" for one per worktree in the manifest.
	For string `yaml:"for"`
	// When is "" for always, or "provisioning" for only when a worktree is still missing.
	When string `yaml:"when"`
	// Focus selects this window after the session is built. The first one wins.
	Focus bool `yaml:"focus"`
}

// Status is how a workflow recognises what its agent is doing from the pane.
//
// Small, and it matters more than its size. wisp used to match a literal string out of Claude
// Code's permission dialog, so anyone driving aider, codex or a bare shell had a "needs input"
// state that could never fire.
type Status struct {
	NeedsInput string `yaml:"needs_input"`
}

// Workflow is a resolved workflow: the built-in, overlaid by a bundle, overlaid by config, by
// the item, and by a one-shot flag, one key at a time.
type Workflow struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	Program  string   `yaml:"program"`
	Branch   string   `yaml:"branch"`
	Worktree string   `yaml:"worktree"`
	Hooks    Hooks    `yaml:"hooks"`
	Layout   []Window `yaml:"layout"`
	Status   Status   `yaml:"status"`

	// From records which layer supplied each key, which is the whole point of `wisp workflow`.
	// "why did my session open like that" is otherwise unanswerable without reading Go.
	From map[string]string `yaml:"-"`
	// Addr is the address that selected the bundle, Dir is where it was found. Both empty for
	// the built-in.
	Addr string `yaml:"-"`
	Dir  string `yaml:"-"`
	// Notes are non-fatal problems: a bundle that would not load, a key that was ignored. They
	// are surfaced rather than raised, because an unusable workflow must still open a session.
	Notes []string `yaml:"-"`
}

// builtinWorkflow is wisp's own workflow, compiled in. It reproduces the behaviour that used to
// be spread across session.go, manifest.go, workspace.go and tmux.go, and it is complete on
// purpose: it is the floor every other layer falls back to, key by key.
func builtinWorkflow() Workflow {
	return Workflow{
		Name:        "default",
		Description: "one agent at the workspace root, a shell per worktree",
		Program:     "claude",
		Branch:      "feature/{slug}",
		Worktree:    "{repo}--{slug}",
		Hooks:       Hooks{Provision: ".claude/scripts/provision-worktree.sh"},
		Layout: []Window{
			{Window: "agent", Cwd: "workspace", Run: "{program} {prompt}", Focus: true},
			{Window: "{repo}", For: "each-worktree", Cwd: "worktree"},
			{Window: "provision", When: "provisioning", Cwd: "workspace", Run: "{wisp} provision {item}"},
		},
		Status: Status{NeedsInput: needsInputMarker},
	}
}

// BuiltinWorkflow is the built-in, for `wisp workflow show default` and for `init` to copy.
func BuiltinWorkflow() Workflow { return builtinWorkflow() }

// UserWorkflowsDir is where your own bundles live: beside the user config, one directory each.
func UserWorkflowsDir() string {
	cfg := UserConfigPath()
	if cfg == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg), "workflows")
}

// WorkspaceWorkflowsDir is where a workspace's own bundles live, checked in beside its repos.
func (c Config) WorkspaceWorkflowsDir() string {
	return filepath.Join(c.Workspace, ".wisp", "workflows")
}

// ErrNoWorkflow means the address named a bundle that is not there.
var ErrNoWorkflow = errors.New("no such workflow")

// WorkflowDir maps an address to a directory. There is deliberately no search path.
//
// The shape of the value says where the workflow lives: a bare name is always one of yours, a
// leading ./ is always this workspace's. An earlier draft had a search path with first-hit-wins,
// which meant a repo shipping .wisp/workflows/solo would silently replace your solo in that
// workspace with nothing anywhere saying so. A name collision across a trust boundary is not a
// resolution problem to be ordered, it is an ambiguity to delete.
//
// The one shadowing left is yours over the built-in: a directory named `default` in your own
// workflows directory wins, because it is your directory and you meant it.
func (c Config) WorkflowDir(addr string) (string, error) {
	addr = normalizeAddr(addr)
	switch {
	case addr == "":
		return "", ErrNoWorkflow
	case strings.HasPrefix(addr, "./"):
		name := strings.TrimPrefix(addr, "./")
		if !safeWorkflowName(name) {
			return "", fmt.Errorf("%q is not a workflow name", addr)
		}
		return filepath.Join(c.WorkspaceWorkflowsDir(), name), nil
	default:
		if !safeWorkflowName(addr) {
			return "", fmt.Errorf("%q is not a workflow name", addr)
		}
		dir := UserWorkflowsDir()
		if dir == "" {
			return "", ErrNoWorkflow
		}
		return filepath.Join(dir, addr), nil
	}
}

// safeWorkflowName keeps an address to one path segment. A workflow address reaches the
// filesystem and can arrive from a checked-in file, so "../.." must not be a way to name a
// directory outside the two places workflows are allowed to live.
func safeWorkflowName(name string) bool {
	if name == "" || name != filepath.Clean(name) {
		return false
	}
	if strings.ContainsRune(name, filepath.Separator) || strings.Contains(name, "/") {
		return false
	}
	return name != "." && name != ".."
}

// IsWorkspaceWorkflow reports whether an address points into the workspace, which is the case
// that needs accepting before anything of it runs.
//
// Trimmed, and that is not tidiness. WorkflowDir trims before resolving, so " ./ship" is a
// workspace bundle as far as the filesystem is concerned; a gate that did not trim answered
// "no" for the same string and skipped the acceptance check entirely. One leading space in a
// checked-in .wisp.yaml was a workflow running unaccepted. Any two functions that decide what
// an address means have to normalise it identically, so both call this.
func IsWorkspaceWorkflow(addr string) bool {
	return strings.HasPrefix(normalizeAddr(addr), "./")
}

// normalizeAddr is the one definition of what an address is. Every consumer goes through it.
func normalizeAddr(addr string) string { return strings.TrimSpace(addr) }

// LoadWorkflowFile reads one bundle's manifest. Everything it does not set stays zero, so the
// caller can tell "said nothing" from "said this".
func LoadWorkflowFile(dir string) (Workflow, error) {
	var w Workflow
	raw, err := os.ReadFile(filepath.Join(dir, WorkflowFile))
	if err != nil {
		return w, err
	}
	if err := yaml.Unmarshal(raw, &w); err != nil {
		return w, fmt.Errorf("%s: %w", filepath.Join(dir, WorkflowFile), err)
	}
	w.Dir = dir
	return w, nil
}

// WorkflowSum hashes a bundle's manifest, for the accept-once record. The manifest is what names
// every script, so a change to it is the change worth re-asking about.
func WorkflowSum(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, WorkflowFile))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// acceptKey namespaces an accepted workflow by the workspace that supplied it. The same relative
// address in two workspaces is two different directories and has to be accepted twice.
func (c Config) acceptKey(addr string) string { return c.Workspace + " " + addr }

// WorkflowAccepted reports whether this workspace's bundle has been accepted as it currently
// stands. A changed manifest is not accepted, which is the point: accepting once must not be a
// standing permission for whatever the file becomes later.
func (c Config) WorkflowAccepted(addr string) (bool, error) {
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return false, err
	}
	sum, err := WorkflowSum(dir)
	if err != nil {
		return false, err
	}
	return c.Accepted[c.acceptKey(addr)] == sum, nil
}

// overlay folds one layer's non-empty values over the resolved workflow, recording where each
// came from. Empty means "said nothing" rather than "said empty": there is no case for unsetting
// a key back to nothing, and treating absence as an override would make every partial bundle
// wipe the built-in.
//
// base is what this layer's relative hook paths are relative to.
func (w *Workflow) overlay(src string, o Workflow, base string) {
	if w.From == nil {
		w.From = map[string]string{}
	}
	set := func(key, val string, dst *string) {
		if val == "" {
			return
		}
		*dst, w.From[key] = val, src
	}
	set("program", o.Program, &w.Program)
	set("branch", o.Branch, &w.Branch)
	set("worktree", o.Worktree, &w.Worktree)
	set("needs_input", o.Status.NeedsInput, &w.Status.NeedsInput)

	hook := func(key, val string, dst *string) {
		if val == "" {
			return
		}
		if !filepath.IsAbs(val) {
			val = filepath.Join(base, val)
		}
		*dst, w.From[key] = val, src
	}
	hook("source", o.Hooks.Source, &w.Hooks.Source)
	hook("context", o.Hooks.Context, &w.Hooks.Context)
	hook("close", o.Hooks.Close, &w.Hooks.Close)
	hook("provision", o.Hooks.Provision, &w.Hooks.Provision)

	if o.Layout != nil {
		w.Layout, w.From["layout"] = o.Layout, src
	}
}

// workflowOverlay is the subset of workflow keys a config file or an item manifest may set
// directly, alongside or instead of naming a bundle. A bundle is a bag of defaults; naming a key
// next to `workflow:` overrides just that key, which is what makes "mostly my workflow, but this
// workspace tracks work in Jira" a one-line change rather than a forked directory.
type workflowOverlay struct {
	Workflow string   `yaml:"workflow"`
	Program  string   `yaml:"program"`
	Branch   string   `yaml:"branch"`
	Worktree string   `yaml:"worktree"`
	Hooks    Hooks    `yaml:"hooks"`
	Layout   []Window `yaml:"layout"`
	Status   Status   `yaml:"status"`

	// The flat spellings, for a config file that would rather not nest a single hook under
	// `hooks:`. Both are accepted; the nested one wins if somebody writes both.
	Source    string `yaml:"source"`
	Context   string `yaml:"context"`
	Close     string `yaml:"close"`
	Provision string `yaml:"provision"`
}

func (o workflowOverlay) workflow() Workflow {
	w := Workflow{
		Program: o.Program, Branch: o.Branch, Worktree: o.Worktree,
		Hooks: o.Hooks, Layout: o.Layout, Status: o.Status,
	}
	if w.Hooks.Source == "" {
		w.Hooks.Source = o.Source
	}
	if w.Hooks.Context == "" {
		w.Hooks.Context = o.Context
	}
	if w.Hooks.Close == "" {
		w.Hooks.Close = o.Close
	}
	if w.Hooks.Provision == "" {
		w.Hooks.Provision = o.Provision
	}
	return w
}

// readOverlayFile pulls the workflow keys out of a YAML config file. Absent file, absent keys.
func readOverlayFile(path string) (workflowOverlay, bool) {
	var o workflowOverlay
	raw, err := os.ReadFile(path)
	if err != nil {
		return o, false
	}
	if err := yaml.Unmarshal(raw, &o); err != nil {
		return o, false
	}
	return o, true
}

// readOverlayFrontmatter pulls the same keys out of an item's orchestration.md, which is where
// per-item intent already lives, beside repos and branches.
func readOverlayFrontmatter(path string) (workflowOverlay, bool) {
	var o workflowOverlay
	raw, err := os.ReadFile(path)
	if err != nil {
		return o, false
	}
	fm, err := extractFrontmatter(raw)
	if err != nil || len(fm) == 0 {
		return o, false
	}
	if err := yaml.Unmarshal(fm, &o); err != nil {
		return o, false
	}
	return o, true
}

// WorkflowFor resolves the workflow in effect, per key, across every layer.
//
//	1  built-in                       always complete, so every key has an answer
//	2  the named bundle               workflow.yaml
//	3  ~/.config/wisp/config.yaml     the default binding for this host
//	4  <workspace>/.wisp.yaml         the workspace binds
//	5  <item>/orchestration.md        the item overrides
//	   oneShot                        a --workflow flag, above all of it, written nowhere
//
// The item may be zero, for the workspace-level answer. An item may override anything except
// where items come from: `source` decides that, and an item cannot have an opinion about it,
// since it does not exist until source has run.
func (c Config) WorkflowFor(item Item, oneShot string) Workflow {
	w := builtinWorkflow()
	w.From = map[string]string{}
	for _, k := range []string{"program", "branch", "worktree", "provision", "needs_input", "layout"} {
		w.From[k] = "built-in"
	}
	// The built-in's provisioning script is workspace-relative, as it has always been.
	if w.Hooks.Provision != "" && !filepath.IsAbs(w.Hooks.Provision) {
		w.Hooks.Provision = filepath.Join(c.Workspace, w.Hooks.Provision)
	}

	user, _ := readOverlayFile(UserConfigPath())
	space, _ := readOverlayFile(filepath.Join(c.Workspace, MarkerFile))
	var itemOv workflowOverlay
	if item.Name != "" {
		itemOv, _ = readOverlayFrontmatter(filepath.Join(c.ItemDir(item.Name), "orchestration.md"))
	}

	// The address: the nearest layer that named one wins outright. A bundle is selected, not
	// merged with another bundle; `extends:` would be the only thing that adds, and nothing has
	// wanted it.
	addr, addrFrom := "", ""
	for _, cand := range []struct{ v, src string }{
		{user.Workflow, shortPath(UserConfigPath())},
		{space.Workflow, MarkerFile},
		{itemOv.Workflow, "orchestration.md"},
		{oneShot, "--workflow"},
	} {
		if v := normalizeAddr(cand.v); v != "" {
			addr, addrFrom = v, cand.src
		}
	}

	var bundle Workflow
	var bundleDir string
	loaded := false
	if addr != "" {
		w.Addr, w.From["workflow"] = addr, addrFrom
		b, dir, err := c.loadBundle(addr)
		if err != nil {
			if err.Error() != "" {
				w.Notes = append(w.Notes, err.Error())
			}
		} else {
			bundle, bundleDir, loaded = b, dir, true
			w.Dir = dir
			if bundle.Name != "" {
				w.Name = bundle.Name
			}
			if bundle.Description != "" {
				w.Description = bundle.Description
			}
		}
	}
	// A bundle named by a config file is layer 2, under the keys that file sets beside it: that
	// is what makes "mostly this workflow, but this one key differently" work.
	if loaded && addrFrom != "--workflow" {
		w.overlay(addr, bundle, bundleDir)
	}

	w.overlay(shortPath(UserConfigPath()), user.workflow(), c.Workspace)
	w.overlay(MarkerFile, space.workflow(), c.Workspace)

	// The item may not decide where items come from. Not a policy call: source runs before the
	// item exists, so an item having an opinion about it is a bootstrapping impossibility.
	iw := itemOv.workflow()
	if iw.Hooks.Source != "" {
		w.Notes = append(w.Notes, "an item cannot set `source`: it decides which items exist, and this one does not yet")
		iw.Hooks.Source = ""
	}
	w.overlay("orchestration.md", iw, c.Workspace)

	// A bundle named on the command line goes on top instead, because a one-shot is an
	// instruction rather than a default. `wisp open x --workflow review` that still ran the
	// program from .wisp.yaml would be doing most of what you asked and none of what you meant,
	// and there would be nothing on screen saying which half it kept.
	if loaded && addrFrom == "--workflow" {
		w.overlay("--workflow "+addr, bundle, bundleDir)
	}

	// WISP_PROGRAM has always been the last word on the agent command, and stays so.
	if v := os.Getenv("WISP_PROGRAM"); v != "" {
		w.Program, w.From["program"] = v, "WISP_PROGRAM"
	}

	w.Notes = append(w.Notes, w.validate()...)
	return w
}

// loadBundle reads the bundle an address names, refusing a workspace one that has not been
// accepted. Every failure here is a note rather than an error: a workflow that will not load
// costs you its keys, not your session.
func (c Config) loadBundle(addr string) (Workflow, string, error) {
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return Workflow{}, "", fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
	}
	if !isDir(dir) {
		// A bare `default` with no directory of yours by that name is simply the built-in, which
		// is not a problem worth annotating.
		if addr == "default" {
			return Workflow{}, "", errSilentBuiltin
		}
		return Workflow{}, "", fmt.Errorf("no workflow %q at %s; using the built-in", addr, shortPath(dir))
	}
	if IsWorkspaceWorkflow(addr) {
		ok, err := c.WorkflowAccepted(addr)
		if err != nil {
			return Workflow{}, "", fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
		}
		if !ok {
			return Workflow{}, "", fmt.Errorf("workflow %q is supplied by this workspace and has not been accepted; run `wisp workflow accept %s` after reading it", addr, addr)
		}
	}
	bundle, err := LoadWorkflowFile(dir)
	if err != nil {
		return Workflow{}, "", fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
	}
	return bundle, dir, nil
}

// errSilentBuiltin marks the one "failure" nobody needs told about: naming `default` when you
// have not made one of your own.
var errSilentBuiltin = errors.New("")

// validate reports what wisp will ignore, so a typo is visible rather than merely ineffective.
func (w *Workflow) validate() []string {
	if w.From == nil {
		w.From = map[string]string{}
	}
	var notes []string
	if strings.Contains(w.Worktree, "/") {
		notes = append(notes, fmt.Sprintf("worktree %q contains a /: it names a directory inside `worktrees:`, not a path", w.Worktree))
	}
	seen := map[string]bool{}
	kept := w.Layout[:0:0]
	for i, win := range w.Layout {
		switch {
		case strings.TrimSpace(win.Window) == "":
			notes = append(notes, fmt.Sprintf("layout entry %d has no window name, skipped", i+1))
			continue
		case win.For != "" && win.For != "each-worktree":
			notes = append(notes, fmt.Sprintf("layout %q: unknown `for: %s`, skipped", win.Window, win.For))
			continue
		case win.When != "" && win.When != "provisioning":
			notes = append(notes, fmt.Sprintf("layout %q: unknown `when: %s`, skipped", win.Window, win.When))
			continue
		case win.Cwd != "" && win.Cwd != "workspace" && win.Cwd != "worktree" && win.Cwd != "home":
			notes = append(notes, fmt.Sprintf("layout %q: unknown `cwd: %s`, skipped", win.Window, win.Cwd))
			continue
		case win.For == "" && seen[win.Window]:
			notes = append(notes, fmt.Sprintf("layout %q: named twice, second one skipped", win.Window))
			continue
		}
		seen[win.Window] = true
		kept = append(kept, win)
	}
	// A layout that survives validation with nothing in it would open an empty session, which is
	// worse than opening the one wisp knows how to build.
	if len(kept) == 0 && len(w.Layout) > 0 {
		notes = append(notes, "no usable windows in `layout:`, falling back to the built-in layout")
		w.Layout = builtinWorkflow().Layout
		w.From["layout"] = "built-in"
	} else {
		w.Layout = kept
	}
	return notes
}

// Expand substitutes a workflow's template vocabulary. It is deliberately tiny and closed:
// {item} {slug} {repo} {branch} {base} {worktree} {workspace} {program} {prompt} {wisp}, plain
// substitution and nothing else.
func Expand(tmpl string, vars map[string]string) string {
	if tmpl == "" {
		return ""
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := tmpl
	for _, k := range keys {
		out = strings.ReplaceAll(out, "{"+k+"}", vars[k])
	}
	return out
}

// BranchFor is the branch an item's repo gets when the manifest does not name one outright.
func (w Workflow) BranchFor(item Item, repo string) string {
	return Expand(w.Branch, map[string]string{"slug": item.Slug(), "repo": repo, "item": item.Name})
}

// WorktreeName is the directory inside `worktrees:` holding one repo's checkout for one item.
func (w Workflow) WorktreeName(item Item, repo string) string {
	return Expand(w.Worktree, map[string]string{"slug": item.Slug(), "repo": repo, "item": item.Name})
}

// NeedsInputMarker is the workspace's needs-input signal, for the pane scan.
//
// The workspace's answer rather than each item's: the scan runs over every live session at once
// on every repaint, and resolving a workflow per row would read three files per row to answer a
// question that is the same for almost all of them.
func (c Config) NeedsInputMarker() string {
	return c.WorkflowFor(Item{}, "").Status.NeedsInput
}

// shortPath puts ~ back on a path under the home directory, for messages a person reads.
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + strings.TrimPrefix(p, home)
}

// WorkflowEntry is one row of `wisp workflow list`.
type WorkflowEntry struct {
	Addr     string
	Dir      string
	Where    string
	Note     string
	InUse    bool
	Accepted bool
}

// ListWorkflows is every workflow addressable from here: yours, this workspace's, and the
// built-in. Not a search path, a listing; two rows may share a name and they are two workflows.
func (c Config) ListWorkflows(current string) []WorkflowEntry {
	var out []WorkflowEntry
	add := func(addr, dir, where string) {
		e := WorkflowEntry{Addr: addr, Dir: dir, Where: where, InUse: addr == current}
		if _, err := os.Stat(filepath.Join(dir, WorkflowFile)); err != nil {
			e.Note = "no " + WorkflowFile
		}
		if IsWorkspaceWorkflow(addr) {
			ok, _ := c.WorkflowAccepted(addr)
			e.Accepted = ok
			if !ok {
				e.Note = "not yet accepted"
			}
		}
		out = append(out, e)
	}
	for _, name := range dirNames(UserWorkflowsDir()) {
		where := shortPath(UserWorkflowsDir())
		if name == "default" {
			where += "  (shadows the built-in)"
		}
		add(name, filepath.Join(UserWorkflowsDir(), name), where)
	}
	for _, name := range dirNames(c.WorkspaceWorkflowsDir()) {
		add("./"+name, filepath.Join(c.WorkspaceWorkflowsDir(), name), ".wisp/workflows")
	}
	out = append(out, WorkflowEntry{Addr: "default", Where: "built-in", Accepted: true,
		InUse: current == "" || (current == "default" && !isDir(filepath.Join(UserWorkflowsDir(), "default")))})
	return out
}

func dirNames(root string) []string {
	if root == "" {
		return nil
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
