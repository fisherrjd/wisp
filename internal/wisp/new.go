package wisp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// iidFromURL pulls the kind and the number out of a GitLab item URL. Work items, issues and
// merge requests all share the /-/<kind>/<n> shape, so one pattern covers every link you might
// paste. The kind is kept because merge requests are not work items in GitLab's schema and have
// to be asked for by a different name.
var iidFromURL = regexp.MustCompile(`/-/(work_items|issues|merge_requests)/([0-9]+)`)

// projectFromURL pulls a project's full path out of an item URL: everything between the host and
// the /-/ that introduces the kind.
//
// Needed as well as the repo name because item numbers are per-project and the group holds many,
// so `42` on its own does not identify anything.
var projectFromURL = regexp.MustCompile(`^https?://[^/]+/(.+?)/-/(?:work_items|issues|merge_requests)/[0-9]+`)

// NewItem turns a line of user input into an item on disk, ready to open.
//
// Two shapes are accepted, distinguished by the input itself rather than by a mode the user has
// to select first:
//
//   - a GitLab URL, which becomes <repo>/<iid>-<slug> with the repo and number read from the
//     link and the slug derived from the item's title
//   - anything else, which is tied to a repo when one can be inferred (see InferRepo) and
//     otherwise becomes _adhoc/<slug>: work with no ticket and no repo behind it
//
// An input already shaped like <repo>/<name> is taken literally, so an item can be placed under
// a specific repo without a URL.
//
// _adhoc is the fallback, not the default. An item with a repo gets a worktree, a shell window
// and a hub note pulled into its briefing; one without gets a notes-only session. So a bare name
// tries to land under a repo first, and only files under _adhoc when nothing here says which.
func (c Config) NewItem(input string) (Item, error) {
	it, _, err := c.NewItemNoted(input)
	return it, err
}

// NewItemNoted is NewItem with the one thing worth saying about where the item landed: when a
// bare name fell back to _adhoc, the note says so and lists the repos it could have been tied
// to. Empty when there is nothing to add.
func (c Config) NewItemNoted(input string) (Item, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Item{}, "", fmt.Errorf("nothing to create")
	}
	// The folder belongs on the machine that owns the workspace, and so does the GitLab lookup
	// that names it. Both happen there and only the name comes back.
	if c.IsRemote() {
		out, err := c.Location.run("new", input, "--json")
		if err != nil {
			return Item{}, "", err
		}
		var made struct {
			Name string `json:"name"`
			Note string `json:"note"`
		}
		if err := json.Unmarshal(out, &made); err != nil || made.Name == "" {
			return Item{}, "", fmt.Errorf("%s: could not read the created item back", c.Location.Host)
		}
		return Item{Name: made.Name, State: StateFolder}, made.Note, nil
	}

	// The workflow gets a say before wisp decides anything: a `new` hook may name the item
	// outright, and `item.parent` may say where a bare name lands. Both are workspace-level,
	// which is why the item passed here is the zero one.
	w := c.WorkflowFor(Item{}, "")

	var name, note, title, repo string
	isURL := strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://")
	switch {
	case isURL:
		// Deferred: naming from a link needs the network, and a `new` hook that has an opinion
		// makes that trip unnecessary. name stays empty here and is filled in below.
	case strings.Contains(input, "/"):
		// Taken as-is so an item can be filed under a repo directly. Each segment is still
		// slugified, since these become directory names.
		repo, rest, _ := strings.Cut(input, "/")
		dir, slug := slugifyPath(repo), slugifyPath(rest)
		if dir == "" || slug == "" {
			return Item{}, "", fmt.Errorf("%q does not name an item; give it a repo and a name, like `wisp/my-thing`", input)
		}
		name = dir + "/" + slug
	default:
		slug := slugifyPath(input)
		if slug == "" {
			return Item{}, "", fmt.Errorf("%q does not name an item", input)
		}
		switch {
		case w.Item.Parent != "":
			// The workflow has said where bare names go, so there is nothing to infer and
			// nothing to note: this is the answer, not a fallback.
			name = w.Item.Parent + "/" + slug
		default:
			repo = c.InferRepo()
			if repo != "" {
				name = repo + "/" + slug
			} else {
				name = "_adhoc/" + slug
				if repos, _ := c.Repos(); len(repos) > 0 {
					note = fmt.Sprintf("filed under _adhoc: nothing here says which repo it belongs to (%s)\n  wisp new <repo>/%s ties it to one", strings.Join(repos, ", "), slug)
				}
			}
		}
	}

	if w.Hooks.New != "" {
		hooked, err := c.runNewHook(w, input, name, repo, isURL)
		if err != nil {
			return Item{}, "", err
		}
		if hooked.Name != "" {
			// The hook answered, so wisp's own reasoning about where the name would have gone
			// is moot, and so is the note explaining that reasoning.
			name, title, note = hooked.Name, hooked.Title, ""
		}
	}
	if name == "" && isURL {
		resolved, err := c.itemFromURL(input)
		if err != nil {
			return Item{}, "", err
		}
		name = resolved
	}

	it := Item{Name: name, State: StateFolder, Title: title}
	if isDir(c.ItemDir(it.Name)) {
		// Already there: hand it back rather than failing, so ctrl-n on something that exists
		// just opens it.
		return it, note, nil
	}
	if err := c.makeItemDir(w, it); err != nil {
		return Item{}, "", err
	}
	return it, note, nil
}

// newHookInput is what a `new` hook is told. Everything wisp already worked out is here, so a
// hook that only wants to change one thing about the answer does not have to re-derive the rest.
type newHookInput struct {
	Input string `json:"input"`
	// Default is the name wisp would give the item on its own, "" for a link, since naming one of
	// those costs a network round trip the hook may be about to make unnecessary.
	Default string `json:"default"`
	// Repo is the checkout the name was tied to, when one was: the picker's choice, or the one
	// InferRepo found. "" when nothing decided.
	Repo      string   `json:"repo"`
	Repos     []string `json:"repos"`
	Parent    string   `json:"parent"`
	URL       bool     `json:"url"`
	Workspace string   `json:"workspace"`
	Vault     string   `json:"vault"`
}

// runNewHook asks the workflow what this input should be called.
//
// A separate hook rather than a third mode on `source`, and the reason is a script that already
// exists: a source that ignores its arguments and prints the whole tracker would, asked `--new`,
// answer with a list, and wisp would name the item after row one. A key nobody has set cannot be
// answered by accident. The same hazard sits under `--url`, which is why both now refuse more
// than one object.
func (c Config) runNewHook(w Workflow, input, def, repo string, isURL bool) (Item, error) {
	repos, _ := c.Repos()
	if repos == nil {
		repos = []string{}
	}
	payload, err := json.Marshal(newHookInput{
		Input: input, Default: def, Repo: repo, Repos: repos, Parent: w.Item.Parent,
		URL: isURL, Workspace: c.Workspace, Vault: c.Vault,
	})
	if err != nil {
		return Item{}, err
	}
	out, err := c.runHook(w.Hooks.New, payload, input)
	if errors.Is(err, ErrHookTruncated) {
		return Item{}, fmt.Errorf("%s answered, but the answer was cut: %v", shortPath(w.Hooks.New), err)
	}
	if err != nil {
		// A refusal is the hook's to make: a tracker that will not file work without a ticket
		// says so here, and the typed text stays on the line for a second try.
		return Item{}, fmt.Errorf("%s would not make that item (%v)", shortPath(w.Hooks.New), err)
	}
	row, ok, err := c.oneObject(out, w.Hooks.New, "new")
	if err != nil || !ok {
		return Item{}, err
	}
	return Item{Name: row.Name, Title: row.Title}, nil
}

// InferRepo is the repo a bare item name belongs to, when the workspace can say without asking.
//
// Two answers count, in this order: the checkout the process is standing in, and the only
// checkout there is. Standing inside <workspace>/<repo> when you run `wisp new` is as clear a
// statement of which repo you mean as typing it, and a workspace with one repo has nothing to
// choose between. Anything less certain is "", and the caller falls back to _adhoc rather than
// guessing: a wrong repo gets a worktree built for work that was never about it, and a missing
// one is a folder rename away.
//
// The picker runs at the workspace root, so for it only the single-repo answer ever fires; the
// choice with several repos is its own step there, not a guess made here.
func (c Config) InferRepo() string {
	repos, err := c.Repos()
	if err != nil || len(repos) == 0 {
		return ""
	}
	if !c.IsRemote() {
		// Both sides resolved, because the cwd comes back with symlinks followed (on macOS
		// /var is /private/var) and the workspace path is whatever the config spelled.
		if cwd, err := os.Getwd(); err == nil {
			cwd = resolvePath(cwd)
			if rel, err := filepath.Rel(resolvePath(c.Workspace), cwd); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
				for _, r := range repos {
					if r == top {
						return r
					}
				}
			}
		}
	}
	if len(repos) == 1 {
		return repos[0]
	}
	return ""
}

// makeItemDir creates an item's folder and its notes stub.
//
// Shared with Open, which needs it for an item picked straight off GitLab: that item has never
// had a folder here, and the vault is where its notes and its context file go.
//
// Safe to call on a folder that already exists, and it never overwrites an existing notes.md.
func (c Config) makeItemDir(w Workflow, item Item) error {
	dir := c.ItemDir(item.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := c.writeSeeds(w, item); err != nil {
		return err
	}
	notes := filepath.Join(dir, "notes.md")
	if exists(notes) {
		return nil
	}
	return os.WriteFile(notes, []byte(fmt.Sprintf("# %s\n\n", item.Slug())), 0o644)
}

// writeSeeds copies the workflow's seed files into a new item folder.
//
// Top-level regular files only, never over a file that is there, and never a dotfile: a seed is
// the shape a folder starts in, not a way to put an .envrc where a shell will read it. The files
// are data and are not gated, and this is the rule that keeps that honest. A seeded
// orchestration.md that names a program is still caught by the item gate on open, like any other.
func (c Config) writeSeeds(w Workflow, item Item) error {
	if w.Item.Seed == "" {
		return nil
	}
	entries, err := os.ReadDir(w.Item.Seed)
	if err != nil {
		return nil // validate has already noted a missing seed dir; a vanished one is the same
	}
	dir := c.ItemDir(item.Name)
	vars := c.seedVars(w, item)
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || safeSegment(name) == "" {
			continue
		}
		dst := filepath.Join(dir, name)
		if exists(dst) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(w.Item.Seed, name))
		if err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
		if err := os.WriteFile(dst, []byte(Expand(string(raw), vars)), 0o644); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
	}
	return nil
}

// seedVars is the template vocabulary a seed file sees. Item facts only: a seed is written
// before any session exists, so there is no branch, worktree or prompt to offer yet.
func (c Config) seedVars(w Workflow, item Item) map[string]string {
	parent, _, _ := strings.Cut(item.Name, "/")
	return map[string]string{
		"item":      item.Name,
		"slug":      item.Slug(),
		"repo":      item.Repo(),
		"iid":       item.IID(),
		"title":     item.Title,
		"date":      time.Now().Format("2006-01-02"),
		"parent":    parent,
		"workspace": c.Workspace,
		"vault":     c.Vault,
	}
}

// itemFromURL turns a pasted link into <repo>/<iid>-<slug>.
//
// The workflow's source hook gets first refusal, in its --url mode. Listing your work and
// starting an item by pasting its link are two code paths, and replacing only the first would
// leave someone on GitHub able to see their tickets and unable to open one, which is the most
// used way to start an item. With no source hook this is the built-in GitLab handling, unchanged.
func (c Config) itemFromURL(url string) (string, error) {
	if it, err := c.ResolveURL(url); err == nil && it.Name != "" {
		return it.Name, nil
	} else if err != nil && !errors.Is(err, ErrNoURLSource) {
		return "", err
	}
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
	repo, kind, iid := repoMatch[1], iidMatch[1], iidMatch[2]

	title, err := c.titleFor(url, kind, iid)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s-%s", repo, iid, slugify(title)), nil
}

// titleFor finds an item's title so the folder gets a readable slug.
//
// The cache is only ever the assigned-items query, so it is taken as a free hit and never relied
// on: an item you are not the assignee of is looked up on its own terms. Being assigned is what
// puts something in your queue, not what makes it something you can open, and for a while this
// conflated the two, so pasting a link to a colleague's merge request to review it was refused.
func (c Config) titleFor(url, kind, iid string) (string, error) {
	project := projectPath(url)
	if project == "" {
		return "", fmt.Errorf("no project path in that URL")
	}
	if title := c.cachedTitle(project, iid); title != "" {
		return title, nil
	}
	return fetchTitle(project, kind, iid)
}

// projectPath is a project's full path from one of its item URLs, or "" if the URL is not one.
func projectPath(url string) string {
	m := projectFromURL.FindStringSubmatch(url)
	if m == nil {
		return ""
	}
	return m[1]
}

// cachedTitle looks an item up in the assigned-items cache.
//
// Matched on the project as well as the number. The group query spans every project under the
// group and numbering is per-project, so on the number alone a paste of one project's #42 would
// take another project's title and file the folder under a name belonging to different work.
func (c Config) cachedTitle(project, iid string) string {
	raw, err := os.ReadFile(c.CachePath())
	if err != nil {
		return ""
	}
	var parsed glabResponse
	if json.Unmarshal(raw, &parsed) != nil {
		return ""
	}
	for _, n := range parsed.Data.Group.WorkItems.Nodes {
		if n.IID == iid && projectPath(n.WebURL) == project {
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
