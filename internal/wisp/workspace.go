package wisp

import (
	"os"
	"path/filepath"
	"regexp"
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

var itemDirRe = regexp.MustCompile(`^[0-9]+-`)

// skipDirs are vault subdirectories that are infrastructure rather than work.
var skipDirs = map[string]bool{".git": true, ".claude": true, ".obsidian": true}

// LocalItems finds vault folders two levels deep: <repo>/<iid>-<slug>, plus anything under
// _adhoc, which has no iid by definition.
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
			if parent.Name() != "_adhoc" && !itemDirRe.MatchString(child.Name()) {
				continue
			}
			out = append(out, Item{
				Name:  parent.Name() + "/" + child.Name(),
				State: StateFolder,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ItemDir is where an item's notes and manifest live.
func (c Config) ItemDir(item string) string { return filepath.Join(c.VaultDir(), item) }

// WorktreeFor is the checkout path for one repo of one item. It is a cache: deleting it is
// safe, and reopening the item recreates it from the branch, which is the durable state.
func (c Config) WorktreeFor(repo string, item Item) string {
	return filepath.Join(c.WorktreeRoot(), repo+"--"+item.Slug())
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
