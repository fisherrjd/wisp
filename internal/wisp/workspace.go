package wisp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Repos lists the workspace's repo checkouts: immediate subdirectories holding a .git, minus
// the vault itself, which is a git repo too but is not a code repo.
func (c Config) Repos() ([]string, error) {
	if c.IsRemote() {
		out, err := c.Location.run("repos")
		if err != nil {
			return nil, err
		}
		var repos []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				repos = append(repos, line)
			}
		}
		return repos, nil
	}
	entries, err := os.ReadDir(c.Workspace)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == c.Vault {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.Workspace, e.Name(), ".git")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// skipDirs are vault subdirectories that are infrastructure rather than work.
var skipDirs = map[string]bool{".git": true, ".claude": true, ".obsidian": true}

// LocalItems finds vault folders two levels deep: <repo>/<item>, plus anything under _adhoc.
//
// Every subdirectory counts, not only the <iid>-<slug> ones. `wisp new <repo>/<name>` files an
// item under a repo without a ticket behind it, and requiring a leading number here meant wisp
// created those, opened them, and then left them out of its own list.
func (c Config) LocalItems() ([]Item, error) {
	top, err := os.ReadDir(c.VaultDir())
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, parent := range top {
		if !parent.IsDir() || skipDirs[parent.Name()] {
			continue
		}
		children, err := os.ReadDir(filepath.Join(c.VaultDir(), parent.Name()))
		if err != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() || skipDirs[child.Name()] {
				continue
			}
			name := parent.Name() + "/" + child.Name()
			// Read here rather than on demand later because this walk is already at the folder,
			// and because the vault row is the only source that can know: sessions and GitLab
			// rows have no note to read and merge in as not-done.
			out = append(out, Item{
				Name:  name,
				State: StateFolder,
				Done:  c.ItemDone(name),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ItemDir is where an item's notes and manifest live.
func (c Config) ItemDir(item string) string { return filepath.Join(c.VaultDir(), item) }

// RequireItem refuses a name that points at nothing, before Open builds a session around it.
//
// Open tolerates a missing vault folder on purpose: an item picked off GitLab has never had one
// here. That tolerance is fine for the picker, which only ever offers rows it found, and wrong
// for the command line, where a typo became a real session named after the typo, holding an agent
// that was told nothing, which then sat in the list until someone killed it by hand.
//
// Three ways to be real, none of them a network call: the folder is there, a session is already
// running under the name, or its repo half is a directory in this workspace, which is what a
// GitLab item looks like before it is opened for the first time.
func (c Config) RequireItem(item Item) error {
	if c.IsRemote() {
		// The machine that owns the workspace runs this same check on its own vault, and it is
		// the only one that can: nothing about the item is knowable from here.
		return nil
	}
	if !c.itemInVault(item.Name) {
		return fmt.Errorf("%q does not name an item in %s/", item.Name, c.Vault)
	}
	if isDir(c.ItemDir(item.Name)) || c.HasSession(item.Name) {
		return nil
	}
	if repo := item.Repo(); repo != "" && isDir(filepath.Join(c.Workspace, repo)) {
		return nil
	}
	return fmt.Errorf(`no item named %q

Nothing in %s/ has that name, no session is running under it, and it does not name
a repo in this workspace.

Fix by either:
  - create it:  wisp new %s
  - pick from what is already there:  wisp`, item.Name, c.Vault, item.Name)
}

// itemInVault reports whether a name still lands inside the vault once ItemDir joins it on. The
// name is user input here, and a ".." in it would put both the folder and the session somewhere
// the vault does not reach.
func (c Config) itemInVault(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	return strings.HasPrefix(
		filepath.Clean(c.ItemDir(name)),
		filepath.Clean(c.VaultDir())+string(filepath.Separator))
}

// WorktreeFor is the checkout path for one repo of one item. It is a cache: deleting it is
// safe, and reopening the item recreates it from the branch, which is the durable state.
//
// The directory name comes from the workflow's `worktree:` template, which is why the workflow
// is a parameter rather than something resolved in here: the caller almost always has one
// already, and resolving it per repo would read the same three files in a loop.
func (c Config) WorktreeFor(w Workflow, repo string, item Item) string {
	// Checked after substitution, not before. The template is the workflow's, but the values are
	// not: `repo` is whatever an item's orchestration.md says, so even the built-in
	// `{repo}--{slug}` puts the checkout outside the root for `repo: ../../evil`. The guard has
	// to be on the name that comes out.
	name := safeSegment(w.WorktreeName(item, repo))
	if name == "" {
		name = safeSegment(repo + "--" + item.Slug())
	}
	if name == "" {
		// Both the template and the fallback produced something unusable. A hash of the two is
		// still a stable directory for this repo and item, and it is inside the root.
		sum := sha256.Sum256([]byte(repo + "\x00" + item.Name))
		name = "wt-" + hex.EncodeToString(sum[:])[:12]
	}
	return filepath.Join(c.WorktreeRoot(), name)
}

// safeSegment returns the name if it is one ordinary directory name, or "" if it is anything
// that could reach outside the directory it is about to be joined onto.
//
// One predicate, because there are two places where a string somebody else wrote becomes a path:
// a workflow address out of a checked-in .wisp.yaml, and a worktree name out of an item's
// orchestration.md. They had a copy each, differing only in whether they trimmed first, and that
// exact asymmetry is what let a leading space walk past the workflow trust gate once already.
func safeSegment(name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "", name == ".", name == "..":
		return ""
	case strings.ContainsRune(name, filepath.Separator), strings.Contains(name, "/"):
		return ""
	case name != filepath.Clean(name):
		return ""
	}
	return name
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
