package wisp

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeTmux puts a tmux on PATH that answers the one question CurrentWorkspace asks: what the
// current session's @wisp_ws says. Anything else exits 0 with nothing, which is what the
// set-option calls elsewhere expect.
func fakeTmux(t *testing.T, ws string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nif [ \"$1 $3\" = 'display-message #{@wisp_ws}' ]; then echo " +
		shellQuote(ws) + "; fi\n"
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/fake,1,0")
}

// The session you are sitting in names the workspace when the directory cannot.
//
// A remote workspace's home session and the wrapper around each of its items both run in $HOME,
// because the workspace path is on another machine. Searching upward from there found nothing and
// fell back to the default workspace, so `wisp`, `hop` and `next` pressed inside a remote item all
// acted on the local workspace instead of the one on screen.
func TestLoadFallsBackToTheSessionsWorkspace(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("WISP_WORKSPACE", "")
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "working_items"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfgDir, "wisp"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfgDir, "wisp", "config.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("default: home\nworkspaces:\n  home: " + local + "\nhosts:\n  eldo: jade@eldo\n")

	// Nowhere near a workspace, which is where the wrapper and the remote home both run.
	t.Chdir(t.TempDir())

	fakeTmux(t, "eldo")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "eldo" {
		t.Fatalf("workspace resolved to %q, want eldo", c.Name)
	}
	if !c.IsRemote() || c.Location.Host != "jade@eldo" {
		t.Errorf("want the machine's workspace, got %+v", c.Location)
	}

	// A workspace since forgotten falls through to the default rather than failing: the session
	// outlives the config entry it was made from.
	fakeTmux(t, "gone")
	if c, err := Load(""); err != nil {
		t.Errorf("a stale @wisp_ws should not be an error: %v", err)
	} else if c.Name != "home" {
		t.Errorf("stale session workspace resolved to %q, want home", c.Name)
	}

	// A directory that does resolve still wins, so cd-ing somewhere keeps meaning what it meant.
	t.Chdir(local)
	fakeTmux(t, "eldo")
	if c, err := Load(""); err != nil {
		t.Fatal(err)
	} else if c.Name != "home" {
		t.Errorf("the directory lost to the session: got %q, want home", c.Name)
	}
}
