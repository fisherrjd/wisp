package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Workflows are the layer that decides what a session is: which program runs, which branch gets
// cut, which windows open. Every test here is about one of two things. Either a layer that should
// win does win and says so, or a layer that should never reach the filesystem does not: a workflow
// address arrives from a checked-in file and a workflow directory holds scripts wisp runs.

// wfFixture isolates a run from the developer's own machine. Everything the resolver reads is
// under one of two temp roots, and nothing it reads is the real ~/.config.
type wfFixture struct {
	t *testing.T
	c Config
}

func newWorkflowFixture(t *testing.T) *wfFixture {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// WISP_PROGRAM is the last word on `program:`, above every layer. A developer with it exported
	// would otherwise decide the outcome of half of these.
	t.Setenv("WISP_PROGRAM", "")
	return &wfFixture{t: t, c: Config{
		Workspace: t.TempDir(),
		Vault:     "working_items",
		Worktrees: ".worktrees",
		Name:      "probe",
		Accepted:  map[string]string{},
	}}
}

func (f *wfFixture) write(path, body string) string {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *wfFixture) userConfig(body string)  { f.write(UserConfigPath(), body) }
func (f *wfFixture) spaceConfig(body string) { f.write(filepath.Join(f.c.Workspace, MarkerFile), body) }

func (f *wfFixture) itemFile(item, body string) {
	f.write(filepath.Join(f.c.ItemDir(item), "orchestration.md"), body)
}

func (f *wfFixture) userBundle(name, manifest string) string {
	dir := filepath.Join(UserWorkflowsDir(), name)
	f.write(filepath.Join(dir, WorkflowFile), manifest)
	return dir
}

func (f *wfFixture) spaceBundle(name, manifest string) string {
	dir := filepath.Join(f.c.WorkspaceWorkflowsDir(), name)
	f.write(filepath.Join(dir, WorkflowFile), manifest)
	return dir
}

// accept records the bundle as it currently stands, which is what `wisp workflow accept` writes.
func (f *wfFixture) accept(addr string) {
	f.t.Helper()
	dir, err := f.c.WorkflowDir(addr)
	if err != nil {
		f.t.Fatal(err)
	}
	sum, err := WorkflowSum(dir)
	if err != nil {
		f.t.Fatal(err)
	}
	f.c.Accepted[f.c.acceptKey(addr)] = sum
}

// trustSpaceConfig accepts the workspace file as it currently stands.
//
// Needed by every test whose subject is a hook rather than the gate: a .wisp.yaml that names a
// program or a script is checked-in code and does not run until somebody says so, which is the
// point, and is noise in a test about what a close hook does once it runs.
func (f *wfFixture) trustSpaceConfig() { trustSpaceConfigIn(f.t, f.c) }

func trustSpaceConfigIn(t *testing.T, c Config) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.Workspace, MarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	c.Accepted[c.acceptKey(MarkerFile)] = sumOf(raw)
}

// trustItem accepts an item's manifest as it currently stands, for tests whose subject is what
// an item may override rather than whether it had to ask first.
func (f *wfFixture) trustItem(name string) {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.c.ItemDir(name), "orchestration.md"))
	if err != nil {
		f.t.Fatal(err)
	}
	f.c.Accepted[f.c.acceptKey(name)] = sumOf(raw)
}

func hasNote(w Workflow, substr string) bool {
	for _, n := range w.Notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// testItem is a normal item: a repo half and a slug half, so {repo}, {slug} and {item} are all
// distinguishable in an expanded template.
var testItem = Item{Name: "wisp/42-fix-the-thing"}

// ---------------------------------------------------------------------------------------------
// 1. Addressing
// ---------------------------------------------------------------------------------------------

// The shape of an address is the whole of its meaning: bare is yours, ./ is this workspace's, and
// nothing else is anything. There is no search path to be confused by and no third place to reach.
func TestWorkflowDirAddressing(t *testing.T) {
	f := newWorkflowFixture(t)
	user, space := UserWorkflowsDir(), f.c.WorkspaceWorkflowsDir()

	for _, tc := range []struct {
		addr string
		want string
		why  string
	}{
		{"solo", filepath.Join(user, "solo"), "a bare name is always one of yours"},
		{"./solo", filepath.Join(space, "solo"), "a leading ./ is always this workspace's"},
		{"default", filepath.Join(user, "default"), "your own default shadows the built-in, because it is your directory"},
		{"  solo  ", filepath.Join(user, "solo"), "surrounding space in a hand-edited yaml file is not part of the name"},
		{"with-dash_and_underscore", filepath.Join(user, "with-dash_and_underscore"), "ordinary directory names stay ordinary"},
	} {
		got, err := f.c.WorkflowDir(tc.addr)
		if err != nil {
			t.Errorf("WorkflowDir(%q) refused it (%s): %v", tc.addr, tc.why, err)
			continue
		}
		if got != tc.want {
			t.Errorf("WorkflowDir(%q) = %q, want %q (%s)", tc.addr, got, tc.want, tc.why)
		}
	}

	if want := filepath.Join(filepath.Dir(UserConfigPath()), "workflows"); user != want {
		t.Errorf("UserWorkflowsDir = %q, want %q (beside the user config)", user, want)
	}
	if want := filepath.Join(f.c.Workspace, ".wisp", "workflows"); space != want {
		t.Errorf("WorkspaceWorkflowsDir = %q, want %q", space, want)
	}
}

// A workflow address reaches the filesystem and can arrive from a file checked into someone
// else's repo. Anything that names a directory outside the two places workflows may live is a way
// to run scripts from a directory nobody agreed to, so this is a boundary rather than a nicety.
func TestWorkflowDirRefusesTraversal(t *testing.T) {
	f := newWorkflowFixture(t)

	for _, addr := range []string{
		"",
		"   ",
		".",
		"..",
		"./",
		"./.",
		"./..",
		"../evil",
		"./../evil",
		"./../../evil",
		"../../../../etc",
		"a/b",
		"./a/b",
		"/etc/passwd",
		"./" + string(filepath.Separator) + "etc",
		"~/evil",
		"~",
		"sub/../../evil",
		"./sub/../../evil",
	} {
		if addr == "~" {
			continue // a directory literally called ~ is silly but stays inside the user dir
		}
		if _, err := f.c.WorkflowDir(addr); err == nil {
			t.Errorf("WorkflowDir(%q) was allowed; it does not name a workflow", addr)
		}
	}
}

// The invariant behind the case list above, stated once so a future address form cannot quietly
// widen it: whatever WorkflowDir accepts is a direct child of one of the two roots.
func TestWorkflowDirNeverLeavesItsRoots(t *testing.T) {
	f := newWorkflowFixture(t)
	roots := []string{filepath.Clean(UserWorkflowsDir()), filepath.Clean(f.c.WorkspaceWorkflowsDir())}

	for _, addr := range []string{
		"solo", "./solo", "default", "..", "./..", "../evil", "./../../evil", "a/b", "./a/b",
		"", ".", "./", "/etc", "~/evil", "  ./../x  ", "...", "..evil", "evil..",
	} {
		dir, err := f.c.WorkflowDir(addr)
		if err != nil {
			continue // refused, which is the other correct answer
		}
		clean := filepath.Clean(dir)
		ok := false
		for _, root := range roots {
			if filepath.Dir(clean) == root {
				ok = true
			}
		}
		if !ok {
			t.Errorf("WorkflowDir(%q) = %q, which is not directly inside %v", addr, clean, roots)
		}
	}
}

// BUG: the trust gate and the path resolver disagree about what an address is.
//
// WorkflowDir trims the address before resolving it, so " ./ship" lands in the workspace's
// workflow directory. IsWorkspaceWorkflow does not trim, so it reports false for the same string
// and loadBundle skips the acceptance check entirely. A .wisp.yaml holding `workflow: " ./ship"`
// therefore runs a workspace-supplied workflow that was never accepted, which is the one thing
// the accept record exists to prevent.
//
// Fix is one line: trim in IsWorkspaceWorkflow, or trim the address once on the way in.
func TestWorkspaceWorkflowTrustGateSeesTheSameAddressAsTheResolver(t *testing.T) {

	f := newWorkflowFixture(t)
	f.spaceBundle("ship", "name: ship\nprogram: codex\n")
	f.spaceConfig("workflow: \" ./ship\"\n")

	dir, err := f.c.WorkflowDir(" ./ship")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(dir, f.c.WorkspaceWorkflowsDir()) && !IsWorkspaceWorkflow(" ./ship") {
		t.Errorf("%q resolves to %q inside the workspace but is not treated as workspace-supplied", " ./ship", dir)
	}

	w := f.c.WorkflowFor(Item{}, "")
	if w.Program == "codex" {
		t.Errorf("an unaccepted workspace workflow was loaded via a leading space: program = %q", w.Program)
	}
}

// ---------------------------------------------------------------------------------------------
// 2. Per-key overlay and provenance
// ---------------------------------------------------------------------------------------------

// The claim that makes a four line workflow possible: a bundle that sets one key costs you one
// key. Everything it does not mention still comes from the built-in, and From says so, because
// "why did my session open like that" is otherwise unanswerable without reading Go.
func TestBundleOverlaysOneKeyAtATime(t *testing.T) {
	f := newWorkflowFixture(t)
	f.userBundle("solo", "name: solo\ndescription: just me\nprogram: aider\n")
	f.userConfig("workflow: solo\n")

	w := f.c.WorkflowFor(Item{}, "")

	if w.Program != "aider" || w.From["program"] != "solo" {
		t.Errorf("program = %q from %q, want aider from solo", w.Program, w.From["program"])
	}
	builtin := builtinWorkflow()
	for _, tc := range []struct{ key, got, want string }{
		{"branch", w.Branch, builtin.Branch},
		{"worktree", w.Worktree, builtin.Worktree},
		{"needs_input", w.Status.NeedsInput, builtin.Status.NeedsInput},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want the built-in %q: the bundle never mentioned it", tc.key, tc.got, tc.want)
		}
		if w.From[tc.key] != "built-in" {
			t.Errorf("%s came from %q, want built-in", tc.key, w.From[tc.key])
		}
	}
	if len(w.Layout) != len(builtin.Layout) || w.From["layout"] != "built-in" {
		t.Errorf("layout = %d windows from %q, want the built-in %d", len(w.Layout), w.From["layout"], len(builtin.Layout))
	}
	// The bundle's identity travels with it, so `wisp workflow` can name what is in force.
	if w.Name != "solo" || w.Description != "just me" || w.Addr != "solo" {
		t.Errorf("identity = %q/%q/%q, want solo/just me/solo", w.Name, w.Description, w.Addr)
	}
}

// The order the layers stand in, one key at a time. Each row adds a layer over the last and the
// nearest one to the item wins. The bundle is at the bottom on purpose: it is a bag of defaults,
// so "mostly my workflow, but this workspace cuts branches differently" is one line rather than a
// forked directory.
func TestWorkflowLayerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		user       string
		space      string
		item       string
		wantBranch string
		wantFrom   string
		why        string
	}{
		{
			name:       "bundle alone",
			user:       "workflow: base\n",
			wantBranch: "bundle/{slug}",
			wantFrom:   "base",
			why:        "nothing above it said anything",
		},
		{
			name:       "user config beats the bundle it named",
			user:       "workflow: base\nbranch: user/{slug}\n",
			wantBranch: "user/{slug}",
			wantFrom:   "", // filled in below: the user config path is a temp dir
			why:        "naming a key next to `workflow:` overrides just that key",
		},
		{
			name:       "the workspace beats the user config",
			user:       "workflow: base\nbranch: user/{slug}\n",
			space:      "branch: space/{slug}\n",
			wantBranch: "space/{slug}",
			wantFrom:   MarkerFile,
			why:        "the workspace owns the repos, so its file travels with them",
		},
		{
			name:       "the item beats the workspace",
			user:       "workflow: base\nbranch: user/{slug}\n",
			space:      "branch: space/{slug}\n",
			item:       "---\nbranch: item/{slug}\n---\n\n# notes\n",
			wantBranch: "item/{slug}",
			wantFrom:   "orchestration.md",
			why:        "per-item intent already lives beside repos and branches",
		},
		{
			name:       "the workspace beats the bundle without a user config at all",
			space:      "workflow: base\nbranch: space/{slug}\n",
			wantBranch: "space/{slug}",
			wantFrom:   MarkerFile,
			why:        "a bundle named by a file loses to a key in that same file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			f.userBundle("base", "name: base\nprogram: bundleprog\nbranch: bundle/{slug}\n")
			if tc.user != "" {
				f.userConfig(tc.user)
			}
			if tc.space != "" {
				f.spaceConfig(tc.space)
			}
			if tc.item != "" {
				f.itemFile(testItem.Name, tc.item)
			}
			want := tc.wantFrom
			if want == "" {
				// The user config lives in a temp XDG dir, so its label is only knowable at runtime.
				want = shortPath(UserConfigPath())
			}

			w := f.c.WorkflowFor(testItem, "")
			if w.Branch != tc.wantBranch {
				t.Errorf("branch = %q, want %q (%s)", w.Branch, tc.wantBranch, tc.why)
			}
			if w.From["branch"] != want {
				t.Errorf("branch came from %q, want %q", w.From["branch"], want)
			}
			// The keys nobody in this stack overrode still come from where they always did.
			if w.Program != "bundleprog" || w.From["program"] != "base" {
				t.Errorf("program = %q from %q, want bundleprog from base", w.Program, w.From["program"])
			}
			if w.Worktree != builtinWorkflow().Worktree || w.From["worktree"] != "built-in" {
				t.Errorf("worktree = %q from %q, want the built-in", w.Worktree, w.From["worktree"])
			}
		})
	}
}

// Hook paths resolve against whichever layer supplied them: a bundle's against the bundle, a
// config file's against the workspace. Getting that wrong would make a bundle uncopyable, since
// the scripts it ships would be looked for wherever it happened to be used.
func TestHookPathsResolveAgainstTheLayerThatSuppliedThem(t *testing.T) {
	f := newWorkflowFixture(t)
	dir := f.userBundle("solo", "name: solo\nhooks:\n  source: bin/source.sh\n")
	f.userConfig("workflow: solo\ncontext: bin/context.sh\n")

	w := f.c.WorkflowFor(Item{}, "")
	if want := filepath.Join(dir, "bin/source.sh"); w.Hooks.Source != want {
		t.Errorf("source = %q, want %q (relative to the bundle)", w.Hooks.Source, want)
	}
	if want := filepath.Join(f.c.Workspace, "bin/context.sh"); w.Hooks.Context != want {
		t.Errorf("context = %q, want %q (relative to the workspace)", w.Hooks.Context, want)
	}
	// An absolute path is left exactly as written, wherever it came from.
	f.userConfig("workflow: solo\nhooks:\n  close: /opt/hooks/close.sh\n")
	if w := f.c.WorkflowFor(Item{}, ""); w.Hooks.Close != "/opt/hooks/close.sh" {
		t.Errorf("close = %q, want the absolute path untouched", w.Hooks.Close)
	}
}

// ---------------------------------------------------------------------------------------------
// 3. The address ladder
// ---------------------------------------------------------------------------------------------

// The nearest layer that names a bundle wins outright, and the bundle it displaces contributes
// nothing. A bundle is selected, not merged with another bundle: two half-workflows folded
// together would be a third workflow nobody wrote and nobody could name.
func TestWorkflowAddressLadderSelectsRatherThanMerges(t *testing.T) {
	// Each bundle sets program, and only alpha sets branch. If branch ever arrives from alpha
	// while another bundle is in force, two bundles were merged.
	bundles := map[string]string{
		"alpha": "name: alpha\nprogram: alphaprog\nbranch: alpha/{slug}\n",
		"beta":  "name: beta\nprogram: betaprog\n",
		"gamma": "name: gamma\nprogram: gammaprog\n",
		"delta": "name: delta\nprogram: deltaprog\n",
	}

	for _, tc := range []struct {
		name     string
		user     string
		space    string
		item     string
		oneShot  string
		wantAddr string
		wantFrom string
	}{
		{name: "user config binds the host", user: "alpha", wantAddr: "alpha", wantFrom: ""},
		{name: "the workspace binds over it", user: "alpha", space: "beta", wantAddr: "beta", wantFrom: MarkerFile},
		{name: "the item binds over the workspace", user: "alpha", space: "beta", item: "gamma", wantAddr: "gamma", wantFrom: "orchestration.md"},
		{name: "a flag binds over everything written down", user: "alpha", space: "beta", item: "gamma", oneShot: "delta", wantAddr: "delta", wantFrom: "--workflow"},
		{name: "a flag with nothing written down", oneShot: "delta", wantAddr: "delta", wantFrom: "--workflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			for name, body := range bundles {
				f.userBundle(name, body)
			}
			if tc.user != "" {
				f.userConfig("workflow: " + tc.user + "\n")
			}
			if tc.space != "" {
				f.spaceConfig("workflow: " + tc.space + "\n")
			}
			if tc.item != "" {
				f.itemFile(testItem.Name, "---\nworkflow: "+tc.item+"\n---\n")
			}
			want := tc.wantFrom
			if want == "" {
				want = shortPath(UserConfigPath())
			}

			w := f.c.WorkflowFor(testItem, tc.oneShot)
			if w.Addr != tc.wantAddr || w.Name != tc.wantAddr {
				t.Errorf("selected %q (named %q), want %q", w.Addr, w.Name, tc.wantAddr)
			}
			if w.From["workflow"] != want {
				t.Errorf("the address came from %q, want %q", w.From["workflow"], want)
			}
			if w.Program != tc.wantAddr+"prog" {
				t.Errorf("program = %q, want %q", w.Program, tc.wantAddr+"prog")
			}
			// The displaced bundle's other keys must not have leaked through.
			if tc.wantAddr != "alpha" && w.Branch != builtinWorkflow().Branch {
				t.Errorf("branch = %q, want the built-in: only the displaced alpha sets one", w.Branch)
			}
		})
	}
}

// BUG (or a stale comment, depending which one is meant to be true): WorkflowFor documents the
// one-shot --workflow flag as sitting "above all of it", but the bundle it names is overlaid at
// layer 2, underneath the plain keys in the user config, the workspace file and the item. So
// `wisp open --workflow solo` in a workspace whose .wisp.yaml sets `program:` runs that program
// rather than solo's, with no note saying why.
//
// Either the flag should overlay last, or the comment should say that a flag selects a bundle and
// a written-down key still beats a bundle.
func TestOneShotWorkflowBeatsWrittenDownKeys(t *testing.T) {

	f := newWorkflowFixture(t)
	f.userBundle("solo", "name: solo\nprogram: soloprog\n")
	f.spaceConfig("program: spaceprog\n")

	w := f.c.WorkflowFor(Item{}, "solo")
	if w.Program != "soloprog" {
		t.Errorf("program = %q from %q, want soloprog: --workflow is documented as above all of it",
			w.Program, w.From["program"])
	}
}

// ---------------------------------------------------------------------------------------------
// 4. The source exception
// ---------------------------------------------------------------------------------------------

// An item may override anything except where items come from. Not a policy call: `source` decides
// which items exist, and it has already run by the time this item is a directory to read. Being
// ignored silently would leave someone editing a key that could never do anything.
func TestItemMayNotSetSource(t *testing.T) {
	f := newWorkflowFixture(t)
	f.itemFile(testItem.Name, "---\nsource: bin/mine.sh\ncontext: bin/context.sh\nprogram: aider\n---\n\n# notes\n")
	// Accepted, because this test is about which keys an item may set, not about whether it had
	// to be read first. The gate itself is TestItemManifestCannotRunAnythingUnaccepted.
	f.trustItem(testItem.Name)

	w := f.c.WorkflowFor(testItem, "")

	if w.Hooks.Source != "" {
		t.Errorf("source = %q, want empty: an item cannot decide which items exist", w.Hooks.Source)
	}
	if !hasNote(w, "source") {
		t.Errorf("the ignored key produced no note; notes were %v", w.Notes)
	}
	// The neighbouring keys are ordinary overrides and must still land, or the exception would
	// read as "an item's frontmatter is ignored".
	if want := filepath.Join(f.c.Workspace, "bin/context.sh"); w.Hooks.Context != want {
		t.Errorf("context = %q, want %q", w.Hooks.Context, want)
	}
	if w.From["context"] != "orchestration.md" {
		t.Errorf("context came from %q, want orchestration.md", w.From["context"])
	}
	if w.Program != "aider" || w.From["program"] != "orchestration.md" {
		t.Errorf("program = %q from %q, want aider from orchestration.md", w.Program, w.From["program"])
	}
}

// The exception is the item's alone. A bundle and a config file are written once for many items,
// which is exactly where deciding the source belongs.
func TestSourceIsHonouredEverywhereButTheItem(t *testing.T) {
	f := newWorkflowFixture(t)
	dir := f.userBundle("board", "name: board\nhooks:\n  source: bin/board.sh\n")
	f.userConfig("workflow: board\n")

	if w := f.c.WorkflowFor(testItem, ""); w.Hooks.Source != filepath.Join(dir, "bin/board.sh") {
		t.Errorf("a bundle's source was dropped: %q", w.Hooks.Source)
	}

	f.spaceConfig("source: bin/space.sh\n")
	f.trustSpaceConfig()
	w := f.c.WorkflowFor(testItem, "")
	if want := filepath.Join(f.c.Workspace, "bin/space.sh"); w.Hooks.Source != want {
		t.Errorf("source = %q, want %q from the workspace file", w.Hooks.Source, want)
	}
	if hasNote(w, "cannot set `source`") {
		t.Errorf("a workspace file setting source was warned about: %v", w.Notes)
	}
}

// ---------------------------------------------------------------------------------------------
// 5. Trust
// ---------------------------------------------------------------------------------------------

// A workflow directory holds the scripts wisp runs, so one that arrives by `git pull` must not run
// because it arrived. The failure mode being closed here is a repo shipping .wisp/workflows and
// silently replacing the agent, the branch scheme and every hook the moment someone clones it.
func TestWorkspaceWorkflowRunsOnlyOnceAccepted(t *testing.T) {
	f := newWorkflowFixture(t)
	f.spaceBundle("ship", "name: ship\nprogram: codex\nbranch: ship/{slug}\n")
	f.spaceConfig("workflow: ./ship\n")
	builtin := builtinWorkflow()

	// Before accepting: the bundle is named, visible, and contributes nothing.
	w := f.c.WorkflowFor(testItem, "")
	if w.Program != builtin.Program || w.From["program"] != "built-in" {
		t.Errorf("an unaccepted workspace workflow was loaded: program = %q from %q", w.Program, w.From["program"])
	}
	if w.Branch != builtin.Branch {
		t.Errorf("branch = %q, want the built-in %q", w.Branch, builtin.Branch)
	}
	if !hasNote(w, "wisp workflow accept ./ship") {
		t.Errorf("no note naming the command that would accept it: %v", w.Notes)
	}
	if ok, err := f.c.WorkflowAccepted("./ship"); err != nil || ok {
		t.Errorf("WorkflowAccepted = (%v, %v), want (false, nil)", ok, err)
	}

	// Accepted as it stands: it loads.
	f.accept("./ship")
	if ok, err := f.c.WorkflowAccepted("./ship"); err != nil || !ok {
		t.Errorf("WorkflowAccepted after accepting = (%v, %v), want (true, nil)", ok, err)
	}
	w = f.c.WorkflowFor(testItem, "")
	if w.Program != "codex" || w.From["program"] != "./ship" {
		t.Errorf("program = %q from %q, want codex from ./ship", w.Program, w.From["program"])
	}
	if hasNote(w, "accept") {
		t.Errorf("an accepted workflow still warns: %v", w.Notes)
	}

	// Changed since: accepting once must not be standing permission for whatever the file becomes.
	f.spaceBundle("ship", "name: ship\nprogram: something-else\n")
	w = f.c.WorkflowFor(testItem, "")
	if w.Program != builtin.Program {
		t.Errorf("a changed manifest kept running: program = %q", w.Program)
	}
	if !hasNote(w, "has not been accepted") {
		t.Errorf("no note about the changed manifest: %v", w.Notes)
	}

	// Accepting the new contents brings it back, which is the whole loop.
	f.accept("./ship")
	if w := f.c.WorkflowFor(testItem, ""); w.Program != "something-else" {
		t.Errorf("re-accepting did not take: program = %q", w.Program)
	}
}

// The accept record is keyed by workspace as well as address, because ./ship in two checkouts is
// two directories holding two different sets of scripts.
func TestAcceptanceIsPerWorkspace(t *testing.T) {
	f := newWorkflowFixture(t)
	f.spaceBundle("ship", "name: ship\nprogram: codex\n")
	f.spaceConfig("workflow: ./ship\n")
	f.accept("./ship")

	other := f.c
	other.Workspace = t.TempDir()
	if ok, _ := other.WorkflowAccepted("./ship"); ok {
		t.Error("accepting ./ship in one workspace accepted it in another")
	}
}

// One of yours is not gated: it is in your own config directory, which is the same trust boundary
// as the config file that names it.
func TestUserWorkflowsNeedNoAcceptance(t *testing.T) {
	f := newWorkflowFixture(t)
	f.userBundle("solo", "name: solo\nprogram: aider\n")
	f.userConfig("workflow: solo\n")

	w := f.c.WorkflowFor(testItem, "")
	if w.Program != "aider" {
		t.Errorf("program = %q, want aider: your own workflows are not gated", w.Program)
	}
	if len(w.Notes) != 0 {
		t.Errorf("notes = %v, want none", w.Notes)
	}
}

// Only a gated bundle is read whole. The hash is what the accept check compares, and a workspace
// bundle is the only thing gated on it, but hashing ran ahead of that check for every bundle: every
// resolution of one of your own read every file beside its manifest. That path is the one
// NeedsInputMarker takes for each of Local, BoardItems, Peers, LocalPeers and Sessions, none of
// which hold the picker's memo, so a bundle with a venv or a vendored checkout next to it made
// `wisp board` walk the lot on every refresh. Delete this and nothing points at the walk.
func TestOnlyGatedBundlesAreReadWhole(t *testing.T) {
	f := newWorkflowFixture(t)
	dir := f.userBundle("solo", "name: solo\nprogram: aider\n")
	f.userConfig("workflow: solo\n")
	// Unreadable is the cheapest stand-in for expensive: a hash of the tree cannot be computed
	// without reading this, and a resolution that has no reason to read it does not notice.
	blocked := f.write(filepath.Join(dir, "vendored"), "not for you")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(blocked); err == nil {
		t.Skip("running as a user who can read anything, so this would prove nothing")
	}

	if w := f.c.WorkflowFor(testItem, ""); w.Program != "aider" || len(w.Notes) != 0 {
		t.Errorf("program = %q, notes = %v: your own bundle was walked rather than read", w.Program, w.Notes)
	}

	// The other half of the same property: a workspace bundle is gated, so it is still hashed
	// whole, and the file the one above never touched is exactly what it trips over.
	sdir := f.spaceBundle("team", "name: team\nprogram: codex\n")
	if err := os.Chmod(f.write(filepath.Join(sdir, "vendored"), "not for you"), 0o000); err != nil {
		t.Fatal(err)
	}
	f.spaceConfig("workflow: ./team\n")
	if w := f.c.WorkflowFor(testItem, ""); !hasNote(w, "vendored") {
		t.Errorf("notes = %v, want the accept check to have read the whole bundle", w.Notes)
	}
}

// ---------------------------------------------------------------------------------------------
// 6. Degradation
// ---------------------------------------------------------------------------------------------

// Nothing about a workflow may cost you a session. The built-in is complete precisely so that
// every way a bundle can be wrong costs one key and produces one sentence, and WorkflowFor has no
// error return at all: there is nothing here it could report that is worth refusing to open over.
func TestABrokenWorkflowCostsAKeyNotASession(t *testing.T) {
	builtin := builtinWorkflow()

	for _, tc := range []struct {
		name     string
		bundle   string // "" means do not create the directory at all
		addr     string
		wantNote string
		check    func(t *testing.T, w Workflow)
		why      string
	}{
		{
			name:     "malformed yaml",
			bundle:   "name: broken\nprogram: [unclosed\n",
			addr:     "broken",
			wantNote: "using the built-in",
			why:      "a half-edited manifest is the most likely way a bundle is wrong",
		},
		{
			name:     "no such directory",
			addr:     "nowhere",
			wantNote: "no workflow \"nowhere\"",
			why:      "a renamed or never-created directory named by a config file",
		},
		{
			name:     "an address that is not a name",
			addr:     "../evil",
			wantNote: "using the built-in",
			why:      "a refused address is a note here, not a failure to open",
		},
		{
			name:     "a layout entry with no window name",
			bundle:   "name: b\nlayout:\n  - {window: \"\", cwd: workspace}\n  - {window: agent, cwd: workspace, run: \"{program}\"}\n",
			addr:     "b",
			wantNote: "no window name",
			check: func(t *testing.T, w Workflow) {
				if len(w.Layout) != 1 || w.Layout[0].Window != "agent" {
					t.Errorf("layout = %+v, want just the usable window", w.Layout)
				}
			},
			why: "one bad entry loses one window, not the layout",
		},
		{
			name:     "unknown for:",
			bundle:   "name: b\nlayout:\n  - {window: each, for: every-repo}\n  - {window: agent, cwd: workspace}\n",
			addr:     "b",
			wantNote: "unknown `for: every-repo`",
			why:      "For and When are the only control flow there is, so a typo must be visible rather than merely ineffective",
		},
		{
			name:     "unknown when:",
			bundle:   "name: b\nlayout:\n  - {window: maybe, when: sometimes}\n  - {window: agent, cwd: workspace}\n",
			addr:     "b",
			wantNote: "unknown `when: sometimes`",
			why:      "same",
		},
		{
			name:     "unknown cwd:",
			bundle:   "name: b\nlayout:\n  - {window: odd, cwd: elsewhere}\n  - {window: agent, cwd: workspace}\n",
			addr:     "b",
			wantNote: "unknown `cwd: elsewhere`",
			why:      "the three places a window can start are the three wisp knows how to find",
		},
		{
			name:     "a window named twice",
			bundle:   "name: b\nlayout:\n  - {window: agent, cwd: workspace}\n  - {window: agent, cwd: home}\n",
			addr:     "b",
			wantNote: "named twice",
			check: func(t *testing.T, w Workflow) {
				if len(w.Layout) != 1 {
					t.Errorf("layout = %+v, want one window", w.Layout)
				}
			},
			why: "tmux would take the second one and the first would look like it never happened",
		},
		{
			name:     "every entry invalid",
			bundle:   "name: b\nlayout:\n  - {window: \"\"}\n  - {window: x, for: nope}\n",
			addr:     "b",
			wantNote: "falling back to the built-in layout",
			check: func(t *testing.T, w Workflow) {
				if len(w.Layout) != len(builtin.Layout) || w.From["layout"] != "built-in" {
					t.Errorf("layout = %d windows from %q, want the built-in %d",
						len(w.Layout), w.From["layout"], len(builtin.Layout))
				}
			},
			why: "an empty layout opens an empty session, which is worse than opening the one wisp knows how to build",
		},
		{
			name:     "worktree template with a path in it",
			bundle:   "name: b\nworktree: nested/{slug}\n",
			addr:     "b",
			wantNote: "contains a /",
			why:      "`worktree:` names a directory inside worktrees:, and a path there would write outside it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			if tc.bundle != "" {
				f.userBundle(tc.addr, tc.bundle)
			}
			f.userConfig("workflow: " + tc.addr + "\n")

			w := f.c.WorkflowFor(testItem, "")

			if !hasNote(w, tc.wantNote) {
				t.Errorf("no note containing %q (%s); notes were %v", tc.wantNote, tc.why, w.Notes)
			}
			// Usable regardless: something to run, somewhere to run it, a branch to cut.
			if w.Program == "" || len(w.Layout) == 0 || w.BranchFor(testItem, "wisp") == "" {
				t.Errorf("left unusable: program %q, %d windows, branch %q",
					w.Program, len(w.Layout), w.BranchFor(testItem, "wisp"))
			}
			if tc.check != nil {
				tc.check(t, w)
			}
		})
	}
}

// A bundle directory with no manifest in it is the same class of problem as a missing directory,
// and so is one wisp cannot read.
func TestABundleWithNoManifest(t *testing.T) {
	f := newWorkflowFixture(t)
	if err := os.MkdirAll(filepath.Join(UserWorkflowsDir(), "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.userConfig("workflow: empty\n")

	w := f.c.WorkflowFor(testItem, "")
	if w.Program != builtinWorkflow().Program {
		t.Errorf("program = %q, want the built-in", w.Program)
	}
	if len(w.Notes) == 0 {
		t.Error("a bundle with no workflow.yaml passed without a word")
	}
}

// The one failure nobody needs told about. Naming `default` when you have not made one of your own
// is not a mistake, it is the ordinary case spelled out.
func TestNamingDefaultWithNoDirectoryIsSilent(t *testing.T) {
	f := newWorkflowFixture(t)
	f.userConfig("workflow: default\n")

	w := f.c.WorkflowFor(testItem, "")
	if len(w.Notes) != 0 {
		t.Errorf("notes = %v, want none: naming the built-in is not a problem", w.Notes)
	}
	if w.Program != builtinWorkflow().Program {
		t.Errorf("program = %q, want the built-in", w.Program)
	}

	// A directory of yours called default does shadow it, and that is deliberate: it is your
	// directory and you meant it.
	f.userBundle("default", "name: default\nprogram: mine\n")
	if w := f.c.WorkflowFor(testItem, ""); w.Program != "mine" {
		t.Errorf("program = %q, want mine: your own default shadows the built-in", w.Program)
	}
}

// A malformed config file is not a workflow problem. It has to leave a usable workflow too, since
// the file that would have named one is the file that will not parse.
func TestBrokenConfigFilesStillResolveAWorkflow(t *testing.T) {
	f := newWorkflowFixture(t)
	f.userConfig("workflow: [unclosed\n")
	f.spaceConfig("branch: [also unclosed\n")
	f.itemFile(testItem.Name, "---\nprogram: [nope\n---\n")

	w := f.c.WorkflowFor(testItem, "")
	if w.Program != builtinWorkflow().Program || w.Branch != builtinWorkflow().Branch {
		t.Errorf("program %q, branch %q, want the built-in throughout", w.Program, w.Branch)
	}
	if len(w.Layout) == 0 {
		t.Error("no layout at all")
	}
}

// ---------------------------------------------------------------------------------------------
// 7. Templates
// ---------------------------------------------------------------------------------------------

// The vocabulary is tiny and closed on purpose. An unknown token is left standing rather than
// replaced with nothing, because a branch called "feature/" tells you far less than one called
// "feature/{tikcet}".
func TestExpand(t *testing.T) {
	vars := map[string]string{"slug": "42-fix", "repo": "wisp", "item": "wisp/42-fix"}
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"feature/{slug}", "feature/42-fix"},
		{"{repo}--{slug}", "wisp--42-fix"},
		{"{item}", "wisp/42-fix"},
		{"{repo}/{slug}/{repo}", "wisp/42-fix/wisp"},
		{"no tokens here", "no tokens here"},
		{"wip/{tikcet}", "wip/{tikcet}"},
		{"{slug}-{unknown}", "42-fix-{unknown}"},
		{"{}", "{}"},
	} {
		if got := Expand(tc.in, vars); got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A template with tokens and no variables at all still comes back intact.
	if got := Expand("feature/{slug}", nil); got != "feature/{slug}" {
		t.Errorf("Expand with no vars = %q, want the template untouched", got)
	}
}

// A value that is empty takes the token and the gap beside it with it. The built-in's agent window
// is `{program} {prompt}`, and a run line is shell-quoted before substitution, so a prompt with
// nothing in it used to expand to claude with an empty quoted argument, which is not the same
// thing as no argument at all for most agent CLIs. Delete this and the token comes back as a hole
// in the middle of a command line.
func TestExpandCollapsesEmptyValues(t *testing.T) {
	vars := map[string]string{"program": "claude", "prompt": "", "flags": ""}
	for _, tc := range []struct{ in, want, why string }{
		{"{program} {prompt}", "claude", "the separator goes with the value it separated"},
		{"{program} {flags} run", "claude run", "one side only: eating both would join the words either side"},
		{"{prompt}", "", "nothing left is nothing at all"},
		{"{prompt} {program}", "claude", "a token at the front takes the space after it instead"},
		{"x={prompt}", "x=", "no whitespace to take, and what is around it is left alone"},
		{"{program}  {prompt}", "claude", "however much whitespace there was"},
	} {
		if got := Expand(tc.in, vars); got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
		}
	}
}

func TestBranchAndWorktreeTemplates(t *testing.T) {
	for _, tc := range []struct {
		branch, worktree string
		item             Item
		repo             string
		wantBranch       string
		wantWorktree     string
	}{
		{
			branch: "feature/{slug}", worktree: "{repo}--{slug}",
			item: testItem, repo: "wisp",
			wantBranch: "feature/42-fix-the-thing", wantWorktree: "wisp--42-fix-the-thing",
		},
		{
			branch: "{repo}/{slug}", worktree: "{item}",
			item: testItem, repo: "other",
			wantBranch: "other/42-fix-the-thing", wantWorktree: "wisp/42-fix-the-thing",
		},
		{
			// An _adhoc item has no repo half. {repo} is whichever repo is being cut, which is
			// still known here even when the item's own name has none.
			branch: "wip/{slug}", worktree: "{repo}--{slug}",
			item: Item{Name: "_adhoc/spike"}, repo: "wisp",
			wantBranch: "wip/spike", wantWorktree: "wisp--spike",
		},
	} {
		w := Workflow{Branch: tc.branch, Worktree: tc.worktree}
		if got := w.BranchFor(tc.item, tc.repo); got != tc.wantBranch {
			t.Errorf("BranchFor(%q, %q) with %q = %q, want %q", tc.item.Name, tc.repo, tc.branch, got, tc.wantBranch)
		}
		if got := w.WorktreeName(tc.item, tc.repo); got != tc.wantWorktree {
			t.Errorf("WorktreeName(%q, %q) with %q = %q, want %q", tc.item.Name, tc.repo, tc.worktree, got, tc.wantWorktree)
		}
	}
}

// The worktree name comes from a template someone else may have written, and it is joined straight
// onto `worktrees:`. Anything that would put the checkout somewhere that directory does not reach
// falls back to the name wisp has always used, rather than writing outside it.
func TestWorktreeForRefusesToLeaveTheWorktreeRoot(t *testing.T) {
	c := Config{Workspace: "/ws", Worktrees: ".worktrees"}
	item := Item{Name: "wisp/42-fix-the-thing"}
	fallback := filepath.Join("/ws", ".worktrees", "wisp--42-fix-the-thing")

	for _, tc := range []struct {
		tmpl string
		want string
		why  string
	}{
		{"{repo}--{slug}", fallback, "the ordinary case, which is also the fallback"},
		{"", fallback, "an unset template expands to nothing and would name the root itself"},
		{"{nothing}", filepath.Join("/ws", ".worktrees", "{nothing}"), "an unknown token is left standing, so the name is still a name"},
		{"nested/{slug}", fallback, "a separator would put the checkout below the root, or outside it"},
		{"../{slug}", fallback, "a separator, and this one leaves outright"},
		{".", fallback, "the root itself"},
		{"..", fallback, "the workspace, not a worktree in it"},
		{"{slug}", filepath.Join("/ws", ".worktrees", "42-fix-the-thing"), "a single segment is fine however it was spelled"},
	} {
		got := c.WorktreeFor(Workflow{Worktree: tc.tmpl}, "wisp", item)
		if got != tc.want {
			t.Errorf("WorktreeFor(%q) = %q, want %q (%s)", tc.tmpl, got, tc.want, tc.why)
		}
		if filepath.Dir(filepath.Clean(got)) != filepath.Clean(c.WorktreeRoot()) {
			t.Errorf("WorktreeFor(%q) = %q, outside %q", tc.tmpl, got, c.WorktreeRoot())
		}
	}
}

// ---------------------------------------------------------------------------------------------
// 8. Regression: the built-in is today's behaviour
// ---------------------------------------------------------------------------------------------

// The acceptance test for the whole change. Workflows only earn their keep if nobody who never
// asked for one notices they exist: with no user config, no .wisp.yaml, no bundle and no
// frontmatter anywhere, the resolved workflow must be exactly what wisp did before it had any of
// this. Every value below was hardcoded across session.go, manifest.go, workspace.go and tmux.go.
func TestBuiltinWorkflowReproducesTodaysBehaviour(t *testing.T) {
	f := newWorkflowFixture(t)
	item := Item{Name: "wisp/42-fix-the-thing"}

	w := f.c.WorkflowFor(item, "")

	if len(w.Notes) != 0 {
		t.Errorf("a workspace with no configuration at all produced notes: %v", w.Notes)
	}
	if w.Addr != "" || w.Dir != "" {
		t.Errorf("addr %q dir %q, want both empty: no bundle was named", w.Addr, w.Dir)
	}
	if w.Program != "claude" {
		t.Errorf("program = %q, want claude", w.Program)
	}
	if got := w.BranchFor(item, "wisp"); got != "feature/42-fix-the-thing" {
		t.Errorf("branch = %q, want feature/42-fix-the-thing", got)
	}
	if got := w.WorktreeName(item, "wisp"); got != "wisp--42-fix-the-thing" {
		t.Errorf("worktree = %q, want wisp--42-fix-the-thing", got)
	}
	if got, want := f.c.WorktreeFor(w, "wisp", item),
		filepath.Join(f.c.Workspace, ".worktrees", "wisp--42-fix-the-thing"); got != want {
		t.Errorf("worktree path = %q, want %q", got, want)
	}
	if got, want := w.Hooks.Provision,
		filepath.Join(f.c.Workspace, ".claude", "scripts", "provision-worktree.sh"); got != want {
		t.Errorf("provision hook = %q, want %q", got, want)
	}
	if w.Status.NeedsInput != needsInputMarker {
		t.Errorf("needs-input marker = %q, want the Claude Code dialog string", w.Status.NeedsInput)
	}
	if got := f.c.NeedsInputMarker(); got != needsInputMarker {
		t.Errorf("NeedsInputMarker = %q, want the same marker", got)
	}

	// The layout the sessions have always had: one agent at the workspace root, a shell per
	// worktree, and a provisioning window only while a worktree is still missing.
	if len(w.Layout) != 3 {
		t.Fatalf("layout = %+v, want three windows", w.Layout)
	}
	for i, want := range []Window{
		{Window: "agent", Cwd: "workspace", Run: "{program} {prompt}", Focus: true},
		{Window: "{repo}", For: "each-worktree", Cwd: "worktree"},
		{Window: "provision", When: "provisioning", Cwd: "workspace", Run: "{wisp} provision {item}"},
	} {
		if w.Layout[i] != want {
			t.Errorf("layout[%d] = %+v, want %+v", i, w.Layout[i], want)
		}
	}

	// And every one of those answers is attributed to the built-in, so `wisp workflow` on an
	// unconfigured machine says so rather than leaving the column blank.
	for _, key := range []string{"program", "branch", "worktree", "provision", "needs_input", "layout"} {
		if w.From[key] != "built-in" {
			t.Errorf("%s came from %q, want built-in", key, w.From[key])
		}
	}
	if w.From["workflow"] != "" {
		t.Errorf("an address was recorded (%q) when none was named", w.From["workflow"])
	}

	// BuiltinWorkflow is what `wisp workflow show default` prints and what `init` copies, so it
	// must be the same thing, minus the workspace-relative hook path resolution.
	if b := BuiltinWorkflow(); b.Program != w.Program || b.Branch != w.Branch || b.Worktree != w.Worktree {
		t.Errorf("BuiltinWorkflow disagrees with the resolved built-in: %+v", b)
	}
}

// The listing is not a search path: two rows may share a name and they are two different
// workflows, one of which needs accepting before it runs.
func TestListWorkflows(t *testing.T) {
	f := newWorkflowFixture(t)
	f.userBundle("solo", "name: solo\n")
	f.spaceBundle("solo", "name: solo\n")
	f.spaceBundle("ship", "name: ship\n")

	byAddr := map[string]WorkflowEntry{}
	for _, e := range f.c.ListWorkflows("./ship") {
		byAddr[e.Addr] = e
	}
	for _, addr := range []string{"solo", "./solo", "./ship", "default"} {
		if _, ok := byAddr[addr]; !ok {
			t.Errorf("%q missing from the listing: %v", addr, byAddr)
		}
	}
	if byAddr["./solo"].Note != "not yet accepted" {
		t.Errorf("./solo note = %q, want the accept prompt", byAddr["./solo"].Note)
	}
	if byAddr["solo"].Note != "" || !byAddr["default"].Accepted {
		t.Errorf("yours and the built-in should carry no warning: %+v %+v", byAddr["solo"], byAddr["default"])
	}
	// Bound but refused is not in force, and the mark says what is running rather than what is
	// written down. A star beside a row whose own note reads "not yet accepted" would be the
	// listing claiming a workflow is doing something it is being prevented from doing.
	if byAddr["./ship"].InUse {
		t.Error("an unaccepted workflow is marked as in force")
	}
	if !byAddr["default"].InUse {
		t.Error("the built-in is what actually runs here and is not marked")
	}

	f.accept("./ship")
	byAddr = map[string]WorkflowEntry{}
	for _, e := range f.c.ListWorkflows("./ship") {
		byAddr[e.Addr] = e
	}
	if e := byAddr["./ship"]; !e.Accepted || e.Note != "" || !e.InUse {
		t.Errorf("./ship after accepting: %+v", e)
	}
	if byAddr["default"].InUse {
		t.Error("the built-in is still marked once a real workflow took over")
	}
}

// The picker resolves a workflow for the highlighted item on every cursor move, which is a
// handful of file reads per keystroke. CacheWorkflows says those reads may be reused; the proof
// is that a config holding one keeps answering from before an edit while a plain one does not.
func TestCachedResolutionReusesItsAnswer(t *testing.T) {
	f := newWorkflowFixture(t)
	f.spaceConfig("program: aider\n")
	f.c.Accepted[f.c.acceptKey(MarkerFile)] = sumOf([]byte("program: aider\n"))

	cached := f.c.CacheWorkflows()
	if got := cached.WorkflowFor(Item{}, "").Program; got != "aider" {
		t.Fatalf("program = %q, want aider", got)
	}

	f.spaceConfig("program: codex\n")
	f.c.Accepted[f.c.acceptKey(MarkerFile)] = sumOf([]byte("program: codex\n"))
	if got := cached.WorkflowFor(Item{}, "").Program; got != "aider" {
		t.Errorf("cached program = %q, want the remembered aider", got)
	}
	if got := f.c.WorkflowFor(Item{}, "").Program; got != "codex" {
		t.Errorf("uncached program = %q, want the edited codex", got)
	}

	// ctrl-r is the key someone presses after editing a workflow in another terminal, so it has
	// to mean the memo as well as the network.
	cached.ForgetWorkflows()
	if got := cached.WorkflowFor(Item{}, "").Program; got != "codex" {
		t.Errorf("after forgetting, program = %q, want codex", got)
	}
}

// An item may override the workflow in its own frontmatter, so the memo is per item. One key for
// the workspace would hand every item the first item's answer.
func TestCachedResolutionIsPerItem(t *testing.T) {
	f := newWorkflowFixture(t)
	f.itemFile("_adhoc/one", "---\nbranch: one/{slug}\n---\n")
	f.itemFile("_adhoc/two", "---\nbranch: two/{slug}\n---\n")

	cached := f.c.CacheWorkflows()
	if got := cached.WorkflowFor(Item{Name: "_adhoc/one"}, "").Branch; got != "one/{slug}" {
		t.Fatalf("first item branch = %q", got)
	}
	if got := cached.WorkflowFor(Item{Name: "_adhoc/two"}, "").Branch; got != "two/{slug}" {
		t.Errorf("second item branch = %q, want its own", got)
	}
}

// The cache hands out copies. Notes is appended to by everything that resolves a workflow, so a
// caller writing through to the cache's own slice would leave the next caller reading notes about
// somebody else's resolution.
func TestCachedResolutionHandsOutCopies(t *testing.T) {
	f := newWorkflowFixture(t)
	f.spaceConfig("worktree: has/a/slash\n")

	cached := f.c.CacheWorkflows()
	first := cached.WorkflowFor(Item{}, "")
	if len(first.Notes) == 0 {
		t.Fatal("a worktree name with slashes in it should be noted")
	}
	first.Notes = append(first.Notes, "written by the caller")
	first.From["program"] = "written by the caller"
	first.Layout[0].Window = "written by the caller"

	second := cached.WorkflowFor(Item{}, "")
	if len(second.Notes) != len(first.Notes)-1 {
		t.Errorf("notes leaked between callers: %v", second.Notes)
	}
	if second.From["program"] == "written by the caller" {
		t.Error("From leaked between callers")
	}
	if second.Layout[0].Window == "written by the caller" {
		t.Error("Layout leaked between callers")
	}
}
