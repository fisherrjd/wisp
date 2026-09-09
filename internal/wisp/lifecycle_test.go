package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenHookSeesTheSessionAndCannotBlockIt(t *testing.T) {
	f := newFixture(t, "name: mk\nhooks:\n  open: bin/open.sh\n", "alpha")
	seen := filepath.Join(t.TempDir(), "stdin")
	f.bundleScript("bin/open.sh", "cat > "+seen+"\necho 'open went wrong' >&2\nexit 1\n")
	item := Item{Name: "alpha/3-thing"}
	w := f.c.WorkflowFor(item, "")

	var notes []string
	f.c.runOpenHook(w, item, []Entry{{Repo: "alpha", Branch: "feature/3-thing"}}, "wisp_probe_alpha-3-thing", func(s string) { notes = append(notes, s) })

	raw, _ := os.ReadFile(seen)
	for _, want := range []string{`"item":"alpha/3-thing"`, `"session":"wisp_probe_alpha-3-thing"`, `"repo":"alpha"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("stdin lacked %s: %s", want, raw)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "open hook: open.sh: open went wrong") {
		t.Errorf("a failing open hook should be one note and nothing more, got %v", notes)
	}
}

func TestKillHookRunsAfterAndCannotVeto(t *testing.T) {
	f := newFixture(t, "name: mk\nhooks:\n  kill: bin/kill.sh\n", "alpha")
	seen := filepath.Join(t.TempDir(), "stdin")
	f.bundleScript("bin/kill.sh", "cat > "+seen+"\necho 'logged, but grumpy' >&2\nexit 1\n")

	note := f.c.runKillHook("alpha/3-thing", "wisp_probe_alpha-3-thing")
	if !strings.Contains(note, "kill hook: kill.sh: logged, but grumpy") {
		t.Errorf("note = %q", note)
	}
	raw, _ := os.ReadFile(seen)
	for _, want := range []string{`"item":"alpha/3-thing"`, `"session":"wisp_probe_alpha-3-thing"`, `"dir":"working_items/alpha/3-thing"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("stdin lacked %s: %s", want, raw)
		}
	}
	// No hook, no note, no complaint.
	f2 := newFixture(t, "name: mk\n", "alpha")
	if note := f2.c.runKillHook("alpha/3-thing", "s"); note != "" {
		t.Errorf("no hook should mean no note, got %q", note)
	}
}

func TestPreviewHookReplacesTheSummaryAndFallsBackWhenBroken(t *testing.T) {
	f := newFixture(t, "name: mk\nhooks:\n  preview: bin/preview.sh\n", "alpha")
	f.bundleScript("bin/preview.sh", "printf 'hook says: %s\\n' \"$1\"\n")
	it, _, err := f.c.NewItemNoted("alpha/5-thing")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.c.Preview(it, 80); !strings.HasPrefix(got, "hook says: alpha/5-thing") {
		t.Errorf("preview = %q, want the hook's text", got)
	}

	f.bundleScript("bin/preview.sh", "exit 1\n")
	f.c.ForgetWorkflows()
	if got := f.c.Preview(it, 80); !strings.Contains(got, "# 5-thing") {
		t.Errorf("a broken preview hook should fall back to the notes, got %q", got)
	}

	f.bundleScript("bin/preview.sh", "exit 0\n")
	if got := f.c.Preview(it, 80); !strings.Contains(got, "# 5-thing") {
		t.Errorf("an empty answer should fall back to the notes, got %q", got)
	}
}

func TestRemoteLabelComesFromTheWorkflow(t *testing.T) {
	f := newFixture(t, "name: mk\npicker:\n  remote_label: github\n", "alpha")
	if got := f.c.RemoteLabel(); got != "github" {
		t.Errorf("label = %q, want github", got)
	}
	if got := newWorkflowFixture(t).c.RemoteLabel(); got != "gitlab" {
		t.Errorf("default label = %q, want gitlab", got)
	}
	// Two words is not a legend entry.
	f = newFixture(t, "name: mk\npicker:\n  remote_label: \"my tracker\"\n", "alpha")
	w := f.c.WorkflowFor(Item{}, "")
	if w.Picker.RemoteLabel != "gitlab" || !hasNote(w, "remote_label") {
		t.Errorf("label = %q notes %v", w.Picker.RemoteLabel, w.Notes)
	}
}

func TestRankSurvivesMerge(t *testing.T) {
	into := map[string]Item{}
	var order []string
	Merge(into, &order, Item{Name: "a/1-x", State: StateLive})
	Merge(into, &order, Item{Name: "a/1-from-source", State: StateRemote, Rank: 2})
	if got := into["a/1"]; got.Rank != 2 || got.State != StateLive || got.Name != "a/1-x" {
		t.Errorf("merged = %+v, want the local name and state with the source's rank", got)
	}
}
