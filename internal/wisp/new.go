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
		dir, slug := slugifyPath(repo), slugifyPath(rest)
		if dir == "" || slug == "" {
			return Item{}, fmt.Errorf("%q does not name an item; give it a repo and a name, like `wisp/my-thing`", input)
		}
		name = dir + "/" + slug
	default:
		slug := slugifyPath(input)
		if slug == "" {
			return Item{}, fmt.Errorf("%q does not name an item", input)
		}
		name = "_adhoc/" + slug
	}

	it := Item{Name: name, State: StateFolder}
	if isDir(c.ItemDir(it.Name)) {
		// Already there: hand it back rather than failing, so ctrl-n on something that exists
		// just opens it.
		return it, nil
	}
	if err := c.makeItemDir(it); err != nil {
		return Item{}, err
	}
	return it, nil
}

// makeItemDir creates an item's folder and its notes stub.
//
// Shared with Open, which needs it for an item picked straight off GitLab: that item has never
// had a folder here, and the vault is where its notes and its context file go.
//
// Safe to call on a folder that already exists, and it never overwrites an existing notes.md.
func (c Config) makeItemDir(item Item) error {
	dir := c.ItemDir(item.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	notes := filepath.Join(dir, "notes.md")
	if exists(notes) {
		return nil
	}
	return os.WriteFile(notes, []byte(fmt.Sprintf("# %s\n\n", item.Slug())), 0o644)
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
//
// Returns "" for anything that does not name a directory of its own. Dots survive slugification
// because plenty of repos have one in the name, which meant `wisp new ../thing` produced the
// literal segment ".." and wrote its folder outside the vault entirely.
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
	out := strings.Trim(b.String(), "-")
	// "." and ".." are directions, not names. Everything else made only of dots is no better as a
	// folder, so the whole family goes rather than the two spellings that happen to traverse.
	if strings.Trim(out, ".") == "" {
		return ""
	}
	return out
}
