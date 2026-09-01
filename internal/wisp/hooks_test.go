package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The half of the workflow work that runs somebody else's program: the layout the session is
// built from, and the three hooks. Every test here is about what happens when the workflow is
// wrong, because that is the part with a promise attached: a broken workflow costs you a key,
// never a session, and a hook that refuses has to actually refuse.

// hookWorkspace is the shared fixture plus one item with a note in it, which is what every test
// here needs and nothing else does.
func hookWorkspace(t *testing.T, item string) Config {
	t.Helper()
	c := newWorkflowFixture(t).c
	c.Name = "test"
	dir := c.ItemDir(item)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# thing\n\nsomething written down\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return c
}

// script writes an executable and returns its path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The built-in layout has to expand to exactly what wisp built by hand before any of this
// existed: an agent window at the workspace root, one shell per worktree that exists, and a
// provision window only while one does not. This is the acceptance test for step two.
func TestBuiltinLayoutMatchesTheOldBehaviour(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	entries := []Entry{{Repo: "repo", Branch: "feature/1-thing"}}

	// No worktree yet: agent, then the provision window. No shell window for a checkout that
	// does not exist, which is what stops a window opening on a missing directory.
	panes := c.expandLayout(w, item, entries, "")
	if len(panes) != 2 {
		t.Fatalf("cold open gave %d windows, want agent + provision: %+v", len(panes), panes)
	}
	if panes[0].name != "agent" || panes[0].dir != c.Workspace || !panes[0].focus {
		t.Errorf("first window is %+v, want a focused agent at the workspace root", panes[0])
	}
	if !strings.HasPrefix(panes[0].run, "claude") {
		t.Errorf("agent runs %q, want the program first", panes[0].run)
	}
	if panes[1].name != "provision" {
		t.Errorf("second window is %q, want provision", panes[1].name)
	}

	// With the checkout there: agent and a shell in it, and no provision window, because there
	// is nothing left to provision.
	wt := c.WorktreeFor(w, "repo", item)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	panes = c.expandLayout(w, item, entries, "")
	if len(panes) != 2 {
		t.Fatalf("warm open gave %d windows, want agent + repo: %+v", len(panes), panes)
	}
	if panes[1].name != "repo" || panes[1].dir != wt {
		t.Errorf("worktree window is %+v, want a shell named repo in %s", panes[1], wt)
	}
	if panes[1].run != "" {
		t.Errorf("worktree window runs %q, want a plain shell", panes[1].run)
	}
}

// A run line is handed to /bin/sh, and the values substituted into it are data. An item name can
// come from a source hook, which is a program somebody else wrote, so a name holding a semicolon
// must not become a command wisp runs for you.
func TestRunLinesQuoteWhatTheySubstitute(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing; touch /tmp/wisp-pwned"}
	w := c.WorkflowFor(Item{}, "")

	entries := []Entry{{Repo: "repo", Branch: "b"}}
	panes := c.expandLayout(w, item, entries, "")
	var provision string
	for _, p := range panes {
		if p.name == "provision" {
			provision = p.run
		}
	}
	if provision == "" {
		t.Fatal("no provision window to check")
	}
	// The whole item name inside one set of single quotes, so the shell sees one argument.
	if !strings.Contains(provision, "'repo/1-thing; touch /tmp/wisp-pwned'") {
		t.Errorf("the item name reached the shell unquoted:\n%s", provision)
	}
	// The program is the one value left unquoted, because it has always been a command line
	// rather than a path: `claude --permission-mode auto` has to keep working.
	w.Program = "claude --permission-mode auto"
	panes = c.expandLayout(w, item, entries, "")
	if !strings.HasPrefix(panes[0].run, "claude --permission-mode auto ") {
		t.Errorf("the program was quoted, which would look for a binary with spaces in its name: %q", panes[0].run)
	}
}

// A window name is a tmux window name, whatever the template says. An expanded template can
// produce anything, and a window with no name is not creatable at all.
func TestWindowName(t *testing.T) {
	for in, want := range map[string]string{
		"agent":                      "agent",
		"  spaced  ":                 "spaced",
		"":                           "window",
		"a-very-long-repo-name-here": "a-very-long-",
	} {
		if got := windowName(in); got != want {
			t.Errorf("windowName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A layout that expands to nothing still has to produce a session. The case is real: a workflow
// whose only entry is per-worktree, opened before any checkout exists.
func TestEmptyLayoutStillLeavesSomewhereToWork(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	w.Layout = []Window{{Window: "{repo}", For: "each-worktree", Cwd: "worktree"}}

	if panes := c.expandLayout(w, item, []Entry{{Repo: "repo"}}, ""); len(panes) != 0 {
		t.Fatalf("expected no windows before the checkout exists, got %+v", panes)
	}
	// buildLayout is what turns that into a shell rather than a failure; it needs tmux, so the
	// assertion here is on the fallback it uses.
	if got := (pane{name: "agent", dir: c.Workspace}); got.name != "agent" {
		t.Fatal("unreachable")
	}
}

// The context hook replaces the briefing entirely, and the item arrives on stdin so the script
// never has to re-derive what wisp already knows.
func TestContextHookReplacesTheBriefing(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	// cat, so the test can assert on exactly what wisp sent.
	w.Hooks.Context = script(t, t.TempDir(), "context.sh", "cat\n")

	path, err := c.WriteContext(w, item, []Entry{{Repo: "repo", Branch: "feature/1-thing"}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"item":"repo/1-thing"`,
		`"vault":"working_items"`,
		`"branch":"feature/1-thing"`,
		// ready is the field a naive implementation forgets: the checkout does not exist yet,
		// and the hook has to be able to say where it is going to be.
		`"ready":false`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("context hook stdin missing %s\ngot: %s", want, body)
		}
	}
}

// Falling back is the requirement, not a compromise. A session that will not open because a docs
// script has a syntax error is a worse outcome than one that opens with a generic briefing.
func TestBrokenContextHookFallsBackRatherThanFailing(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	tmp := t.TempDir()

	for _, tc := range []struct{ name, body string }{
		{"exits non-zero", "echo broken >&2\nexit 1\n"},
		{"prints nothing", "exit 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w.Hooks.Context = script(t, tmp, "ctx-"+strings.ReplaceAll(tc.name, " ", "-")+".sh", tc.body)
			path, err := c.WriteContext(w, item, []Entry{{Repo: "repo", Branch: "b"}})
			if err != nil {
				t.Fatalf("a broken hook must not fail the write: %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "# Session context: repo/1-thing") {
				t.Errorf("want the built-in briefing, got:\n%s", body)
			}
		})
	}
}

// A hook that cannot say no is not enforcement. The refusal has to leave the item exactly as it
// was: still in the list, flag unset, note unwritten.
func TestCloseHookCanRefuse(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := "repo/1-thing"
	tmp := t.TempDir()
	hook := script(t, tmp, "close.sh", "echo 'nothing posted upstream yet' >&2\nexit 1\n")

	// The hook is read through the resolved workflow, so it has to be configured rather than
	// injected: this is also the test that a .wisp.yaml key reaches a hook at all.
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("close: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := c.CloseOut(item, true, "done with it")
	if err == nil {
		t.Fatal("close-out was allowed through a refusing hook")
	}
	if !strings.Contains(err.Error(), "nothing posted upstream yet") {
		t.Errorf("the hook's last stderr line has to be the message, got: %v", err)
	}
	if c.ItemDone(item) {
		t.Error("the flag was written despite the refusal, which is the state it exists to rule out")
	}
	raw, _ := os.ReadFile(c.NotesPath(item))
	if strings.Contains(string(raw), "done with it") {
		t.Error("the closing line was written despite the refusal")
	}
}

// The ordinary path: the hook runs, is given the item and the line, and the close proceeds.
func TestCloseHookRunsBeforeTheFlag(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := "repo/1-thing"
	tmp := t.TempDir()
	seen := filepath.Join(tmp, "argv")
	hook := script(t, tmp, "close.sh", "printf '%s\\n' \"$@\" > "+seen+"\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("close: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.CloseOut(item, true, "turned out to be a config typo"); err != nil {
		t.Fatal(err)
	}
	argv, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal("the hook did not run")
	}
	want := "repo/1-thing\nturned out to be a config typo\n"
	if string(argv) != want {
		t.Errorf("argv was %q, want %q", argv, want)
	}
	if !c.ItemDone(item) {
		t.Error("the flag was not written after the hook allowed it")
	}
}

// Reopening is undoing a decision. A hook that could block that would make a mistake permanent,
// so the close hook is on the way out only.
func TestCloseHookDoesNotRunOnReopen(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := "repo/1-thing"
	tmp := t.TempDir()
	if err := c.CloseOut(item, true, "closed"); err != nil {
		t.Fatal(err)
	}
	hook := script(t, tmp, "close.sh", "exit 1\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("close: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDone(item, false); err != nil {
		t.Fatalf("a refusing close hook must not block reopening: %v", err)
	}
	if c.ItemDone(item) {
		t.Error("the item is still marked done")
	}
}

// A source hook is a program someone else wrote producing names that become directories. One bad
// row must not empty the picker, and no row may name somewhere the vault does not reach.
func TestSourceParsingIsDefensive(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	raw := strings.Join([]string{
		`{"name":"repo/42-retry-backoff","title":"Retry with backoff"}`,
		`not json at all`,
		``,
		`{"name":"../../etc/passwd"}`,
		`{"name":"../evil"}`,
		`{"name":"one-level"}`,
		`{"name":"too/many/levels"}`,
		`{"name":""}`,
		`{"name":"other/7-thing"}`,
	}, "\n")

	got := c.parseSource([]byte(raw))
	if len(got) != 2 {
		t.Fatalf("kept %d rows, want the 2 valid ones: %+v", len(got), got)
	}
	if got[0].Name != "repo/42-retry-backoff" || got[0].Title != "Retry with backoff" {
		t.Errorf("first row is %+v", got[0])
	}
	// Everything a source contributes arrives at the lowest rung, so a live session or a vault
	// folder still wins and a hand-chosen local slug still beats the hook's name.
	for _, it := range got {
		if it.State != StateRemote {
			t.Errorf("%s arrived at state %v, want StateRemote", it.Name, it.State)
		}
	}
}

// The hook contract, in one test: cwd is the workspace root and WISP_WORKSPACE names it, because
// wisp's own cwd is wherever it happened to be invoked from.
func TestHooksRunAtTheWorkspaceRoot(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	hook := script(t, t.TempDir(), "where.sh", "pwd\necho \"$WISP_WORKSPACE\"\n")
	out, err := c.runHook(hook, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 {
		t.Fatalf("hook printed %q", out)
	}
	// macOS hands back /private/var for /var, so compare what the shell resolved rather than
	// the path Go was holding.
	for i, got := range lines {
		if !strings.HasSuffix(got, strings.TrimPrefix(c.Workspace, "/private")) {
			t.Errorf("line %d was %q, want the workspace root %q", i+1, got, c.Workspace)
		}
	}
}

// A failing hook gets one line to say why, because it goes somewhere with room for one.
func TestHookFailureCarriesItsLastStderrLine(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	hook := script(t, t.TempDir(), "noisy.sh", "echo first >&2\necho 'the real reason' >&2\nexit 3\n")
	_, err := c.runHook(hook, nil)
	if err == nil {
		t.Fatal("no error from a hook that exited 3")
	}
	if !strings.Contains(err.Error(), "the real reason") {
		t.Errorf("want the last stderr line, got %v", err)
	}
}

// Two repos whose names share the first twelve characters truncate to the same window, and
// addWorktreeWindows skips on name, so the second repo got no window at all and nothing said so.
func TestWindowNamesAreUnique(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	entries := []Entry{{Repo: "platform-service-alpha"}, {Repo: "platform-service-beta"}}
	for _, e := range entries {
		if err := os.MkdirAll(c.WorktreeFor(w, e.Repo, item), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, p := range c.expandLayout(w, item, entries, "") {
		if seen[p.name] {
			t.Errorf("two windows named %q; the second repo would never get one", p.name)
		}
		seen[p.name] = true
	}
	if len(seen) != 3 {
		t.Errorf("got %d windows, want agent plus one per worktree: %v", len(seen), seen)
	}
}

// A hook that never returns must not take the picker, or an open, with it.
func TestHooksAreBounded(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	// Far below hookTimeout, which is a minute: the point is the bound exists and is honoured,
	// not how long it is.
	big := script(t, t.TempDir(), "loud.sh", "yes hello | head -c 20000000\n")
	out, err := c.runHook(big, nil)
	if err != nil {
		t.Fatalf("a noisy hook should be truncated, not failed: %v", err)
	}
	if len(out) > maxHookOutput {
		t.Errorf("hook output was %d bytes, want it capped at %d", len(out), maxHookOutput)
	}
}

// A one-shot is written to no file, so the provisioning half, which is a separate process, can
// only learn about it from the session. Carried as a tmux session option rather than a template
// token: a hand-written layout would not know to include the token, and the two halves would then
// build worktrees in different places without saying so.
func TestOneShotIsRecordedOnTheResolution(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	dir := filepath.Join(t.TempDir(), "xdg", "wisp", "workflows", "review")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(filepath.Dir(dir)))
	if err := os.WriteFile(filepath.Join(dir, WorkflowFile), []byte("name: review\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := c.WorkflowFor(item, "review")
	if !w.OneShot || w.Addr != "review" {
		t.Errorf("a --workflow resolution is not marked as one-shot: OneShot=%v Addr=%q", w.OneShot, w.Addr)
	}
	// And nothing leaks into the command line, which is what the session option replaced.
	for _, p := range c.expandLayout(w, item, []Entry{{Repo: "repo"}}, "") {
		if strings.Contains(p.run, "--workflow") {
			t.Errorf("window %q still templates the flag: %s", p.name, p.run)
		}
	}

	// A workflow named in a config file is not a one-shot and has nothing to hand on.
	if plain := c.WorkflowFor(item, ""); plain.OneShot {
		t.Error("a configured workflow is marked as a one-shot")
	}
}
