package wisp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// iidFromURL pulls the number out of a GitLab item URL. Work items, issues and merge requests
// all share the /-/<kind>/<n> shape, so one pattern covers every link you might paste.
var iidFromURL = regexp.MustCompile(`/-/(?:work_items|issues|merge_requests)/([0-9]+)`)

// NewItem turns a line of user input into an item on disk, ready to open.
//
// Two shapes are accepted, distinguished by the input itself rather than by a mode the user has
// to select first:
//
//   - a GitLab URL, which becomes <repo>/<iid>-<slug> with the repo and number read from the
//     link and the slug derived from the item's title
//   - anything else, which becomes _adhoc/<slug>: work with no ticket behind it
//
// An input already shaped like <repo>/<name> is taken literally, so an item can be placed under
// a specific repo without a URL.
func (c Config) NewItem(input string) (Item, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Item{}, fmt.Errorf("nothing to create")
	}
	// The folder belongs on the machine that owns the workspace, and so does the GitLab lookup
	// that names it. Both happen there and only the name comes back.
	if c.IsRemote() {
		out, err := c.Location.run("new", input, "--json")
		if err != nil {
			return Item{}, err
		}
		var made struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(out, &made); err != nil || made.Name == "" {
			return Item{}, fmt.Errorf("%s: could not read the created item back", c.Location.Host)
		}
		return Item{Name: made.Name, State: StateFolder}, nil
	}

	var name string
	switch {
	case strings.HasPrefix(input, "http://"), strings.HasPrefix(input, "https://"):
		resolved, err := c.itemFromURL(input)
		if err != nil {
			return Item{}, err
		}
		name = resolved
	case strings.Contains(input, "/"):
		// Taken as-is so an item can be filed under a repo directly. Each segment is still
		// slugified, since these become directory names.
		repo, rest, _ := strings.Cut(input, "/")
		name = slugifyPath(repo) + "/" + slugifyPath(rest)
	default:
		name = "_adhoc/" + slugifyPath(input)
	}

	it := Item{Name: name, State: StateFolder}
	dir := c.ItemDir(it.Name)
	if isDir(dir) {
		// Already there: hand it back rather than failing, so ctrl-n on something that exists
		// just opens it.
		return it, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Item{}, err
	}
	notes := filepath.Join(dir, "notes.md")
	if !exists(notes) {
		body := fmt.Sprintf("# %s\n\n", it.Slug())
		if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
			return Item{}, err
		}
	}
	return it, nil
}

// itemFromURL builds <repo>/<iid>-<slug> from a pasted GitLab link.
func (c Config) itemFromURL(url string) (string, error) {
	if c.GitLab.RepoPattern == "" {
		return "", fmt.Errorf("set gitlab.repo_pattern in %s before pasting links", MarkerFile)
	}
	repoRe, err := regexp.Compile(c.GitLab.RepoPattern)
	if err != nil {
		return "", fmt.Errorf("gitlab.repo_pattern: %w", err)
	}
	repoMatch := repoRe.FindStringSubmatch(url)
	if repoMatch == nil {
		return "", fmt.Errorf("no repo in that URL; gitlab.repo_pattern did not match it")
	}
	iidMatch := iidFromURL.FindStringSubmatch(url)
	if iidMatch == nil {
		return "", fmt.Errorf("no item number in that URL")
	}
	repo, iid := repoMatch[1], iidMatch[1]

	title, err := c.titleFor(iid)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s-%s", repo, iid, slugify(title)), nil
}

// titleFor finds an item's title so the folder gets a readable slug. It checks the cache first
// and refreshes once before giving up, since a link is usually pasted precisely because the
// item is new enough not to be cached yet.
func (c Config) titleFor(iid string) (string, error) {
	if title := c.cachedTitle(iid); title != "" {
		return title, nil
	}
	if err := c.RefreshCache(); err != nil {
		return "", fmt.Errorf("could not reach gitlab to name #%s: %w", iid, err)
	}
	if title := c.cachedTitle(iid); title != "" {
		return title, nil
	}
	// Assigned-to-you is the query, so an unassigned item is invisible here. Say so, and point
	// at the way round it.
	return "", fmt.Errorf("#%s is not in your assigned items; type a name instead of a URL", iid)
}

func (c Config) cachedTitle(iid string) string {
	raw, err := os.ReadFile(c.CachePath())
	if err != nil {
		return ""
	}
	var parsed glabResponse
	if json.Unmarshal(raw, &parsed) != nil {
		return ""
	}
	for _, n := range parsed.Data.Group.WorkItems.Nodes {
		if n.IID == iid {
			return n.Title
		}
	}
	return ""
}

// slugifyPath makes one path segment safe as a directory name while keeping it readable. Unlike
// slugify it does not truncate: the user typed this and expects to see it back.
func slugifyPath(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '_' || r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
