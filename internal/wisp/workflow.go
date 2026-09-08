package wisp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

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

// oneShotSource is what the provenance column calls the command line, and the one place that
// spelling is written down.
const oneShotSource = "--workflow"

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
	// the built-in, and Dir is also empty when a bundle was named but would not load, which is
	// what makes "compiled into wisp" the honest thing to print.
	Addr string `yaml:"-"`
	Dir  string `yaml:"-"`
	// OneShot records that the address came from a --workflow on the command line.
	//
	// A field rather than a test against From["workflow"], which is a label written for a person
	// to read: keying the provision window's behaviour off that string meant rewording a table
	// heading would silently stop the background half inheriting the flag.
	OneShot bool `yaml:"-"`
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

// builtinResolved is the built-in as a resolution starts: every key attributed to it, and its
// provisioning script made absolute against this workspace.
//
// Shared with resolveAddr rather than written twice. The key list here is the one that decides
// what the provenance column can say, and a second copy of it meant a new built-in key printed
// its source as blank in whichever command the author forgot.
func (c Config) builtinResolved() Workflow {
	w := builtinWorkflow()
	w.From = map[string]string{}
	for _, k := range []string{"program", "branch", "worktree", "provision", "needs_input", "layout"} {
		w.From[k] = "built-in"
	}
	// The built-in's provisioning script is workspace-relative, as it has always been.
	if w.Hooks.Provision != "" && !filepath.IsAbs(w.Hooks.Provision) {
		w.Hooks.Provision = filepath.Join(c.Workspace, w.Hooks.Provision)
	}
	return w
}

// applyBundle folds a loaded bundle over a resolution, taking its identity with it. Shared for
// the same reason builtinResolved is: two copies of "the name and description come from the
// bundle, if it set them" is one copy too many.
func (w *Workflow) applyBundle(bundle Workflow, src string) {
	w.Dir = bundle.Dir
	// Its notes come with it. Loading a bundle is the only thing that can produce an
	// unknown-key note, and appending them at one of the two call sites meant `wisp open`
	// reported a typo while `wisp workflow show`, the command whose whole job is explaining a
	// bundle, stayed silent about the same file.
	w.Notes = append(w.Notes, bundle.Notes...)
	w.overlay(src, bundle, bundle.Dir)
	// Recorded like any other key, because the header is the one line that claims to say what is
	// running and a name nobody supplied is the built-in's. Only a bundle can name a workflow, so
	// this is what tells the header apart from "the built-in, with keys taken off it".
	if bundle.Name != "" {
		w.Name, w.From["name"] = bundle.Name, src
	}
	if bundle.Description != "" {
		w.Description = bundle.Description
	}
}

// BuiltinWorkflow is the floor every resolution falls back to, exported for callers outside this
// package that need to say what wisp does with no configuration at all.
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
func safeWorkflowName(name string) bool { return safeSegment(name) != "" }

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
	raw, err := os.ReadFile(filepath.Join(dir, WorkflowFile))
	if err != nil {
		return Workflow{}, err
	}
	return parseWorkflow(dir, raw)
}

// parseWorkflow is LoadWorkflowFile with the bytes already in hand, so a caller that needed to
// hash the manifest does not read it a second time to find out what it says.
func parseWorkflow(dir string, raw []byte) (Workflow, error) {
	var w Workflow
	path := filepath.Join(dir, WorkflowFile)
	// KnownFields, so a misspelled or misplaced key is said rather than ignored. A workflow that
	// quietly does nothing is the worst thing this file can be: you edit it, nothing changes, and
	// there is no signal anywhere that you wrote `progam:`.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil && !errors.Is(err, io.EOF) {
		// Retried permissively: an unknown key should be a note, not a workflow that will not
		// load at all, and the strict pass cannot tell the two apart on its own.
		var relaxed Workflow
		if yaml.Unmarshal(raw, &relaxed) != nil {
			return w, fmt.Errorf("%s: %w", path, err)
		}
		relaxed.Dir, relaxed.Notes = dir, []string{fmt.Sprintf("%s: %v", shortPath(path), err)}
		return relaxed, nil
	}
	w.Dir = dir
	return w, nil
}

// WorkflowSum hashes everything in a bundle: the manifest and every file beside it.
//
// The manifest alone was not enough, and the gap was visible in the prompt. `wisp workflow
// accept` prints the manifest and every script it names, then asked you to agree to it; recording
// only the manifest meant a later change to one of those scripts left the bundle accepted, and
// the next close-out or briefing ran code nobody had read. The prompt and the record now describe
// the same bytes.
//
// This is a directory walk on a path the picker can reach, which is affordable because a bundle
// is a handful of small files and because it only ever runs for a workspace-supplied one, which
// is the only kind that is gated at all.
func WorkflowSum(dir string) (string, error) {
	sum, _, err := hashBundle(dir)
	return sum, err
}

// hashBundle returns the tree hash and the manifest's bytes, so a caller that needs both reads
// the directory once.
func hashBundle(dir string) (string, []byte, error) {
	var manifest []byte
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			return "", nil, err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			rel = filepath.Base(p)
		}
		if rel == WorkflowFile {
			manifest = body
		}
		// The path and the length go in as well as the content, so renaming a script or moving
		// bytes between two of them changes the hash.
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(body))
		h.Write(body)
	}
	if manifest == nil {
		return "", nil, fmt.Errorf("%s has no %s", shortPath(dir), WorkflowFile)
	}
	return hex.EncodeToString(h.Sum(nil)), manifest, nil
}

func sumOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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

// workflow folds the two accepted spellings into one shape, and says when a file used both.
//
// Every other conflict in resolution produces a note; this was the one silent precedence rule
// left, and "the nested one wins" is not something anyone would guess from a file that sets both.
func (o workflowOverlay) workflow() (Workflow, []string) {
	w := Workflow{
		Program: o.Program, Branch: o.Branch, Worktree: o.Worktree,
		Hooks: o.Hooks, Layout: o.Layout, Status: o.Status,
	}
	var notes []string
	fold := func(name string, flat string, nested *string) {
		switch {
		case flat == "":
		case *nested == "":
			*nested = flat
		default:
			notes = append(notes, fmt.Sprintf("%s is set both as `%s:` and under `hooks:`; the one under hooks wins", name, name))
		}
	}
	fold("source", o.Source, &w.Hooks.Source)
	fold("context", o.Context, &w.Hooks.Context)
	fold("close", o.Close, &w.Hooks.Close)
	fold("provision", o.Provision, &w.Hooks.Provision)
	return w, notes
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

// executableKeys names the keys in an overlay that cause wisp to run something someone else
// wrote: a command line, a hook script, or a layout window with a command in it.
//
// The distinction is the whole of the trust boundary. `branch:`, `worktree:` and
// `status.needs_input` are strings wisp interprets itself and can be honoured from any file;
// these are not.
func executableKeys(w Workflow) []string {
	var keys []string
	if w.Program != "" {
		keys = append(keys, "program")
	}
	for _, h := range []struct {
		name, val string
	}{{"source", w.Hooks.Source}, {"context", w.Hooks.Context}, {"close", w.Hooks.Close}, {"provision", w.Hooks.Provision}} {
		if h.val != "" {
			keys = append(keys, h.name)
		}
	}
	for _, win := range w.Layout {
		if win.Run != "" {
			keys = append(keys, "layout")
			break
		}
	}
	return keys
}

// stripExecutable removes the keys executableKeys names, leaving everything a file may say
// without being trusted.
func stripExecutable(w Workflow) Workflow {
	w.Program, w.Hooks = "", Hooks{}
	var kept []Window
	for _, win := range w.Layout {
		if win.Run == "" {
			kept = append(kept, win)
		}
	}
	// A layout that was only windows with commands in them is dropped entirely rather than
	// half-applied: half a layout is a session missing the window the agent runs in.
	if len(kept) == len(w.Layout) {
		w.Layout = kept
	} else {
		w.Layout = nil
	}
	return w
}

// WorkspaceConfigAccepted reports whether this workspace's own .wisp.yaml has been read and
// allowed to run things, as it currently stands.
//
// The file is gated for the same reason a workspace-supplied bundle is, and it is the sharper
// case: a bundle has to be named before it does anything, and this file can set `provision:`,
// `program:` or a `layout[].run` on its own. An attacker was never going to write
// `workflow: ./ship` when the same repo could simply set the keys directly.
func (c Config) WorkspaceConfigAccepted() (bool, bool, error) {
	raw, err := os.ReadFile(filepath.Join(c.Workspace, MarkerFile))
	if err != nil {
		return false, false, err
	}
	var ov workflowOverlay
	if yaml.Unmarshal(raw, &ov) != nil {
		return false, false, nil
	}
	folded, _ := ov.workflow()
	if len(executableKeys(folded)) == 0 {
		// Nothing in it runs anything, so there is nothing to accept.
		return true, false, nil
	}
	return c.Accepted[c.acceptKey(MarkerFile)] == sumOf(raw), true, nil
}

// itemManifestAccepted reports whether an item's orchestration.md has been read and allowed to
// run the programs it names, as it currently stands.
func (c Config) itemManifestAccepted(item Item) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(c.ItemDir(item.Name), "orchestration.md"))
	if err != nil {
		return false, err
	}
	return c.Accepted[c.acceptKey(item.Name)] == sumOf(raw), nil
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
//
// Memoized when the caller asked for it with CacheWorkflows, because resolution is two to five
// file reads and the picker's preview pane asks on every cursor move.
func (c Config) WorkflowFor(item Item, oneShot string) Workflow {
	if c.wfCache == nil {
		return c.resolveWorkflow(item, oneShot)
	}
	// The item and the one-shot are the whole of the question: everything else resolution reads
	// is a file, and the cache's lifetime is what says how stale a file's answer may be.
	key := item.Name + "\x00" + oneShot
	if w, ok := c.wfCache.get(key); ok {
		return w
	}
	w := c.resolveWorkflow(item, oneShot)
	c.wfCache.put(key, w)
	return w
}

// resolveWorkflow is WorkflowFor with the memoization peeled off, so the cache wraps one
// function rather than being threaded through the layers.
func (c Config) resolveWorkflow(item Item, oneShot string) Workflow {
	w := c.builtinResolved()

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
		{oneShot, oneShotSource},
	} {
		if v := normalizeAddr(cand.v); v != "" {
			addr, addrFrom = v, cand.src
		}
	}

	var bundle Workflow
	loaded := false
	if addr != "" {
		w.Addr, w.From["workflow"], w.OneShot = addr, addrFrom, addrFrom == oneShotSource
		b, err := c.loadBundle(addr)
		if err != nil {
			if !errors.Is(err, errSilentBuiltin) {
				w.Notes = append(w.Notes, err.Error())
			}
		} else {
			bundle, loaded = b, true
		}
	}
	// A bundle named by a config file is layer 2, under the keys that file sets beside it: that
	// is what makes "mostly this workflow, but this one key differently" work.
	if loaded && !w.OneShot {
		w.applyBundle(bundle, addr)
	}

	// The user config is yours by definition and is honoured whole.
	userFolded, userNotes := user.workflow()
	w.Notes = append(w.Notes, userNotes...)
	w.overlay(shortPath(UserConfigPath()), userFolded, c.Workspace)

	// The workspace file is not. It travels with the repo, so anything in it that runs a program
	// waits for the same acceptance a workspace-supplied bundle does. Everything else in the file
	// applies either way: the gate is on execution, not on configuration.
	spaceFolded, spaceNotes := space.workflow()
	w.Notes = append(w.Notes, spaceNotes...)
	if keys := executableKeys(spaceFolded); len(keys) > 0 {
		if ok, _, err := c.WorkspaceConfigAccepted(); err != nil || !ok {
			spaceFolded = stripExecutable(spaceFolded)
			w.Notes = append(w.Notes, fmt.Sprintf(
				"%s sets %s, which wisp would run; it has not been accepted, so run `wisp workflow accept %s` after reading it",
				MarkerFile, strings.Join(keys, ", "), MarkerFile))
		}
	}
	w.overlay(MarkerFile, spaceFolded, c.Workspace)

	// The item may not decide where items come from. Not a policy call: source runs before the
	// item exists, so an item having an opinion about it is a bootstrapping impossibility.
	iw, itemNotes := itemOv.workflow()
	w.Notes = append(w.Notes, itemNotes...)
	if iw.Hooks.Source != "" {
		w.Notes = append(w.Notes, "an item cannot set `source`: it decides which items exist, and this one does not yet")
		iw.Hooks.Source = ""
	}
	// Nor status. The pane scan runs over every live session at once and resolves one workflow
	// for the workspace, so an item's marker would be reported here and never consulted, which
	// is worse than refusing it.
	if iw.Status.NeedsInput != "" {
		w.Notes = append(w.Notes, "an item cannot set `status.needs_input`: the pane scan asks the workspace once, not each item")
		iw.Status.NeedsInput = ""
	}
	// And an item does not get to start a process on the strength of its own frontmatter alone.
	//
	// The vault is yours, but the agent writes into it, and an agent that has read something
	// hostile in a repo could put `program:` in an item's manifest and change what runs the next
	// time it is opened. That is a short path from "an agent read a file" to "an agent chose the
	// command", and it is the one this tool can least afford to leave open. Most items set none
	// of these, so the question is only ever asked about an item that wants something unusual,
	// which is exactly when it is worth asking.
	if keys := executableKeys(iw); len(keys) > 0 {
		if ok, err := c.itemManifestAccepted(item); err != nil || !ok {
			iw = stripExecutable(iw)
			w.Notes = append(w.Notes, fmt.Sprintf(
				"orchestration.md sets %s, which wisp would run; it has not been accepted, so run `wisp workflow accept %s` after reading it",
				strings.Join(keys, ", "), item.Name))
		}
	}
	w.overlay("orchestration.md", iw, c.Workspace)

	// A bundle named on the command line goes on top instead, because a one-shot is an
	// instruction rather than a default. `wisp open x --workflow review` that still ran the
	// program from .wisp.yaml would be doing most of what you asked and none of what you meant,
	// and there would be nothing on screen saying which half it kept.
	if loaded && w.OneShot {
		w.applyBundle(bundle, oneShotSource+" "+addr)
	}

	// WISP_PROGRAM has always been the last word on the agent command, and stays so.
	if v := os.Getenv("WISP_PROGRAM"); v != "" {
		w.Program, w.From["program"] = v, "WISP_PROGRAM"
	}

	w.Notes = append(w.Notes, w.validate()...)
	return w
}

// workflowCache holds resolved workflows for as long as a caller says a file's answer may be
// reused. There is no TTL: the lifetime is the caller's, which is the only party that knows when
// it last had a reason to believe the files changed.
type workflowCache struct {
	mu sync.Mutex
	m  map[string]Workflow
}

// CacheWorkflows returns a copy of the config that resolves each workflow once and remembers it.
//
// For the picker, which resolves a workflow per preview: arrowing through forty items was two to
// five file reads each, forty times, to answer a question whose inputs nobody touched. Everything
// else leaves this off, because a command that runs once has nothing to save and a config that
// remembered a workflow across an edit would be answering from before it.
func (c Config) CacheWorkflows() Config {
	c.wfCache = &workflowCache{m: map[string]Workflow{}}
	return c
}

// ForgetWorkflows drops what CacheWorkflows remembered, for the moment the caller knows the files
// may have moved under it: a refresh, or a config reloaded from disk.
func (c Config) ForgetWorkflows() {
	if c.wfCache == nil {
		return
	}
	c.wfCache.mu.Lock()
	defer c.wfCache.mu.Unlock()
	c.wfCache.m = map[string]Workflow{}
}

func (wc *workflowCache) get(key string) (Workflow, bool) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	w, ok := wc.m[key]
	return w.clone(), ok
}

func (wc *workflowCache) put(key string, w Workflow) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	wc.m[key] = w.clone()
}

// clone copies the parts of a Workflow a caller could append to or write through. A cache that
// handed out its own slices would have callers editing each other's answers: Notes in particular
// is appended to by everything that resolves one, and the second caller would inherit the first
// caller's notes and then add its own.
func (w Workflow) clone() Workflow {
	w.Layout = slices.Clone(w.Layout)
	w.Notes = slices.Clone(w.Notes)
	w.From = maps.Clone(w.From)
	return w
}

// loadBundle reads the bundle an address names, refusing a workspace one that has not been
// accepted. Every failure here is a note rather than an error: a workflow that will not load
// costs you its keys, not your session.
func (c Config) loadBundle(addr string) (Workflow, error) {
	dir, err := c.WorkflowDir(addr)
	if err != nil {
		return Workflow{}, fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
	}
	if !isDir(dir) {
		// A bare `default` with no directory of yours by that name is simply the built-in, which
		// is not a problem worth annotating.
		if addr == "default" {
			return Workflow{}, errSilentBuiltin
		}
		return Workflow{}, fmt.Errorf("no workflow %q at %s; using the built-in", addr, shortPath(dir))
	}
	// The whole bundle is hashed only where something is gated on the hash, which is a workspace
	// one. Hashing every bundle read every file beside every manifest, and resolution is not a rare
	// path: NeedsInputMarker resolves the workspace's workflow for each of Local, BoardItems,
	// Peers, LocalPeers and Sessions, none of which hold the picker's memo, so one of your own
	// bundles with a venv or a vendored checkout next to it made every board refresh walk the lot.
	// The gated case still reads the directory once rather than twice: hashBundle hands back the
	// manifest it had to read anyway.
	var raw []byte
	if IsWorkspaceWorkflow(addr) {
		sum, manifest, err := hashBundle(dir)
		if err != nil {
			return Workflow{}, fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
		}
		if c.Accepted[c.acceptKey(addr)] != sum {
			return Workflow{}, fmt.Errorf("workflow %q is supplied by this workspace and has not been accepted; run `wisp workflow accept %s` after reading it", addr, addr)
		}
		raw = manifest
	} else {
		manifest, err := os.ReadFile(filepath.Join(dir, WorkflowFile))
		if err != nil {
			return Workflow{}, fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
		}
		raw = manifest
	}
	bundle, err := parseWorkflow(dir, raw)
	if err != nil {
		return Workflow{}, fmt.Errorf("workflow %q: %v; using the built-in", addr, err)
	}
	return bundle, nil
}

// errSilentBuiltin marks the one "failure" nobody needs told about: naming `default` when you
// have not made one of your own. Matched with errors.Is rather than by an empty message, so the
// check says what it means and no future error can fall into it by accident.
var errSilentBuiltin = errors.New("the built-in workflow")

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
// A token whose value is empty collapses, taking the whitespace on one side with it, rather than
// leaving a gap where a word was. That is a run line's requirement rather than a nicety: the
// values are quoted before they are substituted, so an empty one arriving as a gap between two
// spaces became an empty argument, and an empty argument is not the same thing as no argument.
// One pass, scanning for tokens, rather than a ReplaceAll per key. The difference is not
// efficiency: sequential replacement rescans what it has already substituted, so a value
// containing {repo} would be expanded again by a later key. That is reachable, because {prompt}
// carries the item's own notes, and the run line is shell-quoted before substitution, so a
// second expansion inside an already-quoted string breaks the quoting it was relying on. The
// text a value happens to contain must never be treated as template.
func Expand(tmpl string, vars map[string]string) string {
	if tmpl == "" || !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	// A byte slice rather than a strings.Builder, which cannot give anything back once written: an
	// empty value has to be able to take the space before it with it.
	b := make([]byte, 0, len(tmpl))
	for i := 0; i < len(tmpl); {
		if tmpl[i] != '{' {
			b = append(b, tmpl[i])
			i++
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 0 {
			b = append(b, tmpl[i:]...)
			break
		}
		name := tmpl[i+1 : i+end]
		v, known := vars[name]
		switch {
		case !known:
			// An unknown token is left alone rather than blanked, so a typo is visible in the
			// window it produced instead of silently becoming nothing.
			b = append(b, tmpl[i:i+end+1]...)
		case v == "":
			// An empty value takes the whitespace beside it with it, so the token collapses rather
			// than leaving a hole. The tokens are mostly arguments in a run line, and `{program}
			// {prompt}` with nothing to say would otherwise hand the shell a trailing separator to
			// make an argument out of. One side only, never both: eating both would run the words
			// either side of the token together.
			i += end + 1
			gap := i
			for i < len(tmpl) && (tmpl[i] == ' ' || tmpl[i] == '\t') {
				i++
			}
			if i == gap {
				for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
					b = b[:len(b)-1]
				}
			}
			continue
		default:
			b = append(b, v...)
		}
		i += end + 1
	}
	return string(b)
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
	// The built-in is in use whenever nothing else actually loaded, which includes a bound
	// address that turned out not to resolve. Marking the row that failed would say a workflow
	// is running when the note two lines above says it is not.
	builtinInUse := current == "" || (current == "default" && !isDir(filepath.Join(UserWorkflowsDir(), "default")))
	if !builtinInUse {
		found := false
		for _, e := range out {
			found = found || (e.Addr == current && e.Note == "")
		}
		builtinInUse = !found
	}
	for i := range out {
		out[i].InUse = out[i].InUse && !builtinInUse
	}
	out = append(out, WorkflowEntry{Addr: "default", Where: "built-in", Accepted: true, InUse: builtinInUse})
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
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// isDir, not e.IsDir(): ReadDir does not follow symlinks, and every other consumer here
		// does. A bundle reached by a symlink, which is how you keep one in a git checkout, was
		// loadable and acceptable and invisible in the only command that lists them.
		if isDir(filepath.Join(root, e.Name())) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
