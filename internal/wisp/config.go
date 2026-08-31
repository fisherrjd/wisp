package wisp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
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

	// Location is where this workspace lives. For a local one that is just Workspace again; for
	// a remote one it carries the host, and Workspace is the path over there rather than
	// anything that exists here.
	Location Location `yaml:"-"`

	// Hosts is the machines wisp can reach. Each contributes every workspace it holds, so
	// making one over there needs nothing written down here.
	Hosts HostSet `yaml:"hosts"`

	// Workspaces is the set wisp can hop between, name to location. Only meaningful in the user
	// config: a workspace does not get to name its neighbours.
	//
	// Still here alongside Hosts, for a workspace on a machine that has not registered it. A
	// machine's config is a list of what it was told about, not a scan of its disk.
	Workspaces map[string]Location `yaml:"workspaces"`

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
		loc, ok := c.Workspaces[name]
		if !ok {
			// A name on a machine resolves without asking anyone: the host comes from the
			// config and the rest is that machine's own name for the workspace, which it will
			// resolve itself. Nothing here needs to know where it is, which is why adding a
			// machine is enough and adding its workspaces is not.
			if host, remote, isHost := c.splitQualified(name); isHost {
				loc, ok = Location{Host: c.Hosts[host], Name: remote}, true
			}
		}
		if !ok {
			return c, fmt.Errorf("no workspace named %q\n\nknown: %s\nadd a machine under `hosts:` or a workspace under `workspaces:` in %s",
				name, strings.Join(c.WorkspaceNames(), ", "), UserConfigPath())
		}
		if loc.IsRemote() {
			// Nothing further is knowable here. The far side owns the vault, the repos, the
			// .wisp.yaml and the sessions, and every question about them is a question for it.
			c.Name, c.Location, c.Workspace, c.explicit = name, loc, loc.Path, true
			return c, nil
		}
		abs, err := filepath.Abs(expandHome(loc.Path))
		if err != nil {
			return c, err
		}
		c.Workspace = abs
		c.explicit = true
	} else {
		def := c.Workspaces[c.DefaultName()]
		// A remote default cannot be a path to fall back to, and standing outside every local
		// workspace is exactly when it should be used. Resolve it by name instead, which takes
		// the branch above and stops there.
		if def.IsRemote() && os.Getenv("WISP_WORKSPACE") == "" {
			if cwd, err := os.Getwd(); err == nil && searchUp(cwd, c.Vault) == "" {
				return Load(c.DefaultName())
			}
		}
		ws, err := FindWorkspace(def.Path, c.Vault)
		if err != nil {
			return c, err
		}
		c.Workspace = ws
	}
	c.Location = Location{Path: c.Workspace}
	c.resolveName()

	// The workspace set belongs to the user config alone. A workspace naming its neighbours
	// would let one of them rename or hide another, so whatever the file below says about them
	// is discarded rather than merged.
	// Copies, not the maps themselves. yaml.v3 decodes into a non-nil map by adding keys to it,
	// so holding the reference and putting it back afterwards restores nothing: the workspace
	// file's entries are already in the map the reference points at.
	set, hosts := maps.Clone(c.Workspaces), maps.Clone(c.Hosts)
	def, name, explicit := c.Default, c.Name, c.explicit

	// A parse error in the workspace file is worth reporting: it is the file the user just
	// edited, and silently falling back to defaults would look like wisp ignoring them.
	if err := c.mergeFile(filepath.Join(c.Workspace, MarkerFile)); err != nil && !os.IsNotExist(err) {
		return c, fmt.Errorf("%s: %w", MarkerFile, err)
	}
	// Hosts is restored along with the rest: a workspace naming its machines would let one of
	// them add, rename or hide another, which is the same thing the workspace set is protected
	// from and for the same reason.
	c.Workspaces, c.Hosts, c.Default, c.Name, c.explicit = set, hosts, def, name, explicit

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
		c.Workspaces = map[string]Location{}
	}
	if c.DefaultWorkspace != "" {
		loc := ParseLocation(c.DefaultWorkspace)
		name := wsToken(filepath.Base(strings.TrimRight(expandHome(loc.Path), "/")))
		if !c.hasLocation(loc) && name != "" {
			c.Workspaces[name] = loc
		}
		if c.Default == "" {
			c.Default = name
		}
	}
	if c.Default == "" && len(c.Workspaces) > 0 {
		c.Default = c.WorkspaceNames()[0]
	}
}

// hasLocation reports whether the set already holds this workspace. Local paths are compared
// resolved, since ~/work and /Users/x/work are the same directory; remote ones are compared as
// written, because only the far side can say what its own path expands to.
func (c Config) hasLocation(want Location) bool {
	for _, loc := range c.Workspaces {
		if loc.Host != want.Host {
			continue
		}
		if loc.IsRemote() {
			if loc.Path == want.Path {
				return true
			}
			continue
		}
		if filepath.Clean(expandHome(loc.Path)) == filepath.Clean(expandHome(want.Path)) {
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
	for name, loc := range c.Workspaces {
		if loc.IsRemote() {
			continue // a path found by searching this filesystem is never one of those
		}
		if abs, err := filepath.Abs(expandHome(loc.Path)); err == nil && abs == c.Workspace {
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
// A machine contributes its bare name here and no more. What else it holds is only knowable by
// asking it, which belongs in the tally rather than in every command that needs a list of names.
func (c Config) WorkspaceNames() []string {
	out := make([]string, 0, len(c.Workspaces)+len(c.Hosts)+1)
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for n := range c.Workspaces {
		add(n)
	}
	for n := range c.Hosts {
		add(n)
	}
	// The current workspace, unless a machine already stands for it. A name like `eldo/side`
	// arrives through that machine's own list, and adding it here as well would put two rows in
	// the ring for one workspace, which reads as a duplicate and steps like a dead end.
	if _, _, viaHost := c.splitQualified(c.Name); !viaHost {
		add(c.Name)
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
	if c.IsRemote() {
		// Not answerable from here without a round trip. The board probe decides, and until it
		// has, a remote workspace is assumed fine rather than shown as broken.
		return true
	}
	fi, err := os.Stat(c.VaultDir())
	return err == nil && fi.IsDir()
}

// IsRemote reports whether this workspace lives on another machine.
func (c Config) IsRemote() bool { return c.Location.IsRemote() }

// RequireWorkspace fails early with an actionable message rather than letting every later
// operation return an empty list.
//
// Two messages, because there are two ways to get here and they need opposite advice. A
// workspace named outright is a path that does not hold a vault yet, and the fix is to create
// one or to point the config elsewhere. A workspace nobody named is a failed search, and the fix
// is to say where to look.
func (c Config) RequireWorkspace() error {
	// A remote workspace cannot be checked without asking, and asking belongs in the board
	// probe, which already reports a host that will not answer.
	if c.IsRemote() || c.Ready() {
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
