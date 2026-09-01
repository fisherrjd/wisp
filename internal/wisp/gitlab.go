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
func (c Config) GitLabItems() ([]Item, error) {
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

	raw, err := c.cachedResponse()
	if err != nil {
		return nil, err
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
	return out, nil
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
func (c Config) cachedResponse() ([]byte, error) {
	path := c.CachePath()
	fresh := false
	if fi, err := os.Stat(path); err == nil {
		age := time.Since(fi.ModTime())
		fresh = age < time.Duration(c.GitLab.CacheTTLMin)*time.Minute
	}
	if !fresh {
		if err := c.refreshCache(); err != nil && !exists(path) {
			return nil, err
		}
	}
	return os.ReadFile(path)
}

// RefreshCache drops the cache so the next read re-queries. Bound to ctrl-r in the picker.
//
// Both caches, and then whichever source is actually configured. ctrl-r has to mean the same
// thing whatever the workspace's source is, which is the reason caching stayed wisp's job rather
// than moving into each hook.
func (c Config) RefreshCache() error {
	for _, p := range []string{c.CachePath(), c.SourceCachePath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if w := c.WorkflowFor(Item{}, ""); w.Hooks.Source != "" {
		return c.refreshSource(w)
	}
	return c.refreshCache()
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
	// Write via a temp file so a failed or partial write never leaves a corrupt cache.
	tmp := c.CachePath() + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.CachePath())
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
