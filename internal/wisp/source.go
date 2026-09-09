package wisp

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Rank orders the list, 1 first. Optional: a source that leaves it out is listed in the
	// order wisp always used.
	Rank int `json:"rank"`
}

// sourceCachePathFor is the source hook's cache: separate from the GitLab one, and keyed on the
// hook as well as the workspace.
//
// Both halves of that matter. Separate, so switching a workspace between the two does not serve
// one's rows out of the other's warm cache. Keyed on the hook, because on the workspace alone
// changing `source:` served the previous hook's rows until the TTL ran out, which is the same
// "an empty source and a broken one look identical" problem wearing different clothes.
//
// It takes the hook rather than resolving one, so no caller pays a five-layer resolution to find
// out where a file is.
func (c Config) sourceCachePathFor(hook string) string {
	sum := sha256.Sum256([]byte(c.Workspace + "\x00" + hook))
	return filepath.Join(os.TempDir(), fmt.Sprintf("wisp-source-%s.jsonl", hex.EncodeToString(sum[:])[:12]))
}

// RemoteItems is every item the workspace's source knows about, at StateRemote.
//
// Caching stays wisp's job rather than each hook's. The hook is called when the cache is older
// than cache_ttl_min or when ctrl-r drops it, exactly as the GitLab query is. A hook that wants
// to be cheap can be; a hook that is slow does not have to think about it. Pushing the TTL into
// every hook would make ctrl-r mean something different for each one.
func (c Config) RemoteItems() ([]Item, error) { return c.sourcedItems(false) }

// sourcedItems is RemoteItems carrying the one bit ctrl-r adds: whether the source is being
// re-asked rather than read.
//
// Both entry points come through here so that "which source does this workspace have" stays
// written down once. It was written down twice, in the read and in the refresh, and the two
// halves then disagreed about the TTL, which is how a single ctrl-r came to run the hook twice.
//
// Named for the source rather than for remoteness because remoteItems is already taken by a
// different question: what a workspace on another machine holds.
func (c Config) sourcedItems(force bool) ([]Item, error) {
	// The far side owns its own source, whatever it is. Asking for its board is the whole of
	// this end's job, and it never learns a hook was involved.
	if c.IsRemote() {
		return c.gitLabItems(force)
	}
	w := c.WorkflowFor(Item{}, "")
	if w.Hooks.Source == "" {
		return c.gitLabItems(force)
	}
	// The rows and the reason, not one or the other. cachedSource deliberately hands back stale
	// rows alongside the error that stopped them being refreshed, and returning early on the
	// error threw the rows away, which turned "here is an old list, and here is why" back into
	// the empty-and-annotated list this was supposed to stop being.
	raw, err := c.cachedSource(w, force)
	return c.parseSource(raw), err
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
		out = append(out, Item{Name: row.Name, State: StateRemote, Title: row.Title, Rank: max(row.Rank, 0)})
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

func (c Config) cachedSource(w Workflow, force bool) ([]byte, error) {
	return c.cached(c.sourceCachePathFor(w.Hooks.Source), func() error { return c.refreshSource(w) }, force)
}

func (c Config) refreshSource(w Workflow) error {
	out, err := c.runHook(w.Hooks.Source, nil)
	if err != nil {
		// A non-zero exit is a state, not an exception. The caller annotates the list with this
		// and paints the local items anyway: an empty source and a broken one looking identical
		// is what cost a real debugging session the last time this was silent.
		return fmt.Errorf("source: %w", err)
	}
	return writeCache(c.sourceCachePathFor(w.Hooks.Source), out)
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
	out, err := c.runHook(w.Hooks.Source, nil, "--url", url)
	// Truncation is not a refusal, so it does not get the refusal's wording. A source that
	// recognised the link and then printed 8 MB about it did not fail to recognise it, and
	// telling someone their link was unrecognised sends them to fix the wrong thing. One JSON
	// object is nowhere near the ceiling, so this is about the message being true rather than
	// about a case anyone will hit.
	if errors.Is(err, ErrHookTruncated) {
		return Item{}, fmt.Errorf("%s answered about that link, but the answer was cut: %v", shortPath(w.Hooks.Source), err)
	}
	if err != nil {
		// A non-zero exit here is the source saying it does not recognise this link, which is a
		// normal state rather than a failure: not every tracker can resolve every URL. Said in
		// wisp's own words, because "exit status 1" is not something anyone can act on, and it
		// names the script so the next place to look is obvious.
		return Item{}, fmt.Errorf("%s did not recognise that link (%v)\n\nname the item yourself instead:\n  wisp new <repo>/<name>",
			shortPath(w.Hooks.Source), err)
	}
	row, ok, err := c.oneObject(out, w.Hooks.Source, "source --url")
	if err != nil || !ok {
		return Item{}, err
	}
	return Item{Name: row.Name, Title: row.Title}, nil
}

// oneObject reads the answer to a question about one item: exactly one JSON object, or nothing
// at all, which is "no opinion" and is not an error.
//
// More than one object is refused by name rather than taken from the top. A source that ignores
// its arguments and lists the whole tracker looks, from here, exactly like one that answered, and
// naming an item after whatever happened to be row one was a silent wrong answer to a question
// that was never heard. The name is checked as every name from a hook is: a program somebody else
// wrote may decide what an item is called, not what a name is allowed to be.
func (c Config) oneObject(out []byte, script, what string) (sourceRow, bool, error) {
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	switch len(lines) {
	case 0:
		return sourceRow{}, false, nil
	case 1:
	default:
		return sourceRow{}, false, fmt.Errorf("%s: %s answered with a list of %d, not one item; a hook asked about one item answers with one object, or nothing",
			what, shortPath(script), len(lines))
	}
	var row sourceRow
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		return sourceRow{}, false, fmt.Errorf("%s: %v is not one JSON object", what, lines[0])
	}
	if !c.validSourceName(row.Name) {
		return sourceRow{}, false, fmt.Errorf("%s: %q is not an item name", what, row.Name)
	}
	return row, true, nil
}

// ErrNoURLSource means this workspace has no source hook, so `wisp new <url>` falls through to
// the built-in GitLab handling rather than reporting a missing feature.
var ErrNoURLSource = fmt.Errorf("no source hook")
