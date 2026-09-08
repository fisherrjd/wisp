package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Closing out has two halves that used to fight: the close hook must be asked exactly once about
// a piece of work, and the note must take every line anyone offers it. The tests here are all
// about the second close, which is where making one of those true broke the other.

// Delete this and `wisp done <item> -m '<line>'` goes back to throwing the line away whenever the
// item was already closed, while still reporting that it wrote it up. A note accumulates: the
// second line is someone adding to the write-up of finished work, not a duplicate close.
func TestClosingAnAlreadyClosedItemStillTakesTheLine(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := "repo/1-thing"
	tmp := t.TempDir()
	// The hook counts by appending, so "asked twice" is a fact readable off the file afterwards
	// rather than something the test has to catch as it happens.
	runs := filepath.Join(tmp, "runs")
	hook := script(t, tmp, "close.sh", "echo ran >> "+runs+"\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("close: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trustSpaceConfigIn(t, c)

	// The first close is the ordinary path, and has to keep behaving exactly as it did.
	if err := c.CloseOut(item, true, "it was a caching bug in the router"); err != nil {
		t.Fatal(err)
	}
	if !c.ItemDone(item) {
		t.Fatal("the first close did not set the flag")
	}
	if got := runLines(t, runs); got != 1 {
		t.Fatalf("the close hook ran %d times on the first close, want 1", got)
	}

	// The second close finds it already done. The hook has had its say, but the line is new.
	if err := c.CloseOut(item, true, "and the retry logic was masking it"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(c.NotesPath(item))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"it was a caching bug in the router",
		"and the retry logic was masking it",
		"something written down", // the prose that was there before either close
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the note lost %q:\n%s", want, got)
		}
	}
	if !c.ItemDone(item) {
		t.Errorf("the second close cleared the flag:\n%s", got)
	}
	// The whole reason the guard exists: a hook that harvests an item, or refuses until it has,
	// must not be asked twice about work that finished the first time.
	if n := runLines(t, runs); n != 1 {
		t.Errorf("the close hook ran %d times across two closes, want 1", n)
	}
}

// Delete this and a bare second close starts running the close hook again and rewriting a note
// that has nothing new in it. Both are visible: the hook may post upstream, and the note's
// modification time is how a vault says what was last worked on.
func TestClosingAnAlreadyClosedItemBareChangesNothing(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := "repo/1-thing"
	tmp := t.TempDir()

	if err := c.CloseOut(item, true, "finished and written up"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(c.NotesPath(item))
	if err != nil {
		t.Fatal(err)
	}

	// Configured only now, and refusing, so that the second close returning nil is proof the hook
	// was never reached rather than proof it happened to succeed.
	hook := script(t, tmp, "close.sh", "echo 'nothing posted upstream yet' >&2\nexit 1\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("close: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trustSpaceConfigIn(t, c)

	if err := c.CloseOut(item, true, ""); err != nil {
		t.Fatalf("a bare close of finished work must be a quiet no-op: %v", err)
	}
	after, err := os.ReadFile(c.NotesPath(item))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the note was rewritten by a close with nothing to say:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !c.ItemDone(item) {
		t.Errorf("the flag was cleared:\n%s", after)
	}
}

// runLines counts what a counting hook wrote. A file that is not there yet is nought runs, which
// is the answer a test wants before the hook has been reached at all.
func runLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(raw)))
}

// A close hook's stdout is read by nobody, so crossing the output ceiling must not stop an item
// being closed out. The ceiling reports truncation as an error now, which is right for `source`
// and `context`, whose answers get shorter; here it would make a chatty script a veto over
// finishing work, so the close path lets that one error through.
func TestAChattyCloseHookDoesNotVetoTheCloseOut(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	loud := script(t, t.TempDir(), "loud-close.sh", "yes hello | head -c 20000000\nexit 0\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("close: "+loud+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trustSpaceConfigIn(t, c)

	if err := c.CloseOut("repo/1-thing", true, "finished"); err != nil {
		t.Fatalf("a close hook that printed too much blocked the close: %v", err)
	}
	if !c.ItemDone("repo/1-thing") {
		t.Error("the item is not marked done, so the hook's noise cost the close after all")
	}
}
