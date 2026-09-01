package wisp

import (
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
func newWorkflowConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "working_items"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Config{Workspace: ws, Vault: "working_items", Name: "t", Location: Location{Path: ws}}
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
