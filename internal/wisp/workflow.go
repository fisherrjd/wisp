package wisp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	New       string `yaml:"new"`
	Context   string `yaml:"context"`
	Close     string `yaml:"close"`
	Provision string `yaml:"provision"`
	Open      string `yaml:"open"`
	Kill      string `yaml:"kill"`
	Preview   string `yaml:"preview"`
}

// hookSlot is one hook by name, with a pointer at where it lives, so every place that has to
// walk "all the hooks" walks one list.
type hookSlot struct {
	key string
	dst *string
}

// slots is the hooks in the order they are printed and folded: where items come from, how one is
// named, what the agent is told, what finishing does, how a worktree is built, and the three
// around a session. There were seven hand-kept copies of this list before it existed, and a hook
// added to six of them printed as "-" in the seventh forever.
func (h *Hooks) slots() []hookSlot {
	return []hookSlot{
		{"source", &h.Source},
		{"new", &h.New},
		{"context", &h.Context},
		{"close", &h.Close},
		{"provision", &h.Provision},
		{"open", &h.Open},
		{"kill", &h.Kill},
		{"preview", &h.Preview},
	}
}

// ItemSpec is what a workflow says about the folder an item is: what a bare name is filed under
// and what a new folder starts out holding. The name of the notes file and where `done:` lives
// are deliberately not here: two workflows on one vault disagreeing about where the flag is would
// be a vault that shows finished work as open depending on who is looking.
type ItemSpec struct {
	// Seed is a directory whose top-level files are copied into a new item folder, tokens
	// expanded, never over a file that is already there. Resolved like a hook path: relative to
	// the bundle in a bundle, to the workspace in a config file.
	Seed string `yaml:"seed"`
	// Parent is where a bare typed name lands. "" infers a repo and asks when it cannot; a name
	// (usually _adhoc) files every bare name there without asking, which is what a notes-only
	// workflow wants.
	Parent string `yaml:"parent"`
}

// Picker is what a workflow says about the list. Words only: the list, the tree and the footer
// stay wisp's, and a workflow decides what the rows are called rather than how they are drawn.
type Picker struct {
	// RemoteLabel is the word beside + in the legend and on the help page for rows that came
	// from the source. gitlab by default, because that is what the built-in source is.
	RemoteLabel string `yaml:"remote_label"`
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
	Item     ItemSpec `yaml:"item"`
	Picker   Picker   `yaml:"picker"`
	// SeedFS is where a shipped bundle's seed files are read from, since they are in the binary
	// rather than at Item.Seed. Nil for every bundle on disk.
	SeedFS fs.FS `yaml:"-"`

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
	// Unaccepted holds the absolute paths of the hook scripts the script gate refused, in the
	// order it met them.
	//
	// It exists so that `wisp workflow accept` with no argument can offer exactly what the gate
	// refused rather than working the list out a second time from the layers. Two derivations of
	// "what would run here" are how a prompt comes to describe something other than what the
	// record covers, which is the whole of S2.
	Unaccepted []string `yaml:"-"`
	// Refused is the same information keyed by hook: the path some layer named that the gate would
	// not run, whether unaccepted or not there. It exists for one consumer, the provisioner: a
	// provision script somebody named and wisp withheld must not quietly become the built-in
	// provisioner, and the only way to tell "nobody named one" from "one was refused" is this.
	Refused map[string]string `yaml:"-"`
}

// ProvisionsInGo reports that no script is in effect and none was refused, so the built-in
// provisioner builds this workspace's worktrees.
func (w Workflow) ProvisionsInGo() bool {
	return w.Hooks.Provision == "" && w.From["provision"] == "built-in"
}

// settleProvision records the built-in provisioner as the answer when nothing else is. It runs
// after the gate, so a refused script keeps the row empty and the provisioner out of it.
func (w *Workflow) settleProvision() {
	if w.From == nil {
		w.From = map[string]string{}
	}
	if w.Hooks.Provision == "" {
		if w.Refused["provision"] != "" {
			delete(w.From, "provision")
			return
		}
		w.From["provision"] = "built-in"
	}
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
		Picker: Picker{RemoteLabel: "gitlab"},
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
	w.Refused = map[string]string{}
	for _, k := range []string{"program", "branch", "worktree", "provision", "needs_input", "layout", "remote_label"} {
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
	if bundle.SeedFS != nil {
		w.SeedFS = bundle.SeedFS
	}
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

// applyBuiltinBundle lays the shipped `default` bundle over the floor when nothing else loaded.
//
// The floor, builtinWorkflow(), stays what it always was: overlay cannot unset a key, so a floor
// carrying a seed would make a bare workflow unreachable from any bundle. What "the built-in"
// means to a workspace is the floor plus this file, and every key it supplies is labelled
// built-in because that is what it is. Name, Addr and Dir are left alone: nothing named a bundle.
func (w *Workflow) applyBuiltinBundle() {
	b, ok := shippedBundle("default")
	if !ok {
		return
	}
	w.overlay("built-in", b, "")
	if b.SeedFS != nil {
		w.SeedFS = b.SeedFS
	}
	if b.Description != "" {
		w.Description = b.Description
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
		relaxed.Notes = append(relaxed.Notes, stripBundleEscapes(dir, &relaxed)...)
		return relaxed, nil
	}
	w.Dir = dir
	// Here rather than at the point the paths are joined on, so that every reader of a manifest
	// sees the same bundle: `accept` prints what it is about to authorise out of this, and a
	// refusal the prompt did not know about would print a script the resolution then ignored.
	w.Notes = append(w.Notes, stripBundleEscapes(dir, &w)...)
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
	set("parent", o.Item.Parent, &w.Item.Parent)
	set("remote_label", o.Picker.RemoteLabel, &w.Picker.RemoteLabel)

	hook := func(key, val string, dst *string) {
		if val == "" {
			return
		}
		if !filepath.IsAbs(val) {
			val = filepath.Join(base, val)
		}
		*dst, w.From[key] = val, src
	}
	theirs, mine := o.Hooks.slots(), w.Hooks.slots()
	for i := range theirs {
		hook(theirs[i].key, *theirs[i].dst, mine[i].dst)
	}
	// A seed directory is data rather than a program, but it is addressed exactly like one: the
	// bundle is the unit that gets copied, and a template outside it would not come along.
	hook("seed", o.Item.Seed, &w.Item.Seed)

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
	Item     ItemSpec `yaml:"item"`
	Picker   Picker   `yaml:"picker"`

	// The flat spellings, for a config file that would rather not nest a single hook under
	// `hooks:`. Both are accepted; the nested one wins if somebody writes both.
	Source    string `yaml:"source"`
	New       string `yaml:"new"`
	Context   string `yaml:"context"`
	Close     string `yaml:"close"`
	Provision string `yaml:"provision"`
	Open      string `yaml:"open"`
	Kill      string `yaml:"kill"`
	Preview   string `yaml:"preview"`
}

// workflow folds the two accepted spellings into one shape, and says when a file used both.
//
// Every other conflict in resolution produces a note; this was the one silent precedence rule
// left, and "the nested one wins" is not something anyone would guess from a file that sets both.
func (o workflowOverlay) workflow() (Workflow, []string) {
	w := Workflow{
		Program: o.Program, Branch: o.Branch, Worktree: o.Worktree,
		Hooks: o.Hooks, Layout: o.Layout, Status: o.Status,
		Item: o.Item, Picker: o.Picker,
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
	flat := Hooks{Source: o.Source, New: o.New, Context: o.Context, Close: o.Close, Provision: o.Provision, Open: o.Open, Kill: o.Kill, Preview: o.Preview}
	flats, nested := flat.slots(), w.Hooks.slots()
	for i := range flats {
		fold(flats[i].key, *flats[i].dst, nested[i].dst)
	}
	return w, notes
}

// readOverlayFile pulls the workflow keys out of a YAML config file. Absent file, absent keys.
//
// It hands back the bytes it parsed as well as the keys, because two of these files are gated on
// a hash and the hash has to be of the bytes that were applied. See acceptedBytes.
func readOverlayFile(path string) (workflowOverlay, []byte, bool) {
	var o workflowOverlay
	raw, err := os.ReadFile(path)
	if err != nil {
		return o, nil, false
	}
	if err := yaml.Unmarshal(raw, &o); err != nil {
		return o, nil, false
	}
	return o, raw, true
}

// readOverlayFrontmatter pulls the same keys out of an item's orchestration.md, which is where
// per-item intent already lives, beside repos and branches.
//
// The bytes it returns are the whole file's, not the frontmatter's. That is what acceptance
// records and what the accept prompt printed, and hashing only the half the keys came out of
// would leave the rest of the file free to change under an acceptance somebody gave to all of it.
func readOverlayFrontmatter(path string) (workflowOverlay, []byte, bool) {
	var o workflowOverlay
	raw, err := os.ReadFile(path)
	if err != nil {
		return o, nil, false
	}
	fm, err := extractFrontmatter(raw)
	if err != nil || len(fm) == 0 {
		return o, nil, false
	}
	if err := yaml.Unmarshal(fm, &o); err != nil {
		return o, nil, false
	}
	return o, raw, true
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
	for _, h := range w.Hooks.slots() {
		if *h.dst != "" {
			keys = append(keys, h.key)
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

// acceptedBytes reports whether these exact bytes are the ones recorded against this key.
//
// The bytes rather than the path, and that is the whole of the point. Both gates used to take a
// path and read it for themselves, a second read of a file resolution had already read to find
// out what it says, so the bytes that were checked and the bytes that were applied were only the
// same bytes as long as nobody wrote to the file in between. A writer flipping .wisp.yaml between
// an accepted body and `program: EVIL` in a loop wins that race in milliseconds, and the writer
// this design is worried about is an agent that has read something hostile and can write into the
// vault, which is exactly the thing a loop like that is cheap for. loadBundle has said "one walk,
// used for both the gate and the parse" since it was written; this is that discipline for the two
// files that had not got it.
func (c Config) acceptedBytes(key string, raw []byte) bool {
	return c.Accepted[c.acceptKey(key)] == sumOf(raw)
}

// The script gate. One rule, and it is worth stating on its own line because two holes were
// closed by writing it down rather than by adding a third mechanism:
//
//	Any hook script wisp would run that lives inside the workspace must be accepted by its own
//	content, whoever named it.
//
// Two consequences, and each of them was a hole:
//
// **Provenance is irrelevant.** "built-in", your user config, the workspace config, a bundle and
// an item are all subject to it. The built-in's own `provision:` default is
// `.claude/scripts/provision-worktree.sh` joined onto the workspace root, so a repo that anchors a
// workspace and ships that path ships an executable wisp runs, and the file gate never looked at
// it: that gate only ever inspects what a file *says*, and nothing said this. The asymmetry was
// the tell. Writing `provision: .claude/scripts/provision-worktree.sh` in .wisp.yaml was gated and
// the identical default was not.
//
// **Location is what matters, not who wrote the line.** A script inside the workspace is
// workspace-supplied, because the workspace is the thing that arrives with a repository. A script
// outside it that your own config names is yours: gating `~/bin/brief.sh` would ask every ordinary
// user about their own setup, and a gate that fires on everything is a gate nobody reads. The
// awkward corner is deliberate and correct: a hook in your user config pointing at a path inside
// the workspace IS gated, because the bytes are the workspace's even though the name is yours.
//
// The file gate stays, layered on top, because it covers `program:` and `layout[].run`, which are
// command lines rather than scripts and have no content of their own to hash.
//
// It reads up to four small files per resolution, and only ever the ones inside the workspace,
// which is the same bargain hashBundle takes: resolution is not a rare path, so the walk it
// refused to do for every bundle is not done here either. A workspace with no hooks in it pays one
// failed open for the built-in's `provision:` default and nothing else.
func (c Config) gateScripts(w *Workflow) {
	root := resolvePath(c.Workspace)
	for _, h := range w.Hooks.slots() {
		path := *h.dst
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.Workspace, path)
		}
		if !insideDir(root, path) {
			continue
		}
		key, sum, ok := c.scriptRecord(path)
		// Not there yet. Stripped, because a script with no content cannot have been accepted by
		// its content, and "accept the name now, add the bytes in a later commit" is exactly S2
		// wearing a different hat. Stripped *silently* when the built-in named it: its `provision:`
		// default is a path most workspaces do not have, and a workspace that runs nothing must
		// need no acceptance and produce no notes at all; the built-in provisioner takes over. A
		// path some file wrote is different: that is a name wisp would otherwise swap for a
		// different provisioner without a word, so it is refused and said.
		if !ok {
			from := w.From[h.key]
			*h.dst = ""
			delete(w.From, h.key)
			if from != "built-in" {
				w.Refused[h.key] = path
				w.Notes = append(w.Notes, fmt.Sprintf("%s names %s, which is not there", h.key, c.displayPath(path)))
			}
			continue
		}
		if c.Accepted[key] == sum {
			continue
		}
		*h.dst = ""
		delete(w.From, h.key)
		w.Refused[h.key] = path
		w.Unaccepted = append(w.Unaccepted, path)
		w.Notes = append(w.Notes, fmt.Sprintf(
			"%s names %s, a script this workspace supplies that has not been accepted; run `wisp workflow accept` after reading it",
			h.key, c.displayPath(path)))
	}
	w.settleProvision()
}

// scriptRecord is the accept key and the content hash for one hook script: the pair the gate
// compares and the pair every accept path writes. One function, so the prompt and the record
// cannot come to describe different bytes, which is the failure this whole file is about.
//
// Not ok means there is nothing on disk to hash. A script that does not exist must never be
// recorded as accepted, or the record would be of a name rather than of a file.
func (c Config) scriptRecord(path string) (key, sum string, ok bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	return c.acceptKey(c.scriptKey(path)), sumOf(body), true
}

// scriptKey names a hook script in `accepted:`, resolved and relative to the workspace.
//
// Resolved, so two names for one file (a symlink, a route through `..`) are one record rather than
// two, and relative, so the line in your config reads as the file it is about. acceptKey puts the
// workspace in front of it, as it does for every other accepted thing: the same relative path in
// two checkouts is two different scripts. The `script ` prefix keeps it out of the namespace of
// bundle addresses and item names, which are the other two things keyed here.
func (c Config) scriptKey(path string) string {
	root := resolvePath(c.Workspace)
	rel, err := filepath.Rel(root, resolvePath(path))
	if err != nil {
		rel = path
	}
	return "script " + filepath.ToSlash(rel)
}

// gatedScripts is what accepting a file has to record besides the file itself: the hook scripts it
// names that the gate above will check. base is what a relative path is relative to, the same
// answer printHookBodies uses, so the prompt and the record are of one list.
//
// Only the ones inside the workspace, because those are the only ones the gate checks, and only
// the ones that exist, because there is nothing else to hash. Recording a script outside the
// workspace would write a line nothing ever reads.
func (c Config) gatedScripts(hooks Hooks, base string) []scriptRef {
	root := resolvePath(c.Workspace)
	var out []scriptRef
	seen := map[string]bool{}
	for _, h := range hooks.slots() {
		val := *h.dst
		if val == "" {
			continue
		}
		path := val
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		if !insideDir(root, path) || seen[path] {
			continue
		}
		seen[path] = true
		if key, sum, ok := c.scriptRecord(path); ok {
			out = append(out, scriptRef{Path: path, Key: key, Sum: sum})
		}
	}
	return out
}

// scriptRef is one hook script an accept is about to authorise.
type scriptRef struct {
	Path string
	Key  string
	Sum  string
}

// insideDir reports whether path lands inside root once every symlink and `..` in both of them is
// resolved.
//
// Resolved on both sides, and that is the whole of the correctness here. An earlier finding on
// this branch was a containment check that examined the template instead of the result; this is
// the same mistake one layer down, and it is cheap to make: on macOS a workspace under /var is
// really under /private/var, so a check that resolved one side and not the other answered "outside"
// for every path in it. A symlink inside the workspace pointing out of it is the other direction of
// the same question, and it is the one an attacker would use.
func insideDir(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	target := resolvePath(path)
	// The directory itself is not a file inside itself, and the separator is what stops
	// /work-notes from reading as inside /work.
	return strings.HasPrefix(target, root+string(filepath.Separator))
}

// resolvePath follows symlinks as far as the filesystem goes and cleans the rest.
//
// EvalSymlinks alone is not enough, because the thing most worth deciding about is a script that
// does not exist yet: it fails outright on a missing leaf, and answering "not inside the
// workspace" for a path that is plainly inside it would exempt exactly the file an attacker has
// not committed yet. So the deepest ancestor that does exist is resolved and the rest is joined
// back on.
func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, base := filepath.Split(p)
	if dir == "" || base == "" || filepath.Clean(dir) == filepath.Clean(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(resolvePath(filepath.Clean(dir)), base)
}

// stripBundleEscapes drops a bundle hook whose path reaches outside the bundle directory.
//
// Refused outright rather than gated, because a bundle is hashed as a whole tree and a hook
// reaching out of it is byte-identical before and after the script it names is rewritten:
// `hooks: {close: ../../../shared/close.sh}` was accepted once and then free to become anything.
// The script gate above catches that one when the escape lands back inside the workspace, and this
// catches it wherever it lands. There is no legitimate use: a bundle is meant to be self-contained
// and copyable, which is the property a relative path out of it destroys.
func stripBundleEscapes(dir string, w *Workflow) []string {
	var notes []string
	// The seed directory walks with the hooks: it is not run, but it is copied and hashed, and
	// the two reasons a hook may not leave the bundle are exactly those.
	slots := append(w.Hooks.slots(), hookSlot{"seed", &w.Item.Seed})
	for _, h := range slots {
		val := *h.dst
		// Lexically, before any symlink is followed, because the two are different questions. A
		// symlink inside the bundle is fine: WalkDir lists it and ReadFile follows it, so its
		// content is in the tree hash. A `..` is not in the tree hash at all.
		if val == "" || filepath.IsAbs(val) {
			continue
		}
		rel, err := filepath.Rel(dir, filepath.Join(dir, val))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"hook `%s: %s` reaches outside the bundle, so it is ignored: a bundle is the unit that gets copied and hashed, and a script outside it is in neither",
			h.key, val))
		*h.dst = ""
	}
	return notes
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

	// The bytes are kept alongside the keys for the two layers that are gated on a hash of them.
	// A gate that re-read the file would be hashing whatever it holds now and applying what it
	// held a moment ago, which is a window a hostile writer can simply sit in a loop and hit.
	user, _, _ := readOverlayFile(UserConfigPath())
	space, spaceRaw, _ := readOverlayFile(filepath.Join(c.Workspace, MarkerFile))
	var itemOv workflowOverlay
	var itemRaw []byte
	if item.Name != "" {
		itemOv, itemRaw, _ = readOverlayFrontmatter(filepath.Join(c.ItemDir(item.Name), "orchestration.md"))
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
	} else if !loaded {
		w.applyBuiltinBundle()
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
		if !c.acceptedBytes(MarkerFile, spaceRaw) {
			if p := spaceFolded.Hooks.Provision; p != "" {
				w.Refused["provision"] = absAgainst(c.Workspace, p)
			}
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
	// Same impossibility as source: the item does not exist until something has named it.
	if iw.Hooks.New != "" {
		w.Notes = append(w.Notes, "an item cannot set `new`: it names items, and this one has already been named")
		iw.Hooks.New = ""
	}
	if iw.Item.Parent != "" {
		w.Notes = append(w.Notes, "an item cannot set `item.parent`: it decides where new items land, and this one has already landed")
		iw.Item.Parent = ""
	}
	if iw.Picker.RemoteLabel != "" {
		w.Notes = append(w.Notes, "an item cannot set `picker.remote_label`: the legend is drawn once for the workspace, not per item")
		iw.Picker.RemoteLabel = ""
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
		if !c.acceptedBytes(item.Name, itemRaw) {
			if p := iw.Hooks.Provision; p != "" {
				w.Refused["provision"] = absAgainst(c.Workspace, p)
			}
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

	// Last, over the answer rather than over any one layer, because the question it asks is about
	// the path that came out and not about the file that named it. Asking it per layer would have
	// been the same mistake as gating by provenance: the built-in's provision default is a layer
	// nobody wrote, and it is the one that had to be caught.
	c.gateScripts(&w)

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
	w.Unaccepted = slices.Clone(w.Unaccepted)
	w.From = maps.Clone(w.From)
	w.Refused = maps.Clone(w.Refused)
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
		// Any other name wisp ships resolves out of the binary when you have no directory by
		// that name. Yours shadows it, exactly as yours shadows `default`.
		if b, ok := shippedBundle(addr); ok {
			return b, nil
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
	if w.Item.Parent != "" && safeSegment(w.Item.Parent) == "" {
		notes = append(notes, fmt.Sprintf("item.parent %q is not one directory name, ignored", w.Item.Parent))
		w.Item.Parent = ""
		delete(w.From, "parent")
	}
	if w.Item.Seed != "" && w.SeedFS == nil && !isDir(w.Item.Seed) {
		notes = append(notes, fmt.Sprintf("item.seed %s is not there, so new items get the plain stub", shortPath(w.Item.Seed)))
		w.Item.Seed = ""
		delete(w.From, "seed")
	}
	if l := w.Picker.RemoteLabel; l != "" && (strings.ContainsAny(l, " \t\n\r") || len(l) > 12) {
		notes = append(notes, fmt.Sprintf("picker.remote_label %q is not one short word, ignored", l))
		w.Picker.RemoteLabel = builtinWorkflow().Picker.RemoteLabel
		w.From["remote_label"] = "built-in"
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
//
// A token whose value is empty collapses, taking the whitespace after it with it, rather than
// leaving a gap where a word was. That is a run line's requirement rather than a nicety: the
// values are quoted before they are substituted, so an empty one arriving as a gap between two
// spaces became an empty argument, and an empty argument is not the same thing as no argument.
// When there is no whitespace after it because the token ends the line, the whitespace before it
// goes instead, which is the case `{program} {prompt}` is: nothing after the token to absorb the
// separator, so the separator itself has to go.
//
// It is one side or the other, never both, and never on any other grounds. An earlier version
// ate backwards whenever the next byte was not a space, which is the same condition as "the token
// is followed by a word", so `git log {base}..HEAD` became `git log..HEAD` and ran. {base} is
// empty for every item whose manifest omits it, so that was reachable from an ordinary bundle
// rather than a contrived one.
//
// The backward eat also stops at the last byte this template supplied, tracked as lit below.
// Whitespace that arrived inside a substituted value belongs to the value: `{a}{b}` with a
// ending in a space and b empty must keep that space, because nothing about b says anything
// about how a ends. The `window:`, `branch:` and `worktree:` templates substitute raw values
// throughout, so this is real there in a way it is not on a run line, where the quoting happens to
// end every value in a quote character and hide it.
//
// One pass, scanning for tokens, rather than a ReplaceAll per key. The difference is not
// efficiency: sequential replacement rescans what it has already substituted, so a value
// containing {repo} would be expanded again by a later key. That is reachable, because {prompt}
// carries the item's own notes, and the run line is shell-quoted before substitution, so a
// second expansion inside an already-quoted string breaks the quoting it was relying on. The
// text a value happens to contain must never be treated as template. The scan below only ever
// reads tmpl. The one thing that looks at b at all is the backward eat, and it is looking for a
// space to drop rather than for a token, which is what keeps that true.
func Expand(tmpl string, vars map[string]string) string {
	if tmpl == "" || !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	// A byte slice rather than a strings.Builder, which cannot give anything back once written: an
	// empty value has to be able to take the space before it with it.
	b := make([]byte, 0, len(tmpl))
	// lit is where the current run of bytes copied straight out of the template begins. It is the
	// floor the backward eat may not go below, and it moves up to the end of every value that is
	// substituted, since past that point the bytes are somebody's data rather than the template's
	// spacing.
	lit := 0
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
			// window it produced instead of silently becoming nothing. It is still template text,
			// so lit stays where it is.
			b = append(b, tmpl[i:i+end+1]...)
		case v == "":
			i += end + 1
			gap := i
			for i < len(tmpl) && (tmpl[i] == ' ' || tmpl[i] == '\t') {
				i++
			}
			if i > gap {
				// The whitespace after it went with it, so the whitespace before it stays.
				continue
			}
			// Only the end of a line has nothing after it to absorb the separator. Anything else
			// following the token, a word, a `.`, another token, means the two sides were meant to
			// touch and there is nothing to collapse.
			if i < len(tmpl) && tmpl[i] != '\n' && tmpl[i] != '\r' {
				continue
			}
			for len(b) > lit && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
				b = b[:len(b)-1]
			}
			continue
		default:
			b = append(b, v...)
			lit = len(b)
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
		} else if isShipped(name) {
			where += "  (shadows the shipped one)"
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
		found := isShipped(current) && !isDir(filepath.Join(UserWorkflowsDir(), current))
		for _, e := range out {
			found = found || (e.Addr == current && e.Note == "")
		}
		builtinInUse = !found
	}
	for i := range out {
		out[i].InUse = out[i].InUse && !builtinInUse
	}
	for _, name := range shippedNames {
		if isDir(filepath.Join(UserWorkflowsDir(), name)) {
			continue // shadowed, and the row above says so
		}
		if name == "default" {
			out = append(out, WorkflowEntry{Addr: "default", Where: "built-in", Accepted: true, InUse: builtinInUse})
			continue
		}
		out = append(out, WorkflowEntry{Addr: name, Where: "shipped with wisp", Accepted: true, InUse: current == name && !builtinInUse})
	}
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

// absAgainst joins a relative path onto a base, and leaves an absolute one alone.
func absAgainst(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
