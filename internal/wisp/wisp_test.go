package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pure half of the package: naming, parsing and merging. None of it needs tmux, ssh or a
// filesystem, and all of it decides where files go and which item is which.

func TestParseLocation(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Location
	}{
		{"~/work", Location{Path: "~/work"}},
		{"/var/lib/thing", Location{Path: "/var/lib/thing"}},
		// A colon after a slash is part of the path, not a host. Otherwise a directory with a
		// colon in its name would silently become a machine nobody can reach.
		{"/var/lib/a:b", Location{Path: "/var/lib/a:b"}},
		{"bigbox:~/work", Location{Host: "bigbox", Path: "~/work"}},
		{"jade@eldo:/srv/work", Location{Host: "jade@eldo", Path: "/srv/work"}},
		{"  bigbox:~/work  ", Location{Host: "bigbox", Path: "~/work"}},
		// A trailing colon is a machine with no workspace named on it. CreateWorkspace refuses
		// this by hand, so it has to survive parsing to get there.
		{"bigbox:", Location{Host: "bigbox"}},
	} {
		if got := ParseLocation(tc.in); got != tc.want {
			t.Errorf("ParseLocation(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestLocationStringRoundTrip(t *testing.T) {
	for _, in := range []string{"~/work", "/srv/work", "bigbox:~/work", "jade@eldo:/srv/work"} {
		if got := ParseLocation(in).String(); got != in {
			t.Errorf("round trip of %q gave %q", in, got)
		}
	}
}

func TestHostName(t *testing.T) {
	for in, want := range map[string]string{
		"eldo":            "eldo",
		"jade@eldo":       "eldo",
		"jade@eldo.local": "eldo",
		"eldo:":           "eldo",
		"big_box":         "big-box", // underscores separate the halves of a session name
	} {
		if got := HostName(in); got != want {
			t.Errorf("HostName(%q) = %q, want %q", in, got, want)
		}
	}
}

// slugifyPath must never produce a segment that walks out of the vault: ItemDir joins its result
// straight onto the vault path and NewItem then calls MkdirAll on it.
func TestSlugifyPathRejectsTraversal(t *testing.T) {
	for _, in := range []string{"..", ".", "...", "../", "./", " .. "} {
		if got := slugifyPath(in); got != "" {
			t.Errorf("slugifyPath(%q) = %q, want \"\" (it would escape the vault)", in, got)
		}
	}
}

func TestSlugifyPath(t *testing.T) {
	for in, want := range map[string]string{
		"My Thing":       "my-thing",
		"wisp":           "wisp",
		"a//b":           "a-b",
		"feature/thing":  "feature-thing",
		"trailing---":    "trailing",
		"under_score":    "under_score",
		"dots.in.name":   "dots.in.name",
		"../thing":       "..-thing", // a dot pair only traverses when it is the whole segment
		"Ünïcödé thing?": "n-c-d-thing",
	} {
		if got := slugifyPath(in); got != want {
			t.Errorf("slugifyPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Whatever slugifyPath returns has to stay inside the vault once ItemDir joins it on.
func TestItemDirStaysInVault(t *testing.T) {
	c := Config{Workspace: "/ws", Vault: "working_items"}
	for _, in := range []string{"..", "../escape", "./x", "a/../../b"} {
		seg := slugifyPath(in)
		if seg == "" {
			continue // refused outright, which is the other correct answer
		}
		dir := filepath.Clean(c.ItemDir("_adhoc/" + seg))
		if !strings.HasPrefix(dir, filepath.Clean(c.VaultDir())+string(filepath.Separator)) {
			t.Errorf("input %q produced %q, outside %q", in, dir, c.VaultDir())
		}
	}
}

// Everything wisp creates, wisp must list. `wisp new <repo>/<name>` files an item under a repo
// with no ticket number in front of it, and the list used to require one, so those items were
// created, opened, and then absent from the picker.
func TestLocalItemsListsWhatNewCreates(t *testing.T) {
	ws := t.TempDir()
	c := Config{Workspace: ws, Vault: "working_items"}
	for _, dir := range []string{
		"_adhoc/my-thing",
		"repo/42-with-an-iid",
		"repo/no-iid-here",
		"repo/.git", // infrastructure, not work
		".obsidian/whatever",
	} {
		if err := os.MkdirAll(filepath.Join(c.VaultDir(), dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A hub note sits beside the items rather than being one.
	if err := os.WriteFile(filepath.Join(c.VaultDir(), "repo", "repo.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := c.LocalItems()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Name)
	}
	want := []string{"_adhoc/my-thing", "repo/42-with-an-iid", "repo/no-iid-here"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("LocalItems = %v, want %v", got, want)
	}
}

// Open tolerates a missing vault folder so a GitLab item can be started before it has one. That
// tolerance let a typo on the command line build a whole session around itself, so the CLI checks
// the name first.
func TestRequireItem(t *testing.T) {
	ws := t.TempDir()
	c := Config{Workspace: ws, Vault: "working_items", Name: "probe"}
	for _, dir := range []string{"working_items/_adhoc/has-folder", "myrepo/.git"} {
		if err := os.MkdirAll(filepath.Join(ws, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name string
		ok   bool
		why  string
	}{
		{"_adhoc/has-folder", true, "its folder is there"},
		{"myrepo/189-some-title", true, "its repo is checked out, which is what a gitlab item looks like"},
		{"typo/not-real", false, "no folder, no session, and no repo called typo"},
		{"_adhoc/never-made", false, "_adhoc has no repo to vouch for it"},
		{"", false, "an empty name resolves to the vault itself"},
		{"../escape", false, "it leaves the vault"},
		{"../../escape", false, "it leaves the vault"},
	} {
		err := c.RequireItem(Item{Name: tc.name})
		if tc.ok && err != nil {
			t.Errorf("RequireItem(%q) refused it (%s): %v", tc.name, tc.why, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("RequireItem(%q) allowed it; %s", tc.name, tc.why)
		}
	}

	// A remote workspace is the far side's to judge: nothing about the item is knowable here.
	remote := Config{Location: Location{Host: "eldo", Path: "~/w"}, Vault: "working_items"}
	if err := remote.RequireItem(Item{Name: "anything/at-all"}); err != nil {
		t.Errorf("a remote workspace should defer to the far side, got: %v", err)
	}
}

func TestMakeItemDir(t *testing.T) {
	ws := t.TempDir()
	c := Config{Workspace: ws, Vault: "working_items"}
	it := Item{Name: "myrepo/189-some-title"}

	if err := c.makeItemDir(it); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(c.ItemDir(it.Name), "notes.md")
	body, err := os.ReadFile(notes)
	if err != nil {
		t.Fatalf("notes stub not written: %v", err)
	}
	if want := "# 189-some-title\n\n"; string(body) != want {
		t.Errorf("notes = %q, want %q", body, want)
	}

	// Called again on an item that exists, which is what re-opening one does. It must not clobber
	// notes someone has since written.
	if err := os.WriteFile(notes, []byte("# mine\n\nreal work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.makeItemDir(it); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(notes); string(body) != "# mine\n\nreal work\n" {
		t.Errorf("existing notes were overwritten: %q", body)
	}
}

// The user config holds workspaces: and hosts:, so falling back to defaults on a parse error made
// every workspace and machine vanish and left `wisp -w demo` saying "known:" with nothing after it.
func TestLoadReportsABrokenUserConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("WISP_WORKSPACE", dir) // so a good config resolves without searching the real disk
	if err := os.MkdirAll(filepath.Join(dir, "wisp"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wisp", "config.yaml")

	if err := os.WriteFile(path, []byte("workspaces:\n  demo: /tmp\nhosts: [eldo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err == nil {
		t.Error("a malformed user config loaded without complaint")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("error does not name the file to fix: %v", err)
	}

	// A remote workspace written in long form with no path is the same class of mistake, and was
	// swallowed by the same discarded error.
	if err := os.WriteFile(path, []byte("workspaces:\n  bad: {host: bigbox}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err == nil {
		t.Error("a workspace with no path loaded without complaint")
	}

	// A valid config still loads, and so does no config at all.
	if err := os.WriteFile(path, []byte("workspaces:\n  demo: /tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err != nil {
		t.Errorf("valid config failed to load: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err != nil {
		t.Errorf("absent config should be fine, got: %v", err)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Fix the thing":                    "fix-the-thing",
		"Fix the thing that broke on prod": "fix-the-thing-that", // four words, no more
		"  spaced  out  ":                  "spaced-out",
		"CVE-2026-1234":                    "cve-2026-1234",
		"":                                 "",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestItemParts(t *testing.T) {
	for _, tc := range []struct{ name, key, repo, slug string }{
		{"wisp/42-fix-the-thing", "wisp/42", "wisp", "42-fix-the-thing"},
		{"_adhoc/wisp", "_adhoc/wisp", "", "wisp"},
		{"wisp/no-iid-here", "wisp/no-iid-here", "wisp", "no-iid-here"},
		{"bare", "bare", "", "bare"},
	} {
		it := Item{Name: tc.name}
		if got := it.Key(); got != tc.key {
			t.Errorf("%q.Key() = %q, want %q", tc.name, got, tc.key)
		}
		if got := it.Repo(); got != tc.repo {
			t.Errorf("%q.Repo() = %q, want %q", tc.name, got, tc.repo)
		}
		if got := it.Slug(); got != tc.slug {
			t.Errorf("%q.Slug() = %q, want %q", tc.name, got, tc.slug)
		}
	}
}

// The whole point of Key: the same item under two spellings is one row, keeping the hand-chosen
// local name and the highest state either source reported.
func TestMergeAllPrecedence(t *testing.T) {
	local := []Item{{Name: "wisp/42-my-own-words", State: StateLive}}
	remote := []Item{
		{Name: "wisp/42-fix-the-thing", State: StateRemote, Title: "Fix the thing"},
		{Name: "wisp/99-something-else", State: StateRemote},
	}
	got := MergeAll(local, remote)
	if len(got) != 2 {
		t.Fatalf("merged to %d items, want 2: %+v", len(got), got)
	}
	if got[0].Name != "wisp/42-my-own-words" {
		t.Errorf("first name = %q, want the local spelling to win", got[0].Name)
	}
	if got[0].State != StateLive {
		t.Errorf("first state = %v, want the higher state to win", got[0].State)
	}
	if got[0].Title != "Fix the thing" {
		t.Errorf("first title = %q, want the remote title to fill in", got[0].Title)
	}
}

func TestExtractFrontmatter(t *testing.T) {
	body := "---\nrepos:\n  - repo: wisp\n---\n\n# notes\n"
	fm, err := extractFrontmatter([]byte(body))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if want := "repos:\n  - repo: wisp"; string(fm) != want {
		t.Errorf("frontmatter = %q, want %q", fm, want)
	}
	if got := string(StripFrontmatter([]byte(body))); got != "\n# notes\n" {
		t.Errorf("StripFrontmatter = %q", got)
	}

	// No fence at all is a file with no frontmatter, not a broken one.
	if fm, err := extractFrontmatter([]byte("# notes\n")); err != nil || fm != nil {
		t.Errorf("unfenced file gave (%q, %v), want (nil, nil)", fm, err)
	}
	// An unclosed fence is worth reporting: it is almost always a half-written file.
	if _, err := extractFrontmatter([]byte("---\nrepos: []\n")); err == nil {
		t.Error("unclosed frontmatter should be an error")
	}
}

// step is the ring walk under both `wisp hop` and ctrl-w. Go's % keeps the sign of the dividend,
// so the wrap at the low end is the one that breaks.
func TestStepWraps(t *testing.T) {
	names := []string{"a", "b", "c"}
	for _, tc := range []struct {
		from  string
		delta int
		want  string
	}{
		{"a", 1, "b"},
		{"c", 1, "a"},
		{"a", -1, "c"},
		{"b", -1, "a"},
		{"nowhere", 1, "b"}, // not in the ring: enter at the start and step from there
	} {
		if got := step(names, tc.from, tc.delta); got != tc.want {
			t.Errorf("step(%q, %d) = %q, want %q", tc.from, tc.delta, got, tc.want)
		}
	}
}

func TestSplitQualified(t *testing.T) {
	c := Config{Hosts: HostSet{"eldo": "jade@eldo"}}
	for _, tc := range []struct {
		in         string
		host, ws   string
		wantResolv bool
	}{
		{"eldo", "eldo", "", true},          // a bare machine means its default workspace
		{"eldo/side", "eldo", "side", true}, // the qualified form
		{"near", "", "", false},             // a local workspace
		{"nope/side", "", "", false},        // a machine that is not registered
	} {
		host, ws, ok := c.splitQualified(tc.in)
		if ok != tc.wantResolv || host != tc.host || ws != tc.ws {
			t.Errorf("splitQualified(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, host, ws, ok, tc.host, tc.ws, tc.wantResolv)
		}
	}
}

func TestQualify(t *testing.T) {
	if got := qualify("eldo", "work", true); got != "eldo" {
		t.Errorf("the default workspace should take the bare machine name, got %q", got)
	}
	if got := qualify("eldo", "side", false); got != "eldo/side" {
		t.Errorf("qualify = %q, want eldo/side", got)
	}
}

// Session names are namespaced by workspace, and both halves are sanitized: tmux forbids "." and
// ":" outright, and an underscore in the workspace name would make the boundary ambiguous.
func TestSessionName(t *testing.T) {
	c := Config{Name: "my_ws"}
	if got := c.SessionName("wisp/42-thing"); got != "wisp_my-ws_wisp-42-thing" {
		t.Errorf("SessionName = %q", got)
	}
}

// shellQuote wraps things handed to /bin/sh. The characters that matter are the ones Go's %q
// would have left live.
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      "'plain'",
		"with space": "'with space'",
		`$HOME`:      `'$HOME'`,
		"back`tick":  "'back`tick'",
		`back\slash`: `'back\slash'`,
		"it's":       `'it'\''s'`,
		"two\nlines": "'two\nlines'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectPath(t *testing.T) {
	for in, want := range map[string]string{
		"https://gitlab.com/grp/repo/-/issues/42":            "grp/repo",
		"https://gitlab.com/grp/sub/deep/repo/-/issues/42":   "grp/sub/deep/repo",
		"https://gitlab.com/grp/repo/-/merge_requests/7":     "grp/repo",
		"https://gitlab.com/grp/repo/-/work_items/9":         "grp/repo",
		"http://gitlab.internal/grp/repo/-/issues/1":         "grp/repo",
		"https://gitlab.com/grp/repo/-/issues/42#note_12345": "grp/repo",
		// A group-level epic has no project to belong to, and neither does a bare repo link.
		"https://gitlab.com/groups/grp/-/epics/3": "",
		"https://gitlab.com/grp/repo":             "",
		"not a url":                               "",
	} {
		if got := projectPath(in); got != want {
			t.Errorf("projectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The kind decides which field is asked for: a merge request is not a work item in GitLab's
// schema, so asking workItems for one returns nothing at all.
func TestItemQueryFollowsKind(t *testing.T) {
	mr := itemQuery("grp/repo", "merge_requests", "7")
	if !strings.Contains(mr, "mergeRequest(iid: \"7\")") {
		t.Errorf("merge request query does not ask for mergeRequest: %s", mr)
	}
	for _, kind := range []string{"issues", "work_items"} {
		q := itemQuery("grp/repo", kind, "42")
		if !strings.Contains(q, "workItems(iid: \"42\")") {
			t.Errorf("%s query does not ask for workItems: %s", kind, q)
		}
		if strings.Contains(q, "mergeRequest") {
			t.Errorf("%s query asks for a merge request: %s", kind, q)
		}
	}
	// The path is quoted rather than interpolated raw, so a name with a quote in it cannot end
	// the string early and change the query.
	if q := itemQuery(`grp/re"po`, "issues", "1"); !strings.Contains(q, `\"`) {
		t.Errorf("project path is not quoted: %s", q)
	}
}

func TestTitleFromResponse(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"work item":     {`{"data":{"project":{"workItems":{"nodes":[{"title":"fix the thing"}]}}}}`, "fix the thing"},
		"merge request": {`{"data":{"project":{"mergeRequest":{"title":"bump deps"}}}}`, "bump deps"},
		// Not there, or not visible to this token. Distinct from an unparseable response, and
		// the caller turns it into a message that names both possibilities.
		"missing item": {`{"data":{"project":{"workItems":{"nodes":[]}}}}`, ""},
		"no project":   {`{"data":{"project":null}}`, ""},
	} {
		got, err := titleFromResponse([]byte(tc.raw))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
	if _, err := titleFromResponse([]byte("not json")); err == nil {
		t.Error("unparseable response did not error")
	}
}

// The point of the fix: a title lookup must not depend on being the assignee. The cache only ever
// holds assigned items, so a miss has to fall through to a lookup rather than refuse.
func TestCachedTitleMatchesOnProjectNotJustNumber(t *testing.T) {
	dir := t.TempDir()
	c := Config{Workspace: dir, Vault: "working_items", Name: "t"}
	body := `{"data":{"group":{"workItems":{"nodes":[
		{"iid":"42","title":"mine in repo-a","webUrl":"https://gitlab.com/grp/repo-a/-/issues/42"}
	]}}}}`
	if err := os.WriteFile(c.CachePath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.cachedTitle("grp/repo-a", "42"); got != "mine in repo-a" {
		t.Errorf("own project: got %q, want %q", got, "mine in repo-a")
	}
	// Same number, different project. Numbering is per-project, so this must miss rather than
	// hand back a title belonging to entirely different work.
	if got := c.cachedTitle("grp/repo-b", "42"); got != "" {
		t.Errorf("other project: got %q, want \"\"", got)
	}
}

// Marking an item done must not disturb the rest of its note. The frontmatter belongs to whoever
// writes it and can hold anything their editor puts there.
func TestSetDoneInPreservesTheNote(t *testing.T) {
	note := "---\ntags:\n    - review\naliases:\n    - the thing\n---\n\n# my item\n\nsome prose.\n"
	out, err := setDoneIn([]byte(note), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tags:", "review", "aliases:", "the thing", "# my item", "some prose."} {
		if !strings.Contains(string(out), want) {
			t.Errorf("setDoneIn dropped %q:\n%s", want, out)
		}
	}
	if !doneIn(out) {
		t.Errorf("setDoneIn did not mark it done:\n%s", out)
	}
	// And back again, still without losing anything.
	back, err := setDoneIn(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if doneIn(back) {
		t.Errorf("undo did not clear the flag:\n%s", back)
	}
	if !strings.Contains(string(back), "some prose.") || !strings.Contains(string(back), "review") {
		t.Errorf("undo lost part of the note:\n%s", back)
	}
}

// A note with no frontmatter at all is the common case: makeItemDir writes a bare heading.
func TestSetDoneInAddsFrontmatterWhenThereIsNone(t *testing.T) {
	out, err := setDoneIn([]byte("# my item\n\nprose.\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !doneIn(out) {
		t.Errorf("not marked done:\n%s", out)
	}
	if !strings.Contains(string(out), "# my item") {
		t.Errorf("body lost:\n%s", out)
	}
	// Reopening something never closed must not grow a frontmatter block for a false.
	same, err := setDoneIn([]byte("# my item\n\nprose.\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(same), "---") {
		t.Errorf("undo on a plain note added frontmatter:\n%s", same)
	}
}

// The rule the picker turns on: closed out means hidden, unless something is running under that
// name. Hiding a live session would leave an agent on the machine with nothing pointing at it.
func TestHideDoneKeepsLiveSessions(t *testing.T) {
	items := []Item{
		{Name: "repo/1-open", State: StateFolder},
		{Name: "repo/2-closed", State: StateFolder, Done: true},
		{Name: "repo/3-closed-but-running", State: StateLive, Done: true},
		{Name: "repo/4-closed-and-waiting", State: StateNeedsInput, Done: true},
		{Name: "repo/5-closed-on-gitlab", State: StateRemote, Done: true},
	}
	var got []string
	for _, it := range HideDone(items) {
		got = append(got, it.Name)
	}
	want := []string{"repo/1-open", "repo/3-closed-but-running", "repo/4-closed-and-waiting"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("HideDone = %v, want %v", got, want)
	}
}

// Done is a separate axis from State, so it must survive a merge from a source that cannot know
// it. Only the vault row reads the note; a session or a GitLab row arriving second carries false.
func TestMergeKeepsDoneFromWhicheverSourceKnew(t *testing.T) {
	sessions := []Item{{Name: "repo/1-thing", State: StateLive}}
	vault := []Item{{Name: "repo/1-thing", State: StateFolder, Done: true}}
	gitlab := []Item{{Name: "repo/1-different-slug", State: StateRemote}}
	merged := MergeAll(sessions, vault, gitlab)
	if len(merged) != 1 {
		t.Fatalf("got %d items, want 1: %v", len(merged), merged)
	}
	if !merged[0].Done {
		t.Error("done was lost merging a session and a gitlab row over the vault")
	}
	if merged[0].State != StateLive {
		t.Errorf("state = %v, want live", merged[0].State)
	}
}

// End to end over a real vault: the flag is written, read back through LocalItems, and survives
// the merge that Items does.
func TestItemDoneRoundTripsThroughTheVault(t *testing.T) {
	dir := t.TempDir()
	c := Config{Workspace: dir, Vault: "working_items", Name: "t"}
	for _, name := range []string{"repo/1-one", "repo/2-two"} {
		if err := c.makeItemDir(Item{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if c.ItemDone("repo/1-one") {
		t.Error("a fresh item is already done")
	}
	if err := c.SetDone("repo/1-one", true); err != nil {
		t.Fatal(err)
	}
	if !c.ItemDone("repo/1-one") {
		t.Error("SetDone did not stick")
	}
	if c.ItemDone("repo/2-two") {
		t.Error("SetDone marked the wrong item")
	}
	items, err := c.LocalItems()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if want := it.Name == "repo/1-one"; it.Done != want {
			t.Errorf("LocalItems: %s done = %v, want %v", it.Name, it.Done, want)
		}
	}
	if n := len(HideDone(items)); n != 1 {
		t.Errorf("HideDone left %d of 2 items, want 1", n)
	}
	// An item with no note at all must not read as done, or a folder made by hand would vanish.
	if err := os.Remove(c.NotesPath("repo/2-two")); err != nil {
		t.Fatal(err)
	}
	if c.ItemDone("repo/2-two") {
		t.Error("an item with no notes.md reads as done")
	}
}

// The gap this closes: the flag was one keystroke and the write-up was a trip to an editor, so
// every item carrying the flag had a note holding nothing but the flag.
func TestNoteIsEmptySeesPastTheStub(t *testing.T) {
	dir := t.TempDir()
	c := Config{Workspace: dir, Vault: "working_items", Name: "t"}
	if err := c.makeItemDir(Item{Name: "repo/1-thing"}); err != nil {
		t.Fatal(err)
	}
	// makeItemDir writes "# <slug>\n\n" and nothing else. That is wisp's writing, not yours.
	if !c.NoteIsEmpty("repo/1-thing") {
		t.Error("a fresh item reads as written up")
	}
	if !c.NoteIsEmpty("repo/never-made") {
		t.Error("a missing note reads as written up")
	}
	for name, raw := range map[string]string{
		"stub":             "# thing\n\n",
		"stub with matter": "---\ndone: true\n---\n\n# thing\n\n",
		"blank":            "",
		"matter only":      "---\ntags:\n    - x\n---\n",
	} {
		if hasBody([]byte(raw)) {
			t.Errorf("%s: reads as written up:\n%s", name, raw)
		}
	}
	for name, raw := range map[string]string{
		"prose":          "# thing\n\nit turned out to be a caching bug.\n",
		"second heading": "# thing\n\n## what happened\n",
		"no stub at all": "some loose prose\n",
		"under matter":   "---\ndone: true\n---\n\n# thing\n\nfixed it.\n",
	} {
		if !hasBody([]byte(raw)) {
			t.Errorf("%s: reads as empty:\n%s", name, raw)
		}
	}
}

// Closing out with a line has to set the flag and write the note in one pass, and must not
// disturb frontmatter anyone else put there.
func TestCloseOutWritesTheLineAndTheFlag(t *testing.T) {
	dir := t.TempDir()
	c := Config{Workspace: dir, Vault: "working_items", Name: "t"}
	if err := c.makeItemDir(Item{Name: "repo/1-thing"}); err != nil {
		t.Fatal(err)
	}
	note := c.NotesPath("repo/1-thing")
	if err := os.WriteFile(note, []byte("---\ntags:\n    - review\n---\n\n# 1-thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseOut("repo/1-thing", true, "  it was a caching bug in the router.  "); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !c.ItemDone("repo/1-thing") {
		t.Errorf("flag not set:\n%s", got)
	}
	if !strings.Contains(got, "it was a caching bug in the router.") {
		t.Errorf("line not written:\n%s", got)
	}
	if !strings.Contains(got, "## Closed out ") {
		t.Errorf("no dated heading:\n%s", got)
	}
	for _, want := range []string{"tags:", "review", "# 1-thing"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	// And the item now reads as written up, so a second close does not ask again.
	if c.NoteIsEmpty("repo/1-thing") {
		t.Errorf("still reads as empty after being written up:\n%s", got)
	}
}

// Closing out bare stays possible: some work has nothing to say about it, and refusing outright
// would only teach people to stop closing things.
func TestCloseOutBareLeavesTheNoteAlone(t *testing.T) {
	dir := t.TempDir()
	c := Config{Workspace: dir, Vault: "working_items", Name: "t"}
	if err := c.makeItemDir(Item{Name: "_adhoc/thing"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseOut("_adhoc/thing", true, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(c.NotesPath("_adhoc/thing"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.ItemDone("_adhoc/thing") {
		t.Errorf("flag not set:\n%s", raw)
	}
	if strings.Contains(string(raw), "Closed out") {
		t.Errorf("bare close invented a heading:\n%s", raw)
	}
}
