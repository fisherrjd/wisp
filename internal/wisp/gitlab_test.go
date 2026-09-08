package wisp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runsOf counts how many times a script that appends a line per invocation has been called. A
// missing file is nought rather than a failure: a hook that never ran wrote nothing.
func runsOf(t *testing.T, tally string) int {
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

// A refresh that reports success and leaves nothing readable behind is a failure, and has to say
// so. cached() used to return refreshErr whatever the read did, so with a nil refreshErr the
// caller got (nil, nil): an empty list and no reason at all, which is the "a broken source looks
// exactly like a quiet one" state the rest of this file exists to prevent, reached from the one
// direction nobody was watching. Delete this and that silence comes back, and it comes back in
// the picker as a `+` section that is empty for no stated reason.
func TestASuccessfulRefreshWithNothingToReadIsStillAnError(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	zero := 0
	c.CacheTTLMin = &zero // nothing is ever younger than a zero TTL, so the refresh always runs
	path := filepath.Join(t.TempDir(), "never-written.json")

	raw, err := c.cached(path, func() error { return nil }, false)
	if err == nil {
		t.Error("a refresh that wrote no cache reported no error, so nothing would annotate the list")
	}
	if raw != nil {
		t.Errorf("got %q back from a cache that was never written", raw)
	}

	// The refresh error still wins when there is one: it is the cause, and an unreadable cache is
	// only its symptom, so the message has to name the thing that actually broke.
	_, err = c.cached(path, func() error { return os.ErrPermission }, false)
	if err == nil || !strings.Contains(err.Error(), os.ErrPermission.Error()) {
		t.Errorf("the read error displaced the refresh error: %v", err)
	}
}

// The reason has to survive the callers, or it is lost one level up and the status line shows
// nothing. GitLabItems drops the rows when the cache came back empty, and that branch has to carry
// the error out with it rather than returning the nil it used to get handed.
func TestAnUnreadableCacheReachesTheCaller(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	zero := 0
	c.CacheTTLMin = &zero
	// A configured gitlab source with no glab on PATH: the refresh fails, there is no cache to
	// fall back on, and the picker has to be told which of those happened.
	c.GitLab = GitLab{Group: "g", Username: "u", RepoPattern: `/([^/]+)/-/`}
	t.Setenv("PATH", t.TempDir())

	items, err := c.RemoteItems()
	if err == nil {
		t.Error("an unusable gitlab source returned no error, so the list would empty silently")
	}
	if len(items) != 0 {
		t.Errorf("got %d items from a source that never answered: %+v", len(items), items)
	}
}

// One ctrl-r, one run of the hook. The picker used to refresh and then read as two calls, and the
// read went through the ordinary TTL check: a failed refresh leaves the cache file's mtime alone,
// so the read found the cache stale and asked again. A hook is allowed sixty seconds, so a single
// keypress could stall for two minutes. With `cache_ttl_min: 0`, which means "ask on every load",
// it happened on every press whether the source worked or not. This test is a count for that
// reason: the rows can be right while the cost is double.
func TestAForcedRefreshAsksTheSourceOnce(t *testing.T) {
	c := hookWorkspace(t, "repo/1-thing")
	zero := 0
	c.CacheTTLMin = &zero // the worst case, and a documented setting
	tmp := t.TempDir()
	tally := filepath.Join(tmp, "runs")

	hook := script(t, tmp, "source.sh", "echo run >>"+tally+"\n"+`echo '{"name":"repo/7-warm","title":"warm"}'`+"\n")
	if err := os.WriteFile(filepath.Join(c.Workspace, MarkerFile),
		[]byte("source: "+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trustSpaceConfigIn(t, c)

	items, err := c.RefreshRemoteItems()
	if err != nil {
		t.Fatalf("a working source failed the refresh: %v", err)
	}
	if n := runsOf(t, tally); n != 1 {
		t.Errorf("one ctrl-r ran the source %d times, want exactly 1", n)
	}
	if len(items) != 1 || items[0].Name != "repo/7-warm" {
		t.Errorf("the refreshed rows are wrong: %+v", items)
	}

	// And when it fails, which is the case that doubled: still one run, still the stale rows, and
	// still the reason, because dropping the refresh error is the fix this must not be.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho run >>"+tally+"\necho 'tracker is down' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	items, err = c.RefreshRemoteItems()
	if n := runsOf(t, tally) - 1; n != 1 {
		t.Errorf("one ctrl-r against a broken source ran it %d times, want exactly 1", n)
	}
	if err == nil {
		t.Error("a broken source reported no error, so nothing would annotate the list")
	}
	if len(items) != 1 || items[0].Name != "repo/7-warm" {
		t.Errorf("the stale rows were dropped by the forced refresh: %+v", items)
	}
}
