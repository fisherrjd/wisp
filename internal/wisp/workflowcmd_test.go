package wisp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The three things `wisp workflow` writes: a bundle, a binding, and an acceptance. Each one
// touches a file somebody else owns, which is why they are the parts worth pinning down.

// newWorkflowConfig gives a workspace and a user config directory that are both temporary, so
// nothing here can reach the real ~/.config/wisp.
//
// It goes through the shared fixture rather than rolling its own. Three files had three of these
// and they disagreed about what isolation meant: this one did not neutralise WISP_PROGRAM, which
// is the last word on `program:` above every layer, so a developer with it exported ran a
// different suite here than in the other two files and than CI did.
func newWorkflowConfig(t *testing.T) Config {
	t.Helper()
	c := newWorkflowFixture(t).c
	c.Location = Location{Path: c.Workspace}
	c.Name = "t"
	if err := os.MkdirAll(c.VaultDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return c
}

// init has to be safe to mistype: it is the one command that writes a file into a directory the
// user names, and "nothing wisp does destroys work" is the whole rule.
func TestWorkflowInitRefusesAnExistingName(t *testing.T) {
	for _, tc := range []struct {
		name string
		here bool
		root func(Config) string
		addr string
	}{
		{name: "solo", root: func(Config) string { return UserWorkflowsDir() }, addr: "solo"},
		{name: "team", here: true, root: Config.WorkspaceWorkflowsDir, addr: "./team"},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			c := newWorkflowConfig(t)
			if err := c.workflowInit(tc.name, tc.here); err != nil {
				t.Fatalf("init: %v", err)
			}
			path := filepath.Join(tc.root(c), tc.name, WorkflowFile)

			// What it wrote has to be a workflow, not just a file: a starting point that does not
			// parse would cost every key it claims to set.
			w, err := LoadWorkflowFile(filepath.Dir(path))
			if err != nil {
				t.Fatalf("what init wrote does not parse: %v", err)
			}
			if w.Name != tc.name {
				t.Errorf("name = %q, want %q", w.Name, tc.name)
			}
			builtin := builtinWorkflow()
			if w.Program != builtin.Program || w.Branch != builtin.Branch || w.Worktree != builtin.Worktree {
				t.Errorf("not a copy of the built-in: %q %q %q", w.Program, w.Branch, w.Worktree)
			}
			if len(w.Layout) != len(builtin.Layout) {
				t.Errorf("layout has %d windows, want the built-in's %d", len(w.Layout), len(builtin.Layout))
			}

			// Second time round: refused, and the file left exactly as the user last had it.
			edited := "name: mine\nprogram: aider\n"
			if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			err = c.workflowInit(tc.name, tc.here)
			if err == nil {
				t.Fatal("init over an existing workflow was allowed")
			}
			if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "wisp workflow edit") {
				t.Errorf("the refusal does not say what to do next: %v", err)
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(raw) != edited {
				t.Errorf("the existing workflow was overwritten:\n%s", raw)
			}
		})
	}

	// A name that is not one path segment must never reach the filesystem.
	c := newWorkflowConfig(t)
	for _, bad := range []string{"", "..", "a/b", "../escape"} {
		if err := c.workflowInit(bad, false); err == nil {
			t.Errorf("init(%q) was allowed", bad)
		}
	}
}

// use writes one key into a file the user owns. Everything else in that file has to survive it,
// comments included, or binding a workflow becomes a reformat of a config nobody asked about.
func TestWorkflowUseWritesTheKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		here   bool
		before string
		keep   []string
	}{
		{name: "into an absent user config"},
		{name: "beside what is already there", before: "# mine\nprogram: aider\n\nworkspaces:\n  side: ~/side\n",
			keep: []string{"# mine", "program: aider", "side: ~/side"}},
		{name: "replacing an existing binding", before: "# mine\nworkflow: old\n", keep: []string{"# mine"}},
		{name: "into the workspace", here: true, before: wispYAMLTemplate, keep: []string{"# wisp workspace config"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newWorkflowConfig(t)
			if err := c.workflowInit("solo", false); err != nil {
				t.Fatal(err)
			}
			path := UserConfigPath()
			if tc.here {
				path = filepath.Join(c.Workspace, MarkerFile)
			}
			if tc.before != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if err := c.workflowUse("solo", tc.here); err != nil {
				t.Fatalf("use: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Read back through yaml rather than matched as text: the key is what the next run
			// resolves, and how it is spelled on disk is the encoder's business.
			var got struct {
				Workflow string `yaml:"workflow"`
			}
			if err := yaml.Unmarshal(raw, &got); err != nil {
				t.Fatalf("what use wrote does not parse: %v\n%s", err, raw)
			}
			if got.Workflow != "solo" {
				t.Errorf("workflow = %q, want solo:\n%s", got.Workflow, raw)
			}
			if strings.Contains(string(raw), "workflow: old") {
				t.Errorf("the old binding is still there:\n%s", raw)
			}
			for _, want := range tc.keep {
				if !strings.Contains(string(raw), want) {
					t.Errorf("lost %q:\n%s", want, raw)
				}
			}
		})
	}

	// A name with nothing behind it is a typo, and binding to it would only surface later as a
	// note about a workflow that does not load.
	c := newWorkflowConfig(t)
	if err := c.workflowUse("nosuch", false); err == nil {
		t.Error("use bound to a workflow that does not exist")
	}
}

// accept is the only security decision wisp has. It records a hash, and the hash is what makes
// accepting once not a standing permission for whatever the file becomes later.
func TestWorkflowAcceptRecordsTheHash(t *testing.T) {
	c := newWorkflowConfig(t)
	dir := filepath.Join(c.WorkspaceWorkflowsDir(), "team")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, WorkflowFile)
	if err := os.WriteFile(manifest, []byte("name: team\nprogram: aider\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// recorded reads the accept map back off disk, which is where the next run of wisp reads it.
	recorded := func() map[string]string {
		raw, err := os.ReadFile(UserConfigPath())
		if err != nil {
			return nil
		}
		var got struct {
			Accepted map[string]string `yaml:"accepted"`
		}
		if err := yaml.Unmarshal(raw, &got); err != nil {
			t.Fatalf("the user config does not parse: %v\n%s", err, raw)
		}
		return got.Accepted
	}

	// Without -y and with no terminal to ask on, nothing is written either way: it is refused
	// outright, or the empty answer declines.
	if err := c.workflowAccept("./team", false); err == nil {
		t.Error("accept went ahead with no answer and no -y")
	}
	if len(recorded()) != 0 {
		t.Errorf("an unanswered accept still wrote: %v", recorded())
	}

	if err := c.workflowAccept("./team", true); err != nil {
		t.Fatalf("accept: %v", err)
	}
	sum, err := WorkflowSum(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := c.acceptKey("./team")
	if got := recorded()[key]; got != sum {
		t.Fatalf("accepted[%q] = %q, want %q", key, got, sum)
	}

	// And the record is of this file, not of the address: an edited manifest is not accepted.
	c.Accepted = recorded()
	if ok, err := c.WorkflowAccepted("./team"); err != nil || !ok {
		t.Errorf("what was just accepted does not read as accepted: %v %v", ok, err)
	}
	if err := os.WriteFile(manifest, []byte("name: team\nprogram: rm -rf /\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.WorkflowAccepted("./team"); ok {
		t.Error("an edited workflow still reads as accepted")
	}

	// Only a workspace's own workflow needs accepting; one of yours already runs.
	if err := c.workflowAccept("team", true); err == nil {
		t.Error("accept took a bare name, which is one of yours")
	}
}

// accept refuses three different addresses for three different reasons, and the reason is the
// whole value of the message: it says where to go and look. Telling someone a workflow of theirs
// "already runs" when they never made one sends them to a directory that is not there.
func TestWorkflowAcceptSaysWhichThingIsMissing(t *testing.T) {
	c := newWorkflowConfig(t)
	if err := os.MkdirAll(filepath.Join(UserWorkflowsDir(), "mine", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ addr, want string }{
		{"nope", `no workflow "nope" anywhere`},
		{"_adhoc/nope", `no item "_adhoc/nope"`},
		{"mine", "and yours already run"},
	} {
		err := c.workflowAccept(tc.addr, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("accept %q: %v, want it to mention %q", tc.addr, err, tc.want)
		}
	}
}

// The header is the one line claiming to say what runs. With no bundle named, the name is the
// built-in's and so is the description, and printing that above a table saying `program` came
// from .wisp.yaml is the header contradicting the rows under it.
func TestTheHeaderDoesNotClaimTheBuiltinWhenAConfigOverrodeIt(t *testing.T) {
	f := newWorkflowFixture(t)
	body := "program: aider\n"
	f.spaceConfig(body)
	f.c.Accepted[f.c.acceptKey(MarkerFile)] = sumOf([]byte(body))

	w := f.c.WorkflowFor(Item{}, "")
	if got := overrides(w); len(got) != 1 || got[0] != "program" {
		t.Fatalf("overrides = %v, want just program", got)
	}
	if w.From["name"] != "" {
		t.Errorf("nothing named this workflow, so From[name] should be empty, got %q", w.From["name"])
	}

	// A bundle names itself, and then its own description is the honest one.
	f.userBundle("solo", "name: solo\ndescription: mine\n")
	f.userConfig("workflow: solo\n")
	w = f.c.WorkflowFor(Item{}, "")
	if w.Name != "solo" || w.From["name"] != "solo" {
		t.Errorf("name = %q from %q, want solo from the bundle", w.Name, w.From["name"])
	}
}

// humanBytes measures two different things and they are orders of magnitude apart. `push` reports
// a bundle, which is scripts and a manifest; the hook ceiling reports what a runaway script
// DROPPED, and a `yes` loop inside the sixty second deadline drops tens of gigabytes. Stopping a
// tier too early is a message that is technically true and unreadable, which is how "8192.0 kB"
// and then "40960.0 MB" each shipped.
func TestHumanBytesReachesTheSizesItIsAskedAbout(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{1 << 10, "1.0 kB"},
		{8 << 20, "8.0 MB"},   // the hook ceiling, named in the truncation message
		{40 << 30, "40.0 GB"}, // what a `yes` loop drops inside the deadline
	} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// Accepting an item's orchestration.md has to show the scripts it names, for the same reason the
// other two accept paths do: the decision is about what runs, and the manifest only says where to
// look. This one printed the key list and stopped, so it asked you to authorise a `provision:`
// script it never showed you, resolved against the workspace, which is the side that wrote it.
func TestAcceptingAnItemShowsTheScriptsItWouldRun(t *testing.T) {
	c := newWorkflowConfig(t)
	item := "repo/1-thing"
	body := "#!/bin/sh\necho the line nobody was shown\n"
	if err := os.MkdirAll(filepath.Join(c.Workspace, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Workspace, "bin", "harvest.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.ItemDir(item), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.ItemDir(item), "orchestration.md"),
		[]byte("---\nclose: bin/harvest.sh\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { _ = c.acceptItemManifest(item, true) })
	if !strings.Contains(out, "the line nobody was shown") {
		t.Errorf("the prompt authorised a script without printing it:\n%s", out)
	}
	if !strings.Contains(out, "close hook") {
		t.Errorf("the script is not labelled with the hook it answers for:\n%s", out)
	}
}

// captureStdout runs fn with stdout redirected and returns what it printed. The accept prompts
// write to stdout directly rather than through a writer, because they are a conversation with a
// person rather than a value returned to a caller, so this is what it takes to assert on them.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	w.Close()
	os.Stdout = saved
	return <-done
}
