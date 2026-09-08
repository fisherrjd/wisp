package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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
	if !strings.HasPrefix(panes[0].run, "claude --permission-mode auto") {
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
	trustSpaceConfigIn(t, c)

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
	trustSpaceConfigIn(t, c)

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
	trustSpaceConfigIn(t, c)
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

// The provisioning script derives the checkout directory from the arguments it is handed, so a
// workflow that wants it anywhere else has to say so. Both halves are the test: a default
// workspace must send exactly the arguments every script in the field was written against, and a
// non-default `worktree:` must either land where wisp is looking or say so out loud. Without this,
// setting `worktree:` opened a session whose briefing said "still provisioning" forever, because
// the script built one directory and wisp went on watching another.
func TestProvisionIsToldWhereTheWorktreeGoes(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	entries := []Entry{{Repo: "repo", Branch: "feature/1-thing", Base: "main"}}
	if err := os.MkdirAll(filepath.Join(c.Workspace, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	argv := filepath.Join(tmp, "argv")
	// A script on the new contract: it records what it was handed, and builds the directory it was
	// given, falling back to deriving one the way the old contract makes it.
	honours := script(t, tmp, "provision.sh", `printf '%s\n' "$@" > `+argv+`
repo=$(basename "$1"); slug=$2; wt=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--worktree" ]; then wt=$2; fi
  shift
done
[ -n "$wt" ] || wt=".worktrees/$repo--$slug"
mkdir -p "$wt"
`)
	args := func(t *testing.T) []string {
		t.Helper()
		raw, err := os.ReadFile(argv)
		if err != nil {
			t.Fatal("the script did not run")
		}
		return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	// "provisioning repo (branch)" is the ordinary announcement; anything else here is a
	// complaint, and a run that lands where wisp is looking has nothing to complain about.
	quiet := func(msg string) {
		if !strings.HasPrefix(msg, "provisioning repo (") {
			t.Errorf("logged: %s", msg)
		}
	}

	// The default, which is the acceptance test for the whole branch: the arguments wisp has
	// always sent, in the order it has always sent them, and nothing else.
	w := c.WorkflowFor(item, "")
	w.Hooks.Provision = honours
	if err := c.EnsureWorktrees(w, item, entries, quiet); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(c.Workspace, "repo"), "1-thing", "feature/1-thing", "--attach", "--base", "main", "--no-install"}
	if got := args(t); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argv was %v,\nwant             %v", got, want)
	}

	// A `worktree:` that says something else: the path wisp is going to watch goes on the end, and
	// the checkout lands there.
	w.Worktree = "{slug}"
	wt := c.WorktreeFor(w, "repo", item)
	if err := c.EnsureWorktrees(w, item, entries, quiet); err != nil {
		t.Fatal(err)
	}
	if got := args(t); len(got) < 2 || got[len(got)-2] != "--worktree" || got[len(got)-1] != wt {
		t.Errorf("argv was %v, want it ending in --worktree %s", got, wt)
	}
	if !isDir(wt) {
		t.Errorf("%s was not created, so every open would report it as still provisioning", wt)
	}

	// And a script that ignores the flag, which is every script written before it existed, is a
	// mismatch that has to be raised rather than waited on. Raised, not logged: the provision
	// window closes on a log line and stays on screen for an error, and no repo in this item can
	// get past it.
	ignores := script(t, tmp, "old.sh", "mkdir -p \".worktrees/$(basename \"$1\")--$2\"\n")
	w.Hooks.Provision = ignores
	err := c.EnsureWorktrees(w, Item{Name: "repo/2-other"}, entries, func(string) {})
	if err == nil {
		t.Fatal("a script that built the worktree somewhere else was accepted, which is the silent forever-provisioning case")
	}
	for _, want := range []string{"--worktree", c.WorktreeFor(w, "repo", Item{Name: "repo/2-other"})} {
		if !strings.Contains(err.Error(), shortPath(want)) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
}

// A repo whose window name a fixed window has already claimed gets a deduped one, and it has to
// get the same deduped one when the provisioning half comes back to create it. Names are handed
// out in layout order, so expanding a layout stripped to its per-worktree entries started with
// nothing claimed, handed the repo the fixed window's name back, found that name already in the
// session and skipped the repo as done. Delete this and a repo sharing a name with one of your own
// windows silently never gets one.
func TestWorktreeWindowNamesDoNotMoveBetweenTheTwoHalves(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	w.Layout = []Window{
		{Window: "api", Cwd: "workspace"},
		{Window: "{repo}", For: "each-worktree", Cwd: "worktree"},
		{Window: "provision", When: "provisioning", Cwd: "workspace", Run: "{wisp} provision {item}"},
	}
	entries := []Entry{{Repo: "api"}}

	// At open, with no checkout yet: the fixed window has the name, and the repo has no window.
	cold := c.expandLayout(w, item, entries, "")
	if len(cold) != 2 || cold[0].name != "api" || cold[1].name != "provision" {
		t.Fatalf("cold open gave %+v, want the fixed api window and the provision one", cold)
	}

	// And once the checkout lands, which is the expansion the provisioning half runs.
	if err := os.MkdirAll(c.WorktreeFor(w, "api", item), 0o755); err != nil {
		t.Fatal(err)
	}
	var worktrees []pane
	for _, p := range c.expandLayout(w, item, entries, "") {
		if p.perWorktree {
			worktrees = append(worktrees, p)
		}
	}
	if len(worktrees) != 1 {
		t.Fatalf("got %d per-worktree windows, want one: %+v", len(worktrees), worktrees)
	}
	if worktrees[0].name == "api" {
		t.Error("the repo was handed the fixed window's name, so it would be skipped as already made")
	}

	// A name must not depend on which checkouts happen to exist either, or the two halves disagree
	// about which window belongs to which repo the moment one lands before the other.
	two := []Entry{{Repo: "platform-service-alpha"}, {Repo: "platform-service-beta"}}
	if err := os.MkdirAll(c.WorktreeFor(w, "platform-service-beta", item), 0o755); err != nil {
		t.Fatal(err)
	}
	name := func(repo string) string {
		t.Helper()
		for _, p := range c.expandLayout(w, item, two, "") {
			if p.perWorktree && p.dir == c.WorktreeFor(w, repo, item) {
				return p.name
			}
		}
		return ""
	}
	half := name("platform-service-beta")
	if err := os.MkdirAll(c.WorktreeFor(w, "platform-service-alpha", item), 0o755); err != nil {
		t.Fatal(err)
	}
	if whole := name("platform-service-beta"); whole != half {
		t.Errorf("the second repo's window was %q with one checkout and %q with both", half, whole)
	}
}

// The collision path shortens the name to make room for the ~2, and it has to cut by runes the way
// windowName does. Two repos collide only when they share a prefix, so a shared multibyte prefix
// is exactly what reaches this, and a byte cut lands mid-character and hands tmux a broken escape
// sequence for the repo unlucky enough to be second.
func TestCollidingWindowNamesAreCutByRunes(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	entries := []Entry{{Repo: "платформа-сервис-альфа"}, {Repo: "платформа-сервис-бета"}}
	for _, e := range entries {
		if err := os.MkdirAll(c.WorktreeFor(w, e.Repo, item), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, p := range c.expandLayout(w, item, entries, "") {
		if !utf8.ValidString(p.name) {
			t.Errorf("window name %q is not valid UTF-8: the cut landed mid-character", p.name)
		}
		if seen[p.name] {
			t.Errorf("two windows named %q", p.name)
		}
		seen[p.name] = true
	}
	if len(seen) != 3 {
		t.Errorf("got %d windows, want agent plus one per worktree: %v", len(seen), seen)
	}
}

// An empty prompt has to leave no argument at all rather than an empty one. The built-in agent
// window is `{program} {prompt}` and the run line is shell-quoted before substitution, so an item
// with no context file to inline ran claude with an empty quoted argument, which most agent CLIs
// read as being handed an empty prompt rather than as being handed none. Reachable through
// WriteContext returning "" for an item whose folder is not there, and through every call
// addWorktreeWindows makes.
func TestAnEmptyPromptLeavesNoArgument(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	w := c.WorkflowFor(item, "")
	entries := []Entry{{Repo: "repo", Branch: "feature/1-thing"}}

	panes := c.expandLayout(w, item, entries, "")
	if panes[0].run != "claude" {
		t.Errorf("the agent window runs %q, want the program on its own", panes[0].run)
	}

	// And a prompt that exists is still one quoted argument, which is the property this must not
	// have undone: the context carries the item's own notes, and an item name can come from a hook.
	ctx, err := c.WriteContext(w, item, entries)
	if err != nil || ctx == "" {
		t.Fatalf("no context file to inline: %v", err)
	}
	panes = c.expandLayout(w, item, entries, ctx)
	if !strings.HasPrefix(panes[0].run, "claude '") || !strings.HasSuffix(panes[0].run, "'") {
		t.Errorf("the prompt is not one quoted argument: %q", panes[0].run)
	}
}

// A hook that never returns must not take the picker, or an open, with it.
func TestHooksAreBounded(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	// Far below hookTimeout, which is a minute: the point is the bound exists and is honoured,
	// not how long it is.
	big := script(t, t.TempDir(), "loud.sh", "yes hello | head -c 20000000\n")
	out, err := c.runHook(big, nil)
	if len(out) > maxHookOutput {
		t.Errorf("hook output was %d bytes, want it capped at %d", len(out), maxHookOutput)
	}
	// The bound holds and it is reported. Truncation used to return a nil error, which made a
	// source that answered most of the question look exactly like one that answered all of it.
	if err == nil {
		t.Fatal("a truncated hook reported no error, so a short answer would paint as a complete one")
	}
	if !strings.Contains(err.Error(), "dropped") {
		t.Errorf("the error does not say what was lost: %v", err)
	}
}

// A one-shot is written to no file, so the provisioning half, which is a separate process, can
// only learn about it from the session. Carried as a tmux session option rather than a template
// token: a hand-written layout would not know to include the token, and the two halves would then
// build worktrees in different places without saying so.
func TestOneShotIsRecordedOnTheResolution(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	item := Item{Name: "repo/1-thing"}
	// Written where the resolver will actually look. An earlier version of this built its own
	// t.TempDir and reset XDG_CONFIG_HOME to it, so the bundle sat somewhere nothing consulted
	// and the test passed without ever loading it.
	dir, err := c.WorkflowDir("review")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, WorkflowFile), []byte("name: review\nprogram: aider\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := c.WorkflowFor(item, "review")
	if !w.OneShot || w.Addr != "review" {
		t.Errorf("a --workflow resolution is not marked as one-shot: OneShot=%v Addr=%q", w.OneShot, w.Addr)
	}
	// Proof the bundle was found rather than merely named.
	if w.Program != "aider" {
		t.Errorf("the one-shot bundle was not loaded: program = %q", w.Program)
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

// A source that has since broken must annotate the list, never empty it. The cache hands back
// stale rows alongside the reason they are stale, and the caller has to keep both: returning
// early on the error turned "here is an old list, and here is why" back into no list at all.
func TestABrokenSourceKeepsTheStaleRows(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	zero := 0
	c.CacheTTLMin = &zero // every load re-asks, so the refresh always runs
	tmp := t.TempDir()

	good := script(t, tmp, "good.sh", `echo '{"name":"repo/7-warm","title":"warm"}'`+"\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("source: "+good+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trustSpaceConfigIn(t, c)
	if items, err := c.RemoteItems(); err != nil || len(items) != 1 {
		t.Fatalf("warming the cache: %d items, err %v", len(items), err)
	}

	// Same script path, now failing, so the cache it already wrote is the stale one.
	if err := os.WriteFile(good, []byte("#!/bin/sh\necho 'tracker is down' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	items, err := c.RemoteItems()
	if err == nil {
		t.Error("a broken source reported no error, so nothing would annotate the list")
	}
	if len(items) != 1 || items[0].Name != "repo/7-warm" {
		t.Errorf("the stale rows were dropped: %+v", items)
	}
}

// The output cap truncates rather than killing, and says that it did. Two separate properties,
// and both have been wrong at some point.
//
// Reporting the short length back to io.Copy is a short write, which closes the pipe and kills the
// hook with SIGPIPE, producing nothing at all instead of the first 8 MB, which is the opposite of
// what a cap is for. That is why the full length is always returned. But reporting nothing to the
// caller either made a truncated answer indistinguishable from a whole one: the shortened rows
// were cached and painted as complete, which is the same failure as a broken source looking like
// a quiet one, wearing a third face.
func TestTheOutputCapTruncatesRatherThanKilling(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	var b strings.Builder
	for b.Len() <= maxHookOutput {
		b.WriteString(strings.Repeat("x", 1<<16) + "\n")
	}
	big := script(t, t.TempDir(), "loud.sh", "cat <<'EOF'\n"+b.String()+"EOF\n")

	out, err := c.runHook(big, nil)
	// Kept, not killed: the bytes are the proof the pipe stayed open.
	if len(out) != maxHookOutput {
		t.Errorf("kept %d bytes, want exactly the cap %d", len(out), maxHookOutput)
	}
	if err == nil {
		t.Fatal("truncation was silent, so a partial answer would be indistinguishable from a whole one")
	}
	// Not a kill. A hook that died on SIGPIPE would say so, and would have handed back nothing.
	if strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "signal") {
		t.Errorf("the hook was killed instead of truncated: %v", err)
	}
}
