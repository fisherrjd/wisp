package wisp

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nothing in the binary executes. A shipped bundle may shape a folder and a session; a script
// it carried would run out of the binary with no file anyone accepted, so there are none.
func TestShippedBundlesNameNoScripts(t *testing.T) {
	for _, name := range shippedNames {
		w, ok := shippedBundle(name)
		if !ok {
			t.Errorf("%s: not a shipped bundle", name)
			continue
		}
		if len(w.Notes) != 0 {
			t.Errorf("%s: parsing produced notes: %v", name, w.Notes)
		}
		for _, h := range w.Hooks.slots() {
			if *h.dst != "" {
				t.Errorf("%s names a %s hook (%s); shipped bundles carry no scripts", name, h.key, *h.dst)
			}
		}
		if strings.ContainsAny(w.Program, "/") {
			t.Errorf("%s: program %q is a path, not a command on PATH", name, w.Program)
		}
		if w.Item.Seed != "" && w.SeedFS == nil {
			t.Errorf("%s: seed %q is not in the binary", name, w.Item.Seed)
		}
		// Not a single executable anywhere in the tree, and nothing under bin/.
		_ = fs.WalkDir(shippedFS, "bundles/"+name, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.Contains(p, "/bin/") {
				t.Errorf("%s ships %s", name, p)
			}
			if info, err := d.Info(); err == nil && info.Mode()&0o111 != 0 {
				t.Errorf("%s: %s is executable", name, p)
			}
			return nil
		})
	}
}

// Seeds are data: top-level regular files, no dotfiles, written once and never over anything.
func TestShippedSeedsAreData(t *testing.T) {
	for _, name := range shippedNames {
		w, _ := shippedBundle(name)
		if w.SeedFS == nil {
			continue
		}
		entries, err := fs.ReadDir(w.SeedFS, ".")
		if err != nil {
			t.Errorf("%s: seed dir unreadable: %v", name, err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || safeSegment(e.Name()) == "" {
				t.Errorf("%s: seed entry %q would not be written", name, e.Name())
			}
		}
		f := newWorkflowFixture(t)
		f.userConfig("workflow: " + name + "\n")
		it := Item{Name: "repo/3-thing", Title: "Thing"}
		if err := f.c.makeItemDir(f.c.WorkflowFor(it, ""), it); err != nil {
			t.Fatal(err)
		}
		notes := filepath.Join(f.c.ItemDir(it.Name), "notes.md")
		raw, _ := os.ReadFile(notes)
		if !bytes.Contains(raw, []byte("# 3-thing")) || bytes.Contains(raw, []byte("{slug}")) {
			t.Errorf("%s: seeded notes.md not expanded: %q", name, raw)
		}
		if err := os.WriteFile(notes, []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := f.c.makeItemDir(f.c.WorkflowFor(it, ""), it); err != nil {
			t.Fatal(err)
		}
		if raw, _ := os.ReadFile(notes); string(raw) != "edited\n" {
			t.Errorf("%s: a seed overwrote an edited file", name)
		}
	}
}

// The embedded default is the floor plus a seed and a description, and nothing else: every
// value the floor sets, the manifest sets the same, so the provenance column never lies.
func TestTheEmbeddedDefaultIsTheBuiltinPlusASeed(t *testing.T) {
	raw := ShippedManifest("default")
	if raw == nil {
		t.Fatal("no embedded default bundle")
	}
	w, err := parseWorkflow("", raw)
	if err != nil {
		t.Fatalf("the embedded default does not parse: %v", err)
	}
	b := builtinWorkflow()
	if w.Name != "default" || w.Program != b.Program || w.Branch != b.Branch || w.Worktree != b.Worktree {
		t.Errorf("identity differs: %q %q %q %q vs %q %q %q", w.Name, w.Program, w.Branch, w.Worktree, b.Program, b.Branch, b.Worktree)
	}
	if w.Status.NeedsInput != b.Status.NeedsInput || w.Picker.RemoteLabel != b.Picker.RemoteLabel {
		t.Errorf("needs_input / remote_label differ: %q %q", w.Status.NeedsInput, w.Picker.RemoteLabel)
	}
	if len(w.Layout) != len(b.Layout) {
		t.Fatalf("layout: embedded has %d windows, built-in %d", len(w.Layout), len(b.Layout))
	}
	for i := range b.Layout {
		if w.Layout[i] != b.Layout[i] {
			t.Errorf("layout window %d: embedded %+v, built-in %+v", i, w.Layout[i], b.Layout[i])
		}
	}
	if w.Hooks.Provision != "" {
		t.Errorf("the embedded default names provision %q; a bundle-relative path would not mean the built-in's", w.Hooks.Provision)
	}
	if w.Item.Seed == "" || w.Description == b.Description {
		t.Errorf("the default should add a seed and its own description: seed=%q desc=%q", w.Item.Seed, w.Description)
	}
}

// default is workspace under wisp's own name: the same file but for the name and the header.
func TestDefaultIsTheWorkspaceBundle(t *testing.T) {
	strip := func(raw []byte) string {
		var kept []string
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "name:") || strings.HasPrefix(line, "#") {
				continue
			}
			kept = append(kept, line)
		}
		return strings.Join(kept, "\n")
	}
	if strip(ShippedManifest("default")) != strip(ShippedManifest("workspace")) {
		t.Error("default and workspace manifests differ beyond name and comments")
	}
	for _, seed := range []string{"notes.md", "orchestration.md"} {
		a, _ := shippedFS.ReadFile("bundles/default/seed/" + seed)
		b, _ := shippedFS.ReadFile("bundles/workspace/seed/" + seed)
		if a == nil || !bytes.Equal(a, b) {
			t.Errorf("seed %s differs between default and workspace", seed)
		}
	}
}

// An unbound workspace is the built-in, and the built-in now seeds: notes.md and a commented
// orchestration.md that still leaves the manifest to be inferred.
func TestAnUnboundWorkspaceSeedsFromTheEmbeddedDefault(t *testing.T) {
	f := newWorkflowFixture(t)
	if err := os.MkdirAll(filepath.Join(f.c.Workspace, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := f.c.WorkflowFor(Item{}, "")
	if w.From["seed"] != "built-in" || w.SeedFS == nil || len(w.Notes) != 0 || w.From["workflow"] != "" {
		t.Fatalf("seed from %q, SeedFS nil=%v, notes %v, workflow %q", w.From["seed"], w.SeedFS == nil, w.Notes, w.From["workflow"])
	}
	it, _, err := f.c.NewItemNoted("repo/9-thing")
	if err != nil {
		t.Fatal(err)
	}
	dir := f.c.ItemDir(it.Name)
	for _, name := range []string{"notes.md", "orchestration.md"} {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("%s was not seeded", name)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "orchestration.md"))
	if !bytes.Contains(raw, []byte("repo: repo")) || bytes.Contains(raw, []byte("{item}")) {
		t.Errorf("orchestration.md not expanded: %q", raw)
	}
	entries, err := f.c.Manifest(f.c.WorkflowFor(it, ""), it)
	if err != nil || len(entries) != 1 || entries[0].Repo != "repo" || entries[0].Branch != "feature/9-thing" {
		t.Errorf("a commented manifest should still infer one repo: %+v %v", entries, err)
	}
	if notes := f.c.WorkflowFor(it, "").Notes; len(notes) != 0 {
		t.Errorf("the seeded orchestration.md produced notes: %v", notes)
	}
	// Bound to default by name: the same thing.
	f.spaceConfig("workflow: default\n")
	f.c.ForgetWorkflows()
	if w := f.c.WorkflowFor(Item{}, ""); w.From["seed"] != "built-in" || w.From["workflow"] != MarkerFile {
		t.Errorf("workflow: default should be the same workflow, bound: seed from %q, workflow from %q", w.From["seed"], w.From["workflow"])
	}
}

func TestMinimalIsBareAndScratchFilesUnderAdhoc(t *testing.T) {
	f := newWorkflowFixture(t)
	if err := os.MkdirAll(filepath.Join(f.c.Workspace, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.userConfig("workflow: minimal\n")
	w := f.c.WorkflowFor(Item{}, "")
	if w.Item.Seed != "" || w.SeedFS != nil || w.Dir != "" || w.From["name"] != "minimal" {
		t.Errorf("minimal: seed=%q dir=%q name from %q", w.Item.Seed, w.Dir, w.From["name"])
	}
	it, _, err := f.c.NewItemNoted("repo/1-bare")
	if err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(f.c.ItemDir(it.Name)); len(ents) != 1 || ents[0].Name() != "notes.md" {
		t.Errorf("minimal should make only the notes stub, got %v", ents)
	}

	f.userConfig("workflow: scratch\n")
	f.c.ForgetWorkflows()
	w = f.c.WorkflowFor(Item{}, "")
	if w.Item.Parent != "_adhoc" || len(w.Layout) != 1 || w.From["layout"] != "scratch" {
		t.Errorf("scratch: parent=%q layout=%d from %q", w.Item.Parent, len(w.Layout), w.From["layout"])
	}
	loose, note, err := f.c.NewItemNoted("loose")
	if err != nil || loose.Name != "_adhoc/loose" || note != "" {
		t.Errorf("scratch naming: %q note %q err %v", loose.Name, note, err)
	}
	if ents, _ := f.c.Manifest(w, loose); len(ents) != 0 {
		t.Errorf("a scratch item has no repos, got %+v", ents)
	}
	raw, _ := os.ReadFile(filepath.Join(f.c.ItemDir(loose.Name), "notes.md"))
	if !bytes.Contains(raw, []byte("# loose")) || !bytes.Contains(raw, []byte("Started ")) {
		t.Errorf("scratch notes not seeded: %q", raw)
	}
}

func TestListWorkflowsShowsEveryShippedBundle(t *testing.T) {
	f := newWorkflowFixture(t)
	rows := map[string]WorkflowEntry{}
	for _, e := range f.c.ListWorkflows("") {
		rows[e.Addr] = e
	}
	for _, name := range shippedNames {
		e, ok := rows[name]
		if !ok || !e.Accepted || e.Note != "" {
			t.Errorf("%s: %+v (present %v)", name, e, ok)
		}
	}
	if !rows["default"].InUse {
		t.Error("nothing is bound, so the built-in runs and should be marked")
	}
	// Yours by the same name shadows it, and the listing says so.
	f.userBundle("scratch", "name: scratch\n")
	rows = map[string]WorkflowEntry{}
	for _, e := range f.c.ListWorkflows("") {
		rows[e.Addr] = e
	}
	if !strings.Contains(rows["scratch"].Where, "shadows") {
		t.Errorf("your scratch should be marked as shadowing: %+v", rows["scratch"])
	}
}

func TestInitFromEachShippedBundle(t *testing.T) {
	for _, from := range shippedNames {
		c := newWorkflowConfig(t)
		name := "mine-" + from
		if err := c.workflowInit(name, from, false); err != nil {
			t.Fatalf("init --from %s: %v", from, err)
		}
		dir := filepath.Join(UserWorkflowsDir(), name)
		raw, err := os.ReadFile(filepath.Join(dir, WorkflowFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "name: "+name+"\n") {
			t.Errorf("%s: name was not rewritten", from)
		}
		if !strings.Contains(string(raw), "# ") {
			t.Errorf("%s: comments were lost in the copy", from)
		}
		w, err := LoadWorkflowFile(dir)
		if err != nil || len(w.Notes) != 0 {
			t.Errorf("%s: the copy does not load cleanly: %v %v", from, err, w.Notes)
		}
		if b, _ := shippedBundle(from); b.Item.Seed != "" && !isDir(filepath.Join(dir, b.Item.Seed)) {
			t.Errorf("%s: seed directory was not copied", from)
		}
	}
	c := newWorkflowConfig(t)
	if err := c.workflowInit("other", "nope", false); err == nil || !strings.Contains(err.Error(), "does not ship") {
		t.Errorf("an unknown --from should be refused by name, got %v", err)
	}
}
