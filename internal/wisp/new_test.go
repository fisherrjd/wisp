package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFixture is a workspace with a user bundle bound, the repos named made, and the process
// standing at the workspace root. The bundle is yours rather than the workspace's so nothing
// here is about the accept gate.
func newFixture(t *testing.T, manifest string, repos ...string) *wfFixture {
	t.Helper()
	f := newWorkflowFixture(t)
	f.userBundle("mk", manifest)
	f.userConfig("workflow: mk\n")
	for _, r := range repos {
		if err := os.MkdirAll(filepath.Join(f.c.Workspace, r, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(f.c.Workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return f
}

// bundleScript writes an executable into the bundle, where a relative hook path points.
func (f *wfFixture) bundleScript(rel, body string) string {
	f.t.Helper()
	path := f.write(filepath.Join(UserWorkflowsDir(), "mk", rel), "#!/bin/sh\n"+body)
	if err := os.Chmod(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func TestNewHookNamesTheItem(t *testing.T) {
	f := newFixture(t, "name: mk\nhooks:\n  new: bin/new.sh\n", "alpha")
	seen := filepath.Join(t.TempDir(), "stdin")
	f.bundleScript("bin/new.sh", "cat > "+seen+"\necho '{\"name\":\"alpha/9-from-hook\",\"title\":\"Nine\"}'\n")

	it, note, err := f.c.NewItemNoted("thing")
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "alpha/9-from-hook" || it.Title != "Nine" {
		t.Errorf("got %q titled %q, want alpha/9-from-hook titled Nine", it.Name, it.Title)
	}
	if note != "" {
		t.Errorf("a hook that answered should leave no note, got %q", note)
	}
	if !isDir(f.c.ItemDir(it.Name)) {
		t.Errorf("the folder was not made at %s", f.c.ItemDir(it.Name))
	}
	raw, _ := os.ReadFile(seen)
	for _, want := range []string{`"input":"thing"`, `"default":"alpha/thing"`, `"repo":"alpha"`, `"repos":["alpha"]`, `"url":false`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("stdin lacked %s: %s", want, raw)
		}
	}
}

func TestNewHookWithNoOpinionFallsThrough(t *testing.T) {
	f := newFixture(t, "name: mk\nhooks:\n  new: bin/new.sh\n", "alpha")
	f.bundleScript("bin/new.sh", "exit 0\n")
	it, _, err := f.c.NewItemNoted("second")
	if err != nil || it.Name != "alpha/second" {
		t.Errorf("got %q err %v, want alpha/second from wisp's own naming", it.Name, err)
	}
}

func TestNewHookMayRefuse(t *testing.T) {
	f := newFixture(t, "name: mk\nhooks:\n  new: bin/new.sh\n", "alpha")
	f.bundleScript("bin/new.sh", "echo 'no ticket, no item' >&2\nexit 1\n")
	_, _, err := f.c.NewItemNoted("third")
	if err == nil || !strings.Contains(err.Error(), "no ticket, no item") {
		t.Errorf("a refusing hook should surface its last line, got %v", err)
	}
	if entries, _ := os.ReadDir(f.c.VaultDir()); len(entries) != 0 {
		t.Errorf("a refused item must leave nothing behind, vault holds %v", entries)
	}
}

func TestNewHookCannotEscapeTheVault(t *testing.T) {
	for _, bad := range []string{`../x`, `a/b/c`, `/etc/passwd`, `justone`} {
		f := newFixture(t, "name: mk\nhooks:\n  new: bin/new.sh\n", "alpha")
		f.bundleScript("bin/new.sh", "echo '{\"name\":\""+bad+"\"}'\n")
		_, _, err := f.c.NewItemNoted("thing")
		if err == nil {
			t.Errorf("%q was accepted as an item name", bad)
		}
		if entries, _ := os.ReadDir(f.c.VaultDir()); len(entries) != 0 {
			t.Errorf("%q: something was created: %v", bad, entries)
		}
	}
}

// A hook asked about one item that answers with a list has not answered the question. Taking the
// first row was the silent failure a source that ignores its arguments produced under --url.
func TestAListingIsNotAnAnswer(t *testing.T) {
	list := "echo '{\"name\":\"alpha/1-a\"}'\necho '{\"name\":\"alpha/2-b\"}'\necho '{\"name\":\"alpha/3-c\"}'\n"

	f := newFixture(t, "name: mk\nhooks:\n  new: bin/new.sh\n", "alpha")
	f.bundleScript("bin/new.sh", list)
	if _, _, err := f.c.NewItemNoted("thing"); err == nil || !strings.Contains(err.Error(), "list of 3") {
		t.Errorf("new: want a refusal naming the list, got %v", err)
	}

	f = newFixture(t, "name: mk\nhooks:\n  source: bin/source.sh\n", "alpha")
	f.bundleScript("bin/source.sh", list)
	if _, err := f.c.ResolveURL("https://x/y/-/issues/1"); err == nil || !strings.Contains(err.Error(), "list of 3") {
		t.Errorf("--url: want a refusal naming the list, got %v", err)
	}
	if entries, _ := os.ReadDir(f.c.VaultDir()); len(entries) != 0 {
		t.Errorf("nothing should have been created, vault holds %v", entries)
	}
}

func TestItemMayNotSetNewOrParent(t *testing.T) {
	f := newWorkflowFixture(t)
	f.itemFile(testItem.Name, "---\nnew: bin/new.sh\nitem:\n  parent: elsewhere\npicker:\n  remote_label: jira\n---\n")
	f.trustItem(testItem.Name)
	w := f.c.WorkflowFor(testItem, "")
	if w.Hooks.New != "" || w.Item.Parent != "" || w.Picker.RemoteLabel != "gitlab" {
		t.Errorf("item keys landed: new=%q parent=%q label=%q", w.Hooks.New, w.Item.Parent, w.Picker.RemoteLabel)
	}
	for _, key := range []string{"`new`", "`item.parent`", "`picker.remote_label`"} {
		if !hasNote(w, key) {
			t.Errorf("no note about %s; notes were %v", key, w.Notes)
		}
	}
}

func TestSeedsAreWrittenOnceAndExpanded(t *testing.T) {
	f := newFixture(t, "name: mk\nitem:\n  seed: seed\n", "alpha")
	seed := filepath.Join(UserWorkflowsDir(), "mk", "seed")
	f.write(filepath.Join(seed, "notes.md"), "# {slug} in {repo}\n")
	f.write(filepath.Join(seed, "plan.md"), "plan for {item}, iid {iid}, under {parent}, {unknown} stays\n")
	f.write(filepath.Join(seed, ".envrc"), "echo no\n")
	f.write(filepath.Join(seed, "sub", "x.md"), "no\n")

	it, _, err := f.c.NewItemNoted("alpha/7-thing")
	if err != nil {
		t.Fatal(err)
	}
	dir := f.c.ItemDir(it.Name)
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<missing>"
		}
		return string(raw)
	}
	if got := read("notes.md"); got != "# 7-thing in alpha\n" {
		t.Errorf("notes.md = %q", got)
	}
	if got := read("plan.md"); got != "plan for alpha/7-thing, iid 7, under alpha, {unknown} stays\n" {
		t.Errorf("plan.md = %q", got)
	}
	for _, absent := range []string{".envrc", "sub", "x.md"} {
		if exists(filepath.Join(dir, absent)) {
			t.Errorf("%s was seeded; dotfiles and subdirectories must not be", absent)
		}
	}

	// A second pass over an existing folder leaves what is there alone.
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.c.makeItemDir(f.c.WorkflowFor(it, ""), it); err != nil {
		t.Fatal(err)
	}
	if got := read("plan.md"); got != "edited\n" {
		t.Errorf("a seed overwrote an edited file: %q", got)
	}
}

func TestAMissingSeedDirIsANoteNotAFailure(t *testing.T) {
	f := newFixture(t, "name: mk\nitem:\n  seed: nowhere\n", "alpha")
	w := f.c.WorkflowFor(Item{}, "")
	if w.Item.Seed != "" || !hasNote(w, "seed") {
		t.Errorf("seed = %q, notes %v", w.Item.Seed, w.Notes)
	}
	if it, _, err := f.c.NewItemNoted("thing"); err != nil || !exists(filepath.Join(f.c.ItemDir(it.Name), "notes.md")) {
		t.Errorf("creation should still work with the plain stub: %v", err)
	}
}

func TestParentSendsBareNamesThere(t *testing.T) {
	f := newFixture(t, "name: mk\nitem:\n  parent: _adhoc\n", "alpha", "beta")
	// Standing inside a checkout would otherwise decide it.
	if err := os.Chdir(filepath.Join(f.c.Workspace, "alpha")); err != nil {
		t.Fatal(err)
	}
	it, note, err := f.c.NewItemNoted("loose")
	if err != nil || it.Name != "_adhoc/loose" || note != "" {
		t.Errorf("got %q note %q err %v, want _adhoc/loose and no note", it.Name, note, err)
	}
	// An explicit repo is still honoured: parent is about bare names only.
	if it, _, err := f.c.NewItemNoted("beta/tied"); err != nil || it.Name != "beta/tied" {
		t.Errorf("explicit repo lost to parent: %q %v", it.Name, err)
	}
}

func TestParentMustBeOneDirectoryName(t *testing.T) {
	f := newFixture(t, "name: mk\nitem:\n  parent: ../out\n", "alpha")
	w := f.c.WorkflowFor(Item{}, "")
	if w.Item.Parent != "" || !hasNote(w, "item.parent") {
		t.Errorf("parent = %q, notes %v", w.Item.Parent, w.Notes)
	}
	if it, _, err := f.c.NewItemNoted("thing"); err != nil || it.Name != "alpha/thing" {
		t.Errorf("fallback naming broke: %q %v", it.Name, err)
	}
}
