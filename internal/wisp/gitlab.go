package wisp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// GitLabItems returns open work items assigned to the configured user. It is entirely optional:
// with no group configured, no glab on PATH, or no network, it returns nothing and the picker
// simply shows local items only.
func (c Config) GitLabItems() ([]Item, error) {
	if c.GitLab.Group == "" || c.GitLab.Username == "" || c.GitLab.RepoPattern == "" {
		return nil, nil
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
func (c Config) RefreshCache() error {
	if err := os.Remove(c.CachePath()); err != nil && !os.IsNotExist(err) {
		return err
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
