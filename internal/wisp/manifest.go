package wisp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Entry is one repo's worth of intent for an item.
type Entry struct {
	Repo   string `yaml:"repo"`
	Branch string `yaml:"branch"`
	Base   string `yaml:"base"`
}

type frontmatter struct {
	Repos []Entry `yaml:"repos"`
}

// ErrNoManifest means the item has no orchestration.md. Callers fall back to inference rather
// than treating this as a failure.
var ErrNoManifest = errors.New("no manifest")

// Manifest reads the YAML frontmatter of an item's orchestration.md.
//
// It records intent (repo, branch, base) and deliberately not worktree paths: a path is a cache,
// and a deleted worktree should be a cache miss rather than lost state.
//
// The bash version parsed this with hand-rolled awk that understood exactly one layout and
// silently returned nothing for anything else. This is a real YAML parse, so a malformed file
// reports why.
func (c Config) Manifest(item Item) ([]Entry, error) {
	path := filepath.Join(c.ItemDir(item.Name), "orchestration.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.inferManifest(item), nil
		}
		return nil, err
	}
	fm, err := extractFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var parsed frontmatter
	if err := yaml.Unmarshal(fm, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Entry
	for _, e := range parsed.Repos {
		if e.Repo == "" {
			continue
		}
		if e.Branch == "" {
			e.Branch = "feature/" + item.Slug()
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return c.inferManifest(item), nil
	}
	return out, nil
}

// inferManifest handles the common single-repo item that never needed an orchestration.md: the
// repo is the folder's parent and the branch follows the slug. _adhoc items get nothing, which
// is correct: there is no repo to infer, and the session is notes-only.
func (c Config) inferManifest(item Item) []Entry {
	repo := item.Repo()
	if repo == "" {
		return nil
	}
	return []Entry{{Repo: repo, Branch: "feature/" + item.Slug()}}
}

var fence = []byte("---")

// extractFrontmatter returns the bytes between the leading --- fence and the next one. A file
// with no fence has no frontmatter, which is not an error.
func extractFrontmatter(raw []byte) ([]byte, error) {
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), fence) {
		return nil, nil
	}
	for i := 1; i < len(lines); i++ {
		if bytes.Equal(bytes.TrimSpace(lines[i]), fence) {
			return bytes.Join(lines[1:i], []byte("\n")), nil
		}
	}
	return nil, errors.New("frontmatter opened with --- but never closed")
}

// StripFrontmatter returns a markdown body without its frontmatter, for the preview pane. The
// frontmatter is already rendered as the repos block there, so showing it twice wastes space.
func StripFrontmatter(raw []byte) []byte {
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), fence) {
		return raw
	}
	for i := 1; i < len(lines); i++ {
		if bytes.Equal(bytes.TrimSpace(lines[i]), fence) {
			return bytes.Join(lines[i+1:], []byte("\n"))
		}
	}
	return raw
}
