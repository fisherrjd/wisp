package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fisherrjd/wisp/internal/wisp"
)

// sourceCounting builds a workspace whose source hook records every run, and returns the config
// and the tally file. The hook is named in the user config rather than in the workspace's
// .wisp.yaml because that layer is yours by definition and needs no acceptance, which keeps this
// test about how often the hook runs rather than about trust.
func sourceCounting(t *testing.T, body string) (wisp.Config, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WISP_PROGRAM", "")
	ws := t.TempDir()
	tally := filepath.Join(ws, "runs")

	// Outside the workspace on purpose. A hook script inside a workspace is workspace-supplied and
	// has to be accepted by its own content whoever named it, so one written there would be
	// stripped and this would be a test of the trust gate rather than of the refresh count. A hook
	// of your own, named by your own config, living somewhere that is yours, is the ordinary case.
	hook := filepath.Join(t.TempDir(), "source.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho run >>"+tally+"\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := wisp.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("source: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	zero := 0
	return wisp.Config{
		Workspace:   ws,
		Vault:       "working_items",
		Worktrees:   ".worktrees",
		Name:        "test",
		Accepted:    map[string]string{},
		CacheTTLMin: &zero, // "ask on every load", which is where the double call was worst
	}, tally
}

func hookRuns(t *testing.T, tally string) int {
	t.Helper()
	raw, err := os.ReadFile(tally)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(raw)))
}

// ctrl-r is one refresh, so it is one run of the source hook. This used to refresh and then read
// as two calls, and the read went through the ordinary TTL check: a failed refresh leaves the
// cache file's mtime alone, so the read found the cache stale and ran the hook a second time. A
// hook is allowed sixty seconds, so one keypress could stall for two minutes against docs that
// promise one, and `cache_ttl_min: 0` did it on every press whether the source worked or not.
// The assertion is a count for that reason: the rows can be right while the keypress costs double.
func TestRefreshAsksTheSourceOnce(t *testing.T) {
	cfg, tally := sourceCounting(t, `echo '{"name":"repo/7-warm","title":"warm"}'`+"\n")

	msg, ok := loadRemote(cfg, true)().(remoteMsg)
	if !ok {
		t.Fatal("loadRemote did not answer with a remoteMsg")
	}
	if msg.err != nil {
		t.Fatalf("a working source failed the refresh: %v", msg.err)
	}
	if n := hookRuns(t, tally); n != 1 {
		t.Errorf("one ctrl-r ran the source %d times, want exactly 1", n)
	}
	if len(msg.items) != 1 || msg.items[0].Name != "repo/7-warm" {
		t.Errorf("the refreshed rows are wrong: %+v", msg.items)
	}
}

// The failing source is the case that doubled, and it is also the case that must keep its reason:
// the status line is the only place a broken source is visible, so a fix that dropped the refresh
// error to collapse the two calls would trade one silence for another.
func TestRefreshOnABrokenSourceStillAsksOnceAndSaysWhy(t *testing.T) {
	cfg, tally := sourceCounting(t, "echo 'tracker is down' >&2\nexit 1\n")

	msg, ok := loadRemote(cfg, true)().(remoteMsg)
	if !ok {
		t.Fatal("loadRemote did not answer with a remoteMsg")
	}
	if n := hookRuns(t, tally); n != 1 {
		t.Errorf("one ctrl-r against a broken source ran it %d times, want exactly 1", n)
	}
	if msg.err == nil {
		t.Fatal("a broken source reported no error, so the status line would say nothing")
	}
	// And what it says has to be the source's own words, not a generic failure.
	if !strings.Contains(msg.err.Error(), "tracker is down") {
		t.Errorf("the reason was lost on the way to the status line: %v", msg.err)
	}
}

// peers builds a tree with a local machine and one remote, which is the shape that exposed the
// index bug: the rows interleave a header per machine, so row numbers and peer numbers diverge
// by one per machine above the cursor.
func peers() []wisp.Peer {
	return []wisp.Peer{
		{Name: "near", Workspace: "near", Current: true, Ready: true, Path: "/near"},
		{Name: "work", Workspace: "work", Ready: true, Path: "/work"},
		{Name: "eldo", System: "eldo", Workspace: "main", Ready: true, Path: "eldo:/main"},
		{Name: "eldo/side", System: "eldo", Workspace: "side", Ready: true, Path: "eldo:/side"},
	}
}

// The detail pane must describe the row the cursor is on. It used to index the peer list with a
// row number, which named the wrong workspace on every row and ran off the end on the last one.
func TestPeerFollowsTheCursor(t *testing.T) {
	m := model{peers: peers(), width: 80, height: 40}
	rows := m.wsRows()
	if len(rows) != len(m.peers)+2 {
		t.Fatalf("expected one header per machine, got %d rows for %d peers", len(rows), len(m.peers))
	}
	for i, r := range rows {
		m.wsCursor = i
		p := m.peer()
		if p == nil {
			t.Errorf("row %d has no workspace; every row resolves to one", i)
			continue
		}
		want := ""
		switch {
		case r.Peer != nil:
			want = r.Peer.Name
		case len(r.Head.Peers) > 0:
			// A machine stands for its default workspace, which is also where enter goes.
			want = r.Head.Peers[0].Name
		}
		if p.Name != want {
			t.Errorf("row %d: peer() = %q, want %q", i, p.Name, want)
		}
		// The detail pane and enter must agree, or the pane describes one workspace while enter
		// goes to another.
		if target := m.target(rows); target == nil || target.Name != p.Name {
			t.Errorf("row %d: peer() and target() disagree", i)
		}
	}
}

// wsCursor indexes rows, not peers. Clamping it against the peer count dragged a cursor parked
// on a late row backwards every time the board reloaded.
func TestBoardReloadKeepsTheCursor(t *testing.T) {
	m := model{peers: peers(), width: 80, height: 40}
	last := len(m.wsRows()) - 1
	m.wsCursor = last

	next, _ := m.Update(candidatesMsg{board: wisp.Board{Peers: peers()}})
	if got := next.(model).wsCursor; got != last {
		t.Errorf("cursor moved from %d to %d on reload", last, got)
	}
}

// The tree is windowed like the item list. Rendering every row made the pane taller than the
// terminal and pushed the footer off the bottom.
func TestWorkspaceListFitsTheTerminal(t *testing.T) {
	var many []wisp.Peer
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		many = append(many, wisp.Peer{Name: n, Workspace: n, Ready: true, Path: "/" + n})
	}
	m := model{peers: many, mode: modeWorkspace, width: 80, height: 8}
	m.wsCursor = len(m.wsRows()) - 1
	m.scrollWorkspaces()

	rows := m.listRows()
	got := strings.Count(m.renderWorkspaces(rows), "\n") + 1
	if got > rows {
		t.Errorf("rendered %d lines into a %d-line pane", got, rows)
	}
	// The cursor has to be inside the window, or it is invisible and the pane looks frozen.
	if m.wsCursor < m.wsOffset || m.wsCursor >= m.wsOffset+rows {
		t.Errorf("cursor %d outside the window [%d, %d)", m.wsCursor, m.wsOffset, m.wsOffset+rows)
	}
}

// Scrolling back to the top must land on the top, not on a window left hanging past the end.
func TestWorkspaceScrollBounds(t *testing.T) {
	m := model{peers: peers(), mode: modeWorkspace, width: 80, height: 8}
	m.wsCursor = len(m.wsRows()) - 1
	m.scrollWorkspaces()
	m.wsCursor = 0
	m.scrollWorkspaces()
	if m.wsOffset != 0 {
		t.Errorf("offset = %d at the top of the list, want 0", m.wsOffset)
	}
}

// x on this machine's header has nothing to drop: the machine wisp is running on is not an entry
// anybody added. It used to return silently, which reads as a broken binding when the footer
// offers "x forget" on every row alike.
func TestForgetOnTheLocalMachineSaysWhy(t *testing.T) {
	m := model{peers: peers(), mode: modeWorkspace, width: 80, height: 20}
	rows := m.wsRows()
	if rows[0].Head == nil || rows[0].Head.Name != "" {
		t.Fatalf("expected row 0 to be this machine's header, got %+v", rows[0])
	}

	m.wsCursor = 0
	next, _ := m.updateWorkspace(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	status := next.(model).status
	if status == "" {
		t.Error("x on this machine's header said nothing at all")
	}
	if !strings.Contains(status, "not something to forget") {
		t.Errorf("status = %q, want it to explain that this machine is not forgettable", status)
	}
	// And it must not have dropped anything on the way.
	if m.forgetTarget(rows) != "" {
		t.Error("this machine resolved to a name to forget")
	}

	// A machine you added is still forgettable from its header row, which is the only entry for
	// it: the workspaces underneath were never registered here.
	m.wsCursor = 3
	if rows[3].Head == nil {
		t.Fatalf("expected row 3 to be a machine header, got %+v", rows[3])
	}
	if got := m.forgetTarget(rows); got != "eldo" {
		t.Errorf("forgetTarget on the eldo header = %q, want eldo", got)
	}
}

func TestParseWorkspaceLine(t *testing.T) {
	for _, tc := range []struct {
		in         string
		name, path string
		mkdir      bool
	}{
		{"side ~/side", "side", "~/side", false},
		{"-p side ~/side", "side", "~/side", true},
		{"side ~/side -p", "side", "~/side", true}, // the flag reads as naturally at the end
		{"side", "side", "", false},
		{"", "", "", false},
	} {
		name, path, mkdir := parseWorkspaceLine(tc.in)
		if name != tc.name || path != tc.path || mkdir != tc.mkdir {
			t.Errorf("parseWorkspaceLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, name, path, mkdir, tc.name, tc.path, tc.mkdir)
		}
	}
}

// Tabs are the case that matters: lipgloss counts one cell, the terminal renders up to eight,
// so a tabbed notes line passed the truncation check and then wrapped the layout anyway.
func TestSanitizeLine(t *testing.T) {
	if got := sanitizeLine("a\tb"); got != "a    b" {
		t.Errorf("tab not expanded: %q", got)
	}
	if got := sanitizeLine("a\x1b[31mb\x07"); got != "a[31mb" {
		t.Errorf("control characters survived: %q", got)
	}
}

// ctrl-t is the only way back from a mistaken ctrl-d, so it has to be on the footer the moment
// there is something to come back to. It shipped missing from it once, discoverable only from
// the status line ctrl-d prints one time.
//
// The rest of the footer is conditional now, which is exactly the change that could lose this
// again: ctrl-t is the one hint that cannot wait for the user to already know about it.
func TestFooterOffersTheWayBackFromDone(t *testing.T) {
	one := []wisp.Item{{Name: "repo/1-a", State: wisp.StateFolder}}

	nothingClosed := model{filtered: one}.itemKeys()
	if strings.Contains(strings.Join(nothingClosed, "  "), "ctrl-t") {
		t.Errorf("ctrl-t offered with nothing closed out: %v", nothingClosed)
	}

	hidden := strings.Join(model{filtered: one, hasDone: true}.itemKeys(), "  ")
	shown := strings.Join(model{filtered: one, hasDone: true, showDone: true}.itemKeys(), "  ")
	if !strings.Contains(hidden, "ctrl-t") || !strings.Contains(shown, "ctrl-t") {
		t.Fatalf("ctrl-t missing from the footer:\n  %s\n  %s", hidden, shown)
	}
	// Worded for what the next press does, not for the state the list is in.
	if !strings.Contains(hidden, "show") || !strings.Contains(shown, "hide") {
		t.Errorf("ctrl-t does not follow the toggle:\n  hidden: %s\n  shown:  %s", hidden, shown)
	}
}

// Every binding has to be findable without reading the source. The footer carries the verbs and
// ctrl-g carries the rest, so what matters is the union: a key in neither is unreachable.
func TestEveryBindingIsDiscoverable(t *testing.T) {
	m := model{
		filtered: []wisp.Item{{Name: "repo/1-a", State: wisp.StateLive}},
		hasDone:  true,
		peers:    peers(),
		width:    120,
		height:   40,
	}
	page := ansi.ReplaceAllString(m.renderHelp(m.listRows()), "")
	found := strings.Join(m.itemKeys(), "  ") + "\n" + page

	for _, key := range []string{"enter", "ctrl-n", "ctrl-d", "ctrl-t", "ctrl-x", "ctrl-w",
		"ctrl-r", "ctrl-u", "ctrl-g", "esc"} {
		if !strings.Contains(found, key) {
			t.Errorf("%q is bound but appears in neither the footer nor the key page", key)
		}
	}

	// ctrl-x and ctrl-r were taken off the footer deliberately, to stop the bar being a
	// manifest. Taken off it and not put anywhere is a different thing, and the failure would
	// otherwise be silent.
	for _, key := range []string{"ctrl-x", "ctrl-w", "ctrl-r"} {
		if !strings.Contains(page, key) {
			t.Errorf("%q left the footer without landing on the key page", key)
		}
	}
}

// The point of the change: a plain list on an ordinary machine is a short footer. One
// workspace, no GitLab, nothing closed out, and the cursor on a folder means four hints, not
// eight, and no second row stolen from the panes.
func TestFooterIsShortWhenLittleApplies(t *testing.T) {
	quiet := model{filtered: []wisp.Item{{Name: "repo/1-a", State: wisp.StateFolder}}}
	got := quiet.itemKeys()
	want := []string{"enter open", "ctrl-n new", "ctrl-d done", "ctrl-g keys", "esc quit"}
	if strings.Join(got, "  ") != strings.Join(want, "  ") {
		t.Errorf("quiet footer:\n  got  %v\n  want %v", got, want)
	}

	// And an empty list drops the two that act on a row there is none of.
	empty := model{}.itemKeys()
	if strings.Join(empty, "  ") != "ctrl-n new  ctrl-g keys  esc quit" {
		t.Errorf("empty list footer: %v", empty)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// ctrl+g opens the key list and esc closes it. esc quits from the list itself, so the one thing
// this must not do is fall through to that: reading the bindings should never end the session.
func TestHelpOpensAndCloses(t *testing.T) {
	m := model{width: 100, height: 30}

	opened, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if got := opened.(model).mode; got != modeHelp {
		t.Fatalf("ctrl+g did not open the key list: mode %d", got)
	}

	closed, cmd := opened.(model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := closed.(model).mode; got != modeFilter {
		t.Errorf("esc did not close the key list: mode %d", got)
	}
	if cmd != nil {
		t.Error("esc quit the picker from inside the key list")
	}
}

// The footer only lists what applies right now, so the key list is where the rest has to live.
// A binding in neither is one nobody can find.
func TestHelpDocumentsWhatTheFooterOmits(t *testing.T) {
	cfg := wisp.Config{GitLab: wisp.GitLab{Group: "g", Username: "u", RepoPattern: "p"}}
	m := model{
		cfg:      cfg,
		filtered: []wisp.Item{{Name: "repo/1-a", State: wisp.StateLive}},
		hasDone:  true,
		peers:    peers(),
		width:    120,
		height:   40,
	}
	page := ansi.ReplaceAllString(m.renderHelp(m.listRows()), "")

	for _, hint := range m.itemKeys() {
		key := strings.Fields(hint)[0] // "ctrl-t show closed" -> "ctrl-t"
		if !strings.Contains(page, key) {
			t.Errorf("the key list does not mention %q, so it exists only in the footer", key)
		}
	}
	// The tree's plain letters have no footer outside the tree, which is the gap this page was
	// added to close. Matched on what they do, since single letters match anything.
	for _, does := range []string{"new workspace on this machine", "add a machine", "forget it"} {
		if !strings.Contains(page, does) {
			t.Errorf("the key list does not describe %q", does)
		}
	}
}


// A workflow that says where bare names land has answered the question the repo step asks, so
// the step is skipped and the item is made where the workflow said.
func TestParentSkipsTheRepoQuestion(t *testing.T) {
	build := func(userCfg string) wisp.Config {
		t.Helper()
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("WISP_PROGRAM", "")
		ws := t.TempDir()
		for _, r := range []string{"alpha", "beta"} {
			if err := os.MkdirAll(filepath.Join(ws, r, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cfgPath := wisp.UserConfigPath()
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte(userCfg), 0o644); err != nil {
			t.Fatal(err)
		}
		return wisp.Config{Workspace: ws, Vault: "working_items", Worktrees: ".worktrees", Name: "test", Accepted: map[string]string{}}
	}
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	m := model{cfg: build(""), mode: modeNew, input: "loose", width: 100, height: 30}
	next, _ := m.Update(enter)
	if got := next.(model).mode; got != modeNewRepo {
		t.Fatalf("with two repos and no parent, enter should ask which: mode %d", got)
	}

	m = model{cfg: build("item:\n  parent: _adhoc\n"), mode: modeNew, input: "loose", width: 100, height: 30}
	next, cmd := m.Update(enter)
	nm := next.(model)
	if nm.mode == modeNewRepo {
		t.Fatal("item.parent is set, so there is nothing to ask")
	}
	if nm.chosen == nil || nm.chosen.Name != "_adhoc/loose" || cmd == nil {
		t.Errorf("chosen = %v, want _adhoc/loose and a quit", nm.chosen)
	}
}

// unboundWorkspace is a workspace with no .wisp.yaml and an empty user config: nothing names a
// workflow, so the built-in runs by default rather than by choice.
func unboundWorkspace(t *testing.T) wisp.Config {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WISP_PROGRAM", "")
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "working_items"), 0o755); err != nil {
		t.Fatal(err)
	}
	return wisp.Config{Workspace: ws, Vault: "working_items", Worktrees: ".worktrees", Name: "test", Accepted: map[string]string{}}
}

// stubAsked replaces the tmux-backed memory of "later" with a bool for the test's lifetime.
func stubAsked(t *testing.T) *bool {
	t.Helper()
	asked := false
	origAsked, origMark := wisp.WorkflowAsked, wisp.MarkWorkflowAsked
	wisp.WorkflowAsked = func(string) bool { return asked }
	wisp.MarkWorkflowAsked = func(string) { asked = true }
	t.Cleanup(func() { wisp.WorkflowAsked, wisp.MarkWorkflowAsked = origAsked, origMark })
	return &asked
}

func TestUnboundWorkspaceAsksOnceAndBinds(t *testing.T) {
	cfg := unboundWorkspace(t)
	stubAsked(t)

	m := model{cfg: cfg, width: 120, height: 30}
	next, _ := m.Update(candidatesMsg{})
	nm := next.(model)
	if nm.mode != modeBindWorkflow {
		t.Fatalf("the first paint of an unbound workspace should ask: mode %d", nm.mode)
	}
	if len(nm.wfChoices) < 2 || nm.wfChoices[0].Addr != "default" || nm.wfChoices[len(nm.wfChoices)-1].Addr != "later" {
		t.Fatalf("choices should start at the built-in and end at later: %+v", nm.wfChoices)
	}

	bound, _ := nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	bm := bound.(model)
	if bm.mode != modeFilter {
		t.Errorf("after binding the picker should be back on the list: mode %d", bm.mode)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.Workspace, ".wisp.yaml"))
	if err != nil || !strings.Contains(string(raw), "workflow: default") {
		t.Errorf("binding should write the choice into .wisp.yaml, literally: %q %v", raw, err)
	}

	// Bound now, so a fresh picker does not ask.
	again, _ := (model{cfg: cfg, width: 120, height: 30}).Update(candidatesMsg{})
	if got := again.(model).mode; got != modeFilter {
		t.Errorf("a bound workspace was asked again: mode %d", got)
	}
}

func TestLaterIsRememberedForTheServer(t *testing.T) {
	cfg := unboundWorkspace(t)
	asked := stubAsked(t)

	next, _ := (model{cfg: cfg, width: 120, height: 30}).Update(candidatesMsg{})
	deferred, _ := next.(model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := deferred.(model).mode; got != modeFilter {
		t.Errorf("esc should leave the list running the built-in: mode %d", got)
	}
	if !*asked {
		t.Error("later was not recorded")
	}
	if exists := fileExists(filepath.Join(cfg.Workspace, ".wisp.yaml")); exists {
		t.Error("later must write nothing")
	}

	again, _ := (model{cfg: cfg, width: 120, height: 30}).Update(candidatesMsg{})
	if got := again.(model).mode; got != modeFilter {
		t.Errorf("asked again after later: mode %d", got)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
