package wisp

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Where work comes from used to be one GraphQL query against one GitLab group. That made wisp a
// tool for one tracker: someone on GitHub, Jira, Linear or a text file got an empty remote
// section and a message about `gitlab.group`, which is wisp telling them their tracker is
// misconfigured when the truth is that wisp only knew one.
//
// A source hook replaces it. Two modes, one program, because it is one piece of knowledge:
//
//	source.sh                  no args, lists open work as JSONL
//	source.sh --url <url>      resolves one URL, prints one object, or nothing
//
// The second mode is the one a first draft of this forgot. Listing your work and starting an
// item by pasting its link are separate code paths, and a source that only replaced the first
// would leave a GitHub user able to see their tickets and unable to open one.

// sourceRow is one line of a source hook's output. `name` is required and must be the same
// two-level shape the vault uses; `title` is optional and fills the row's description when the
// local slug differs from whatever the tracker calls it.
type sourceRow struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// SourceCachePath is the source hook's cache, namespaced by workspace exactly as the GitLab one
// is, and separate from it: switching a workspace from one to the other must not serve the
// other's rows out of a warm cache.
func (c Config) SourceCachePath() string {
	sum := sha256.Sum256([]byte(c.Workspace))
	return filepath.Join(os.TempDir(), fmt.Sprintf("wisp-source-%s.jsonl", hex.EncodeToString(sum[:])[:12]))
}

// RemoteItems is every item the workspace's source knows about, at StateRemote.
//
// Caching stays wisp's job rather than each hook's. The hook is called when the cache is older
// than cache_ttl_min or when ctrl-r drops it, exactly as the GitLab query is. A hook that wants
// to be cheap can be; a hook that is slow does not have to think about it. Pushing the TTL into
// every hook would make ctrl-r mean something different for each one.
func (c Config) RemoteItems() ([]Item, error) {
	// The far side owns its own source, whatever it is. Asking for its board is the whole of
	// this end's job, and it never learns a hook was involved.
	if c.IsRemote() {
		return c.GitLabItems()
	}
	w := c.WorkflowFor(Item{}, "")
	if w.Hooks.Source == "" {
		return c.GitLabItems()
	}
	raw, err := c.cachedSource(w)
	if err != nil {
		return nil, err
	}
	return c.parseSource(raw), nil
}

// parseSource reads JSONL, one object per line.
//
// JSON per line rather than one array, because a long list should stream and because a truncated
// write should cost the last row rather than the whole response. A line that will not parse, or
// names something that is not an item, is skipped rather than failing the batch: one bad row from
// a tracker must not empty the picker.
func (c Config) parseSource(raw []byte) []Item {
	var out []Item
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var row sourceRow
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		if !c.validSourceName(row.Name) {
			continue
		}
		out = append(out, Item{Name: row.Name, State: StateRemote, Title: row.Title})
	}
	return out
}

// validSourceName is the guard on untrusted input. A source hook is a program someone else wrote
// producing names that become directories, so every one of them passes the same containment
// check anything else does, plus the two-level shape the vault is.
//
// A workflow may decide where names come from. It may not decide what a name is allowed to be:
// identity is what makes the same ticket found in a session, a folder and a source into one row.
func (c Config) validSourceName(name string) bool {
	if !c.itemInVault(name) {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	return true
}

func (c Config) cachedSource(w Workflow) ([]byte, error) {
	path := c.SourceCachePath()
	fresh := false
	if fi, err := os.Stat(path); err == nil {
		fresh = time.Since(fi.ModTime()) < time.Duration(c.GitLab.CacheTTLMin)*time.Minute
	}
	if !fresh {
		// A refresh failure falls back to a stale cache rather than emptying the list, which is
		// the same bargain the GitLab source has always made.
		if err := c.refreshSource(w); err != nil && !exists(path) {
			return nil, err
		}
	}
	return os.ReadFile(path)
}

func (c Config) refreshSource(w Workflow) error {
	out, err := c.runHook(w.Hooks.Source, nil)
	if err != nil {
		// A non-zero exit is a state, not an exception. The caller annotates the list with this
		// and paints the local items anyway: an empty source and a broken one looking identical
		// is what cost a real debugging session the last time this was silent.
		return fmt.Errorf("source: %w", err)
	}
	tmp := c.SourceCachePath() + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.SourceCachePath())
}

// ResolveURL turns a pasted link into an item name, through the workflow's source hook.
//
// Returns "" with no error when the source has no opinion about this URL, which is a normal
// state rather than a failure: not every tracker can resolve every link, and `wisp new` has its
// own message for "this is not something I can turn into an item".
func (c Config) ResolveURL(url string) (Item, error) {
	w := c.WorkflowFor(Item{}, "")
	if w.Hooks.Source == "" {
		return Item{}, ErrNoURLSource
	}
	out, err := c.runHookArgs(w.Hooks.Source, "--url", url)
	if err != nil {
		// A non-zero exit here is the source saying it does not recognise this link, which is a
		// normal state rather than a failure: not every tracker can resolve every URL. Said in
		// wisp's own words, because "exit status 1" is not something anyone can act on, and it
		// names the script so the next place to look is obvious.
		return Item{}, fmt.Errorf("%s did not recognise that link (%v)\n\nname the item yourself instead:\n  wisp new <repo>/<name>",
			shortPath(w.Hooks.Source), err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row sourceRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return Item{}, fmt.Errorf("source --url: %v is not one JSON object", line)
		}
		if !c.validSourceName(row.Name) {
			return Item{}, fmt.Errorf("source --url: %q is not an item name", row.Name)
		}
		return Item{Name: row.Name, Title: row.Title}, nil
	}
	return Item{}, nil
}

// ErrNoURLSource means this workspace has no source hook, so `wisp new <url>` falls through to
// the built-in GitLab handling rather than reporting a missing feature.
var ErrNoURLSource = fmt.Errorf("no source hook")
