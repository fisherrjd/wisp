package wisp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// GitLab describes where remote work items come from. Every field here was hardcoded in the
// original bash, which meant wisp only ever worked for one group and one person.
type GitLab struct {
	Group    string `yaml:"group"`
	Username string `yaml:"username"`
	// RepoPattern recovers a repo directory name from a work item's web URL. It must contain
	// exactly one capturing group. GitLab nests projects arbitrarily, so there is no way to
	// derive this generically; the default matches nothing and disables the remote source.
	RepoPattern string `yaml:"repo_pattern"`
	CacheTTLMin int    `yaml:"cache_ttl_min"`
}

// Config is resolved from, in increasing order of precedence: built-in defaults,
// ~/.config/wisp/config.yaml, <workspace>/.wisp.yaml, then environment variables.
//
// The workspace file wins over the user file on purpose: the workspace is the unit that owns a
// vault, a set of repos and a GitLab group, so its config should travel with it rather than
// living in one global file that a second workspace would have to fight.
type Config struct {
	Workspace string `yaml:"-"`

	// DefaultWorkspace is only meaningful in the user config: it is where wisp goes when it is
	// run from somewhere that is not inside a workspace at all.
	DefaultWorkspace string `yaml:"workspace"`

	Program   string `yaml:"program"`
	Install   bool   `yaml:"install"`
	Vault     string `yaml:"vault"`
	Worktrees string `yaml:"worktrees"`
	Provision string `yaml:"provision"`
	GitLab    GitLab `yaml:"gitlab"`
}

func defaults() Config {
	return Config{
		Program:   "claude",
		Vault:     "working_items",
		Worktrees: ".worktrees",
		Provision: ".claude/scripts/provision-worktree.sh",
		GitLab:    GitLab{CacheTTLMin: 15},
	}
}

// MarkerFile identifies a workspace root during the upward search.
const MarkerFile = ".wisp.yaml"

// UserConfigPath is the per-user config file.
//
// Deliberately XDG rather than os.UserConfigDir: on macOS that returns
// ~/Library/Application Support, but command line tools there conventionally use ~/.config,
// and that is where anyone will look for this file.
func UserConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "wisp", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "wisp", "config.yaml")
}

// Load resolves configuration and locates the workspace. See FindWorkspace for the search.
func Load() (Config, error) {
	c := defaults()

	// The user config is read first because it can name the fallback workspace, which the
	// search below needs.
	_ = c.mergeFile(UserConfigPath())

	ws, err := FindWorkspace(c.DefaultWorkspace, c.Vault)
	if err != nil {
		return c, err
	}
	c.Workspace = ws

	// A parse error in the workspace file is worth reporting: it is the file the user just
	// edited, and silently falling back to defaults would look like wisp ignoring them.
	if err := c.mergeFile(filepath.Join(c.Workspace, MarkerFile)); err != nil && !os.IsNotExist(err) {
		return c, fmt.Errorf("%s: %w", MarkerFile, err)
	}

	if v := os.Getenv("WISP_PROGRAM"); v != "" {
		c.Program = v
	}
	if os.Getenv("WISP_INSTALL") != "" {
		c.Install = true
	}
	return c, nil
}

// FindWorkspace locates the workspace root, in precedence order:
//
//  1. WISP_WORKSPACE, when set. An explicit answer always wins.
//  2. The nearest ancestor of the current directory holding a .wisp.yaml or a vault directory.
//     This is the git approach, and it is what lets `wisp` work from inside a repo or a
//     worktree rather than only from the workspace root. It matters in practice: the tmux
//     popup inherits the current pane's directory, and a wisp session's own worktree windows
//     are several levels below the root.
//  3. A `workspace:` key in ~/.config/wisp/config.yaml, for running wisp from anywhere at all.
//  4. The current directory, so the error message names somewhere the user recognises.
func FindWorkspace(fallback, vault string) (string, error) {
	if ws := os.Getenv("WISP_WORKSPACE"); ws != "" {
		return filepath.Abs(ws)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if found := searchUp(cwd, vault); found != "" {
		return found, nil
	}
	if fallback != "" {
		return filepath.Abs(expandHome(fallback))
	}
	return cwd, nil
}

func searchUp(start, vault string) string {
	dir := start
	for {
		if exists(filepath.Join(dir, MarkerFile)) || isDir(filepath.Join(dir, vault)) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			return ""
		}
		dir = parent
	}
}

// expandHome handles a leading ~ in a configured path, which a hand-edited YAML file will
// almost always contain and which the shell does not get a chance to expand.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func (c *Config) mergeFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, c)
}

func (c Config) VaultDir() string     { return filepath.Join(c.Workspace, c.Vault) }
func (c Config) WorktreeRoot() string { return filepath.Join(c.Workspace, c.Worktrees) }
func (c Config) ProvisionPath() string {
	return filepath.Join(c.Workspace, c.Provision)
}

// CachePath is namespaced by workspace. The bash version used one fixed filename, so pointing
// WISP_WORKSPACE at a second tree served it the first tree's remote items.
func (c Config) CachePath() string {
	sum := sha256.Sum256([]byte(c.Workspace))
	name := fmt.Sprintf("wisp-gitlab-%s.json", hex.EncodeToString(sum[:])[:12])
	return filepath.Join(os.TempDir(), name)
}

// RequireWorkspace fails early with an actionable message rather than letting every later
// operation return an empty list.
func (c Config) RequireWorkspace() error {
	if fi, err := os.Stat(c.VaultDir()); err == nil && fi.IsDir() {
		return nil
	}
	return fmt.Errorf(`no workspace found

wisp searched upward from the current directory for a %s or a %s/ directory
and reached the filesystem root. Resolved to: %s

Fix by any one of:
  - run wisp from inside a workspace (any depth)
  - add `+"`workspace: /path/to/workspace`"+` to %s
  - set WISP_WORKSPACE=/path/to/workspace`,
		MarkerFile, c.Vault, c.Workspace, UserConfigPath())
}
