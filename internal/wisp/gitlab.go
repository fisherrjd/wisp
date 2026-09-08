package wisp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type glabNode struct {
	IID    string `json:"iid"`
	Title  string `json:"title"`
	WebURL string `json:"webUrl"`
}

type glabResponse struct {
	Data struct {
		Group struct {
			WorkItems struct {
				Nodes []glabNode `json:"nodes"`
			} `json:"workItems"`
		} `json:"group"`
	} `json:"data"`
}

const query = `
{
  group(fullPath: %q) {
    workItems(assigneeUsernames: [%q], state: opened, includeDescendants: true, first: 100) {
      nodes { iid title webUrl }
    }
  }
}`

// itemResponse is the answer to itemQuery. Both fields are asked for by kind, never together, so
// only one of them is ever populated.
type itemResponse struct {
	Data struct {
		Project struct {
			MergeRequest *struct {
				Title string `json:"title"`
			} `json:"mergeRequest"`
			WorkItems struct {
				Nodes []glabNode `json:"nodes"`
			} `json:"workItems"`
		} `json:"project"`
	} `json:"data"`
}

// itemQuery asks for one item's title, by project and number.
//
// Deliberately not a widening of the queue query above. That one answers "what is on my plate"
// and is filtered to the assignee for good reason; this one answers "what is this link", which
// has no business caring who the item belongs to. They only ever looked like the same question
// because the queue's cache was the sole place a title had ever been written down.
func itemQuery(project, kind, iid string) string {
	// Merge requests are not work items in GitLab's schema, so the field follows the kind that
	// was in the URL rather than guessing and retrying.
	if kind == "merge_requests" {
		return fmt.Sprintf(`{ project(fullPath: %q) { mergeRequest(iid: %q) { title } } }`, project, iid)
	}
	return fmt.Sprintf(`{ project(fullPath: %q) { workItems(iid: %q) { nodes { title } } } }`, project, iid)
}

// titleFromResponse reads the title back out, whichever field it came in. Returns "" when the
// item is simply not there, which is a different failure from the query not running at all.
func titleFromResponse(raw []byte) (string, error) {
	var parsed itemResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("unreadable gitlab response: %w", err)
	}
	p := parsed.Data.Project
	if p.MergeRequest != nil && p.MergeRequest.Title != "" {
		return p.MergeRequest.Title, nil
	}
	for _, n := range p.WorkItems.Nodes {
		if n.Title != "" {
			return n.Title, nil
		}
	}
	return "", nil
}

// fetchTitle asks GitLab what a single item is called. No cache: this runs once, when a link is
// pasted by hand, and the thing it is being asked about is usually too new or too much someone
// else's to have been cached by anything.
func fetchTitle(project, kind, iid string) (string, error) {
	if _, err := exec.LookPath("glab"); err != nil {
		return "", fmt.Errorf("glab not on PATH, so #%s cannot be named from its link; type a name instead", iid)
	}
	out, err := exec.Command("glab", "api", "graphql", "-f", "query="+itemQuery(project, kind, iid)).Output()
	if err != nil {
		return "", fmt.Errorf("could not reach gitlab to name #%s: %w", iid, err)
	}
	title, err := titleFromResponse(out)
	if err != nil {
		return "", err
	}
	if title == "" {
		return "", fmt.Errorf("gitlab has no #%s in %s, or your token cannot see it", iid, project)
	}
	return title, nil
}

// GitLabItems returns open work items assigned to the configured user. It is entirely optional:
// with no group configured, no glab on PATH, or no network, it returns nothing and the picker
// simply shows local items only.
func (c Config) GitLabItems() ([]Item, error) { return c.gitLabItems(false) }

// gitLabItems is GitLabItems plus the one thing a forced refresh has to say: that the query is
// being re-asked rather than read. The flag stops at the far side, whose board is a live request
// with no cache of ours in front of it, so there is nothing there for a refresh to skip.
func (c Config) gitLabItems(force bool) ([]Item, error) {
	// The far side owns its own remote source: the group, the credentials and the cache are all
	// over there. Asking for its board with gitlab folded in is the whole of this end's job.
	if c.IsRemote() {
		b, err := c.Location.Board(true)
		if err != nil {
			return nil, err
		}
		if b.Note != "" {
			return b.AsItems(), errors.New(b.Note)
		}
		return b.AsItems(), nil
	}
	// Say so rather than returning an empty list. An unconfigured remote source and a source
	// with no assigned items look identical in the picker, and the silent version of this cost
	// a real debugging session: after the group and username stopped being hardcoded, a
	// workspace with no .wisp.yaml simply showed fewer rows and gave no reason.
	if missing := c.gitlabMissing(); len(missing) > 0 {
		return nil, fmt.Errorf("gitlab source off: set %s in %s",
			strings.Join(missing, ", "), filepath.Join(c.Workspace, ".wisp.yaml"))
	}
	repoRe, err := regexp.Compile(c.GitLab.RepoPattern)
	if err != nil {
		return nil, fmt.Errorf("gitlab.repo_pattern: %w", err)
	}
	if repoRe.NumSubexp() < 1 {
		return nil, fmt.Errorf("gitlab.repo_pattern needs one capturing group for the repo name")
	}

	// Stale rows survive the error that stopped them being refreshed, for the same reason the
	// source hook's do: an old list with a note beats no list at all, and the caller decides.
	raw, cacheErr := c.cachedResponse(force)
	if len(raw) == 0 {
		return nil, cacheErr
	}
	var parsed glabResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("gitlab cache: %w", err)
	}

	var out []Item
	for _, n := range parsed.Data.Group.WorkItems.Nodes {
		// A /groups/ segment marks a group-level epic. It has no repo directory to open, so it
		// would only ever be a dead entry in the picker.
		if strings.Contains(n.WebURL, "/groups/") {
			continue
		}
		m := repoRe.FindStringSubmatch(n.WebURL)
		if m == nil || m[1] == "" || n.IID == "" {
			continue
		}
		out = append(out, Item{
			Name:  fmt.Sprintf("%s/%s-%s", m[1], n.IID, slugify(n.Title)),
			State: StateRemote,
			Title: n.Title,
		})
	}
	return out, cacheErr
}

// gitlabMissing names the config keys the remote source still needs, so the message can point
// at what to fix rather than just reporting that something is wrong.
func (c Config) gitlabMissing() []string {
	var missing []string
	if c.GitLab.Group == "" {
		missing = append(missing, "gitlab.group")
	}
	if c.GitLab.Username == "" {
		missing = append(missing, "gitlab.username")
	}
	if c.GitLab.RepoPattern == "" {
		missing = append(missing, "gitlab.repo_pattern")
	}
	return missing
}

// cachedResponse returns the cached GraphQL response, refreshing it if it is missing or older
// than the configured TTL. A refresh failure falls back to a stale cache rather than emptying
// the picker.
func (c Config) cachedResponse(force bool) ([]byte, error) {
	return c.cached(c.CachePath(), c.refreshCache, force)
}

// cached is the one read-or-refresh policy both sources share.
//
// Four things it decides, and they are the same four whichever source is behind it. A file
// younger than the TTL is used as is, unless the caller forced the refresh, which is what ctrl-r
// means and the only thing that overrides a warm cache. A refresh failure with a usable file
// returns the stale rows *and* the reason, because serving an old list silently is how a broken
// source comes to look exactly like a quiet one, which is the failure that cost a real debugging
// session. A refresh failure with nothing to fall back on is the error alone. And a refresh that
// reported success while leaving nothing readable behind is the read error, which used to be
// dropped on the floor: `return nil, refreshErr` with a nil refreshErr handed the caller an empty
// list and no reason at all, the exact state the paragraph above exists to prevent, arrived at
// from the one direction nobody was watching.
//
// The refresh is called from here and only from here, so a forced refresh costs exactly one run
// of it. The alternative, refreshing and then reading as two calls, ran the source twice: a failed
// refresh leaves the file's mtime where it was, so the read that followed found the cache stale
// and asked again.
//
// Written once because it was written twice: the source cache and the GitLab cache drifted apart
// on exactly this policy, and fixing one of them meant fixing the other afterwards.
func (c Config) cached(path string, refresh func() error, force bool) ([]byte, error) {
	if !force {
		if fi, err := os.Stat(path); err == nil {
			if time.Since(fi.ModTime()) < c.cacheTTL() {
				return os.ReadFile(path)
			}
		}
	}
	refreshErr := refresh()
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		// The refresh error wins when there is one: it is the cause, and an unreadable cache is
		// only its symptom. With no refresh error the read failure is the whole of what happened,
		// and saying which half went wrong beats naming neither.
		if refreshErr != nil {
			return nil, refreshErr
		}
		return nil, fmt.Errorf("refreshed, but the cache could not be read back: %w", readErr)
	}
	return raw, refreshErr
}

// writeCache replaces a cache file in one step, so a failed or partial write never leaves a
// corrupt one behind.
func writeCache(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RefreshRemoteItems re-asks whichever source this workspace has and returns what came back. It
// is what ctrl-r in the picker calls.
//
// ctrl-r has to mean the same thing whatever the workspace's source is, which is the reason
// caching stayed wisp's job rather than moving into each hook.
//
// It refreshes rather than deleting. Dropping the cache first meant a ctrl-r against a source
// that had since broken emptied the list outright, which is exactly the state every other path
// here goes out of its way to avoid: a broken source has to annotate the rows, not remove them.
//
// One call rather than two, and that is the whole point of it existing. The picker used to refresh
// and then ask for the items separately, and the ask went through the ordinary TTL check: a failed
// refresh leaves the cache file's mtime alone, so the read decided the cache was still stale and
// ran the hook a second time. A source hook gets sixty seconds, so one keypress could stall for
// two minutes against a document promising one, and `cache_ttl_min: 0`, which is a supported
// setting meaning "ask on every load", did it on every press whether the source worked or not.
// Discarding the refresh error would also have collapsed the two calls into one, and it is the
// wrong fix twice over: that error is what the status line shows, and losing errors is the other
// bug this file has already been through.
func (c Config) RefreshRemoteItems() ([]Item, error) {
	return c.sourcedItems(true)
}

func (c Config) refreshCache() error {
	if _, err := exec.LookPath("glab"); err != nil {
		return fmt.Errorf("glab not on PATH, remote items unavailable")
	}
	q := fmt.Sprintf(query, c.GitLab.Group, c.GitLab.Username)
	out, err := exec.Command("glab", "api", "graphql", "-f", "query="+q).Output()
	if err != nil {
		return fmt.Errorf("glab query failed: %w", err)
	}
	return writeCache(c.CachePath(), out)
}

// slugify derives a folder-style slug from a work item title: lowercase, non-alphanumerics to
// spaces, first four words joined by dashes. It matches what the bash version produced, so
// existing vault folders keep deduplicating against their remote counterparts.
func slugify(title string) string {
	var words []string
	cur := strings.Builder{}
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, "-")
}
