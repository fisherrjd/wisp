package wisp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

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

// Load resolves configuration for the workspace named by WISP_WORKSPACE, or the current
// directory if that is unset.
func Load() (Config, error) {
	c := defaults()

	ws := os.Getenv("WISP_WORKSPACE")
	if ws == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return c, err
		}
		ws = cwd
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return c, err
	}
	c.Workspace = abs

	if home, err := os.UserConfigDir(); err == nil {
		_ = c.mergeFile(filepath.Join(home, "wisp", "config.yaml"))
	}
	// A parse error in the workspace file is worth reporting: it is the file the user just
	// edited, and silently falling back to defaults would look like wisp ignoring them.
	if err := c.mergeFile(filepath.Join(c.Workspace, ".wisp.yaml")); err != nil && !os.IsNotExist(err) {
		return c, fmt.Errorf(".wisp.yaml: %w", err)
	}

	if v := os.Getenv("WISP_PROGRAM"); v != "" {
		c.Program = v
	}
	if os.Getenv("WISP_INSTALL") != "" {
		c.Install = true
	}
	return c, nil
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
	return fmt.Errorf("workspace vault not found: %s\n    set WISP_WORKSPACE, or add %s to it",
		c.VaultDir(), c.Vault)
}
