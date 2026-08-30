package wisp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// Workspace is the resolved root path and Name is what that workspace is called. The name
	// is not decoration: it namespaces tmux sessions, so two workspaces holding an item with
	// the same slug no longer collide onto one session.
	Workspace string `yaml:"-"`
	Name      string `yaml:"-"`

	// explicit records that the workspace was named outright, by -w or by a hop, rather than
	// found by searching upward from the current directory. Only the error message cares, and it
	// cares a lot: telling someone wisp searched upward and found nothing, when they named the
	// workspace themselves, sends them looking in the wrong place entirely.
	explicit bool `yaml:"-"`

	// Workspaces is the set wisp can hop between, name to path. Only meaningful in the user
	// config: a workspace does not get to name its neighbours.
	Workspaces map[string]string `yaml:"workspaces"`

	// Default names the workspace wisp goes to when it has no better answer, and the one that
	// adopts sessions created before workspaces existed.
	Default string `yaml:"default"`

	// DefaultWorkspace is the single-workspace spelling that predates the map. It is folded
	// into Workspaces at load, so both spellings behave identically from there on.
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

// Load resolves configuration and locates the workspace.
//
// A non-empty name selects a configured workspace outright, skipping the upward search. That is
// what `wisp -w side` and a hop between workspaces both use: the answer is already known, and
// searching from the current directory would find the workspace you are leaving.
func Load(name string) (Config, error) {
	c := defaults()

	// The user config is read first because it holds the workspace set, which the resolution
	// below needs.
	_ = c.mergeFile(UserConfigPath())
	c.normalizeWorkspaces()

	if name != "" {
		path, ok := c.Workspaces[name]
		if !ok {
			return c, fmt.Errorf("no workspace named %q\n\nconfigured: %s\ndefine it under `workspaces:` in %s",
				name, strings.Join(c.WorkspaceNames(), ", "), UserConfigPath())
		}
		abs, err := filepath.Abs(expandHome(path))
		if err != nil {
			return c, err
		}
		c.Workspace = abs
		c.explicit = true
	} else {
		ws, err := FindWorkspace(c.Workspaces[c.DefaultName()], c.Vault)
		if err != nil {
			return c, err
		}
		c.Workspace = ws
	}
	c.resolveName()

	// The workspace set belongs to the user config alone. A workspace naming its neighbours
	// would let one of them rename or hide another, so whatever the file below says about them
	// is discarded rather than merged.
	set, def, name, explicit := c.Workspaces, c.Default, c.Name, c.explicit

	// A parse error in the workspace file is worth reporting: it is the file the user just
	// edited, and silently falling back to defaults would look like wisp ignoring them.
	if err := c.mergeFile(filepath.Join(c.Workspace, MarkerFile)); err != nil && !os.IsNotExist(err) {
		return c, fmt.Errorf("%s: %w", MarkerFile, err)
	}
	c.Workspaces, c.Default, c.Name, c.explicit = set, def, name, explicit

	if v := os.Getenv("WISP_PROGRAM"); v != "" {
		c.Program = v
	}
	if os.Getenv("WISP_INSTALL") != "" {
		c.Install = true
	}
	return c, nil
}

// normalizeWorkspaces folds the older single `workspace:` key into the map and settles which
// entry is the default, so everything downstream sees one shape.
func (c *Config) normalizeWorkspaces() {
	if c.Workspaces == nil {
		c.Workspaces = map[string]string{}
	}
	if c.DefaultWorkspace != "" {
		name := wsToken(filepath.Base(strings.TrimRight(expandHome(c.DefaultWorkspace), "/")))
		if !c.hasPath(c.DefaultWorkspace) && name != "" {
			c.Workspaces[name] = c.DefaultWorkspace
		}
		if c.Default == "" {
			c.Default = name
		}
	}
	if c.Default == "" && len(c.Workspaces) > 0 {
		c.Default = c.WorkspaceNames()[0]
	}
}

func (c Config) hasPath(path string) bool {
	want := filepath.Clean(expandHome(path))
	for _, p := range c.Workspaces {
		if filepath.Clean(expandHome(p)) == want {
			return true
		}
	}
	return false
}

// resolveName names the resolved workspace. A configured entry pointing at this path wins, so
// the name the user chose is the one that appears in session names and in the picker. Anything
// else, including a workspace found by searching upward and never configured at all, is named
// after its directory, which is stable across runs without needing to be written down.
func (c *Config) resolveName() {
	for name, path := range c.Workspaces {
		if abs, err := filepath.Abs(expandHome(path)); err == nil && abs == c.Workspace {
			c.Name = name
			return
		}
	}
	c.Name = wsToken(filepath.Base(c.Workspace))
}

// DefaultName is the workspace wisp falls back to, and the one that adopts sessions created
// before wisp knew about workspaces at all.
func (c Config) DefaultName() string {
	if c.Default != "" {
		return c.Default
	}
	// No workspaces configured means there is exactly one, and it is this one.
	return c.Name
}

// WorkspaceNames is every configured workspace, plus the current one when it was found by
// searching upward and does not appear in the config. Sorted, because this is the order the
// workspace ring walks and it must not depend on map iteration.
func (c Config) WorkspaceNames() []string {
	out := make([]string, 0, len(c.Workspaces)+1)
	seen := false
	for n := range c.Workspaces {
		out = append(out, n)
		if n == c.Name {
			seen = true
		}
	}
	if !seen && c.Name != "" {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// FindWorkspace locates the workspace root, in precedence order:
//
//  1. WISP_WORKSPACE, when set. An explicit answer always wins.
//  2. The nearest ancestor of the current directory holding a .wisp.yaml or a vault directory.
//     This is the git approach, and it is what lets `wisp` work from inside a repo or a
//     worktree rather than only from the workspace root. It matters in practice: a wisp
//     session's own worktree windows are several levels below the root.
//  3. The default workspace's path, for running wisp from anywhere at all.
//  4. The current directory, so the error message names somewhere the user recognises.
func FindWorkspace(fallback, vault string) (string, error) {
	if ws := os.Getenv("WISP_WORKSPACE"); ws != "" {
		return filepath.Abs(expandHome(ws))
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

// Ready reports whether the resolved path is actually a workspace: a directory with a vault in
// it. Naming a workspace in the config does not create one, so the set wisp knows about and the
// set that exists are not the same thing.
func (c Config) Ready() bool {
	fi, err := os.Stat(c.VaultDir())
	return err == nil && fi.IsDir()
}

// RequireWorkspace fails early with an actionable message rather than letting every later
// operation return an empty list.
//
// Two messages, because there are two ways to get here and they need opposite advice. A
// workspace named outright is a path that does not hold a vault yet, and the fix is to create
// one or to point the config elsewhere. A workspace nobody named is a failed search, and the fix
// is to say where to look.
func (c Config) RequireWorkspace() error {
	if c.Ready() {
		return nil
	}
	if c.explicit {
		return fmt.Errorf(`workspace %q is configured but does not exist yet

  %s has no %s/ directory

Fix by either:
  - create it:  wisp ws new -p %s %s
  - point `+"`workspaces: %s:`"+` somewhere else in %s`,
			c.Name, c.Workspace, c.Vault, c.Name, c.Workspace, c.Name, UserConfigPath())
	}
	return fmt.Errorf(`no workspace found

wisp searched upward from the current directory for a %s or a %s/ directory
and reached the filesystem root. Resolved to: %s

Fix by any one of:
  - run wisp from inside a workspace (any depth)
  - make this directory one:  wisp ws new <name>
  - register one you already have:  wisp ws new <name> /path/to/workspace
  - set WISP_WORKSPACE=/path/to/workspace

`+"`wisp ws`"+` lists what is configured, in %s.`,
		MarkerFile, c.Vault, c.Workspace, UserConfigPath())
}
