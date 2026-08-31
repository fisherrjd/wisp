package wisp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// wispYAMLTemplate is the workspace config written for a new workspace. Every line is commented
// out, so the file parses to nothing and the defaults still apply. It exists to be edited: the
// keys anyone will want are already here with the right spelling, which is otherwise a trip to
// the README.
const wispYAMLTemplate = `# wisp workspace config. Everything here is optional; these are the defaults.
#
# program: claude --permission-mode auto   # what runs in the agent window
# install: false                           # install dependencies when provisioning
# vault: working_items
# worktrees: .worktrees
#
# gitlab:
#   group: your-group/subgroup
#   username: you
#   # Recovers the repo directory from a work item URL. Exactly one capturing
#   # group, which must be the repo.
#   repo_pattern: '/subgroup/([^/]+)/-/'
#   cache_ttl_min: 15
`

// CreateWorkspace makes a directory into a workspace and registers it under a name.
//
// Without mkdir the directory has to be there already, which covers the two things anyone
// actually does: adopting a tree of repos you have been working in, and registering a vault that
// already exists so hop can reach it. With mkdir it builds the whole path, for a workspace
// starting from nothing.
//
// The default is deliberately the strict one. A mistyped path creates a workspace somewhere
// nobody meant to put one, and the mistake surfaces much later, as a picker with nothing in it
// and no clue why. Asking for -p is one keystroke and makes the intent explicit, the same
// bargain mkdir strikes.
//
// Safe to run twice: an existing vault and an existing .wisp.yaml are both left alone.
func (c Config) CreateWorkspace(name, path string, mkdir bool) (string, error) {
	name = wsToken(name)
	if name == "" {
		return "", fmt.Errorf("a workspace needs a name: wisp ws new <name> [path]")
	}

	// A path with a host in front of it. Registering it here is always the local half; -p also
	// makes it over there, by running the same command on the far side rather than by reaching
	// into its filesystem. Same bargain as locally: without the flag the workspace has to exist
	// already, so a mistyped path fails instead of quietly appearing on another machine.
	if loc := ParseLocation(path); loc.IsRemote() {
		if err := c.checkFree(name, loc); err != nil {
			return "", err
		}
		if mkdir {
			if _, err := loc.runBare("ws", "new", name, "-p", loc.Path); err != nil {
				return "", fmt.Errorf("could not create it on %s: %w", loc.Host, err)
			}
		}
		if err := c.register(name, loc); err != nil {
			return "", err
		}
		return loc.String(), nil
	}

	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return "", err
	}
	switch {
	case isDir(abs):
	case mkdir:
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", fmt.Errorf("could not create %s: %w", abs, err)
		}
	case exists(abs):
		return "", fmt.Errorf("%s exists but is not a directory", abs)
	default:
		return "", fmt.Errorf("%s does not exist\n\nwisp adds a workspace to a directory you already have, so a mistyped path\nfails here rather than becoming a workspace somewhere you never meant.\nTo create it anyway:\n  wisp ws new -p %s %s", abs, name, path)
	}

	if err := c.checkFree(name, Location{Path: abs}); err != nil {
		return "", err
	}

	vault := filepath.Join(abs, c.Vault)
	if err := os.MkdirAll(vault, 0o755); err != nil {
		return "", fmt.Errorf("could not create the vault: %w", err)
	}
	marker := filepath.Join(abs, MarkerFile)
	if !exists(marker) {
		if err := os.WriteFile(marker, []byte(wispYAMLTemplate), 0o644); err != nil {
			return "", fmt.Errorf("could not write %s: %w", MarkerFile, err)
		}
	}
	if err := c.register(name, Location{Path: abs}); err != nil {
		return "", err
	}
	return abs, nil
}

// checkFree refuses both directions of conflict, because they need different answers. A name
// already in use means picking another; a location already registered means the workspace exists
// and the name you wanted is a second alias for it, which the ring would then show twice.
func (c Config) checkFree(name string, want Location) error {
	if existing, ok := c.Workspaces[name]; ok && !sameLocation(existing, want) {
		return fmt.Errorf("workspace %q already points at %s", name, existing)
	}
	for n, loc := range c.Workspaces {
		if n != name && sameLocation(loc, want) {
			return fmt.Errorf("%s is already the workspace %q", want, n)
		}
	}
	return nil
}

func sameLocation(a, b Location) bool {
	if a.Host != b.Host {
		return false
	}
	if a.IsRemote() {
		return a.Path == b.Path
	}
	x, err1 := filepath.Abs(expandHome(a.Path))
	y, err2 := filepath.Abs(expandHome(b.Path))
	return err1 == nil && err2 == nil && x == y
}

// Unregister drops a workspace from the user config.
//
// It touches nothing on disk. The vault, the repos, the worktrees and any session running there
// all stay exactly where they are, and adding the name back restores it. Forgetting a workspace
// and destroying one must not be the same keystroke, which is why there is no flag here to make
// it the second thing.
func (c Config) Unregister(name string) error {
	if _, ok := c.Workspaces[name]; !ok {
		return fmt.Errorf("no workspace named %q", name)
	}
	if name == c.Name {
		return fmt.Errorf("%q is the workspace you are in; hop somewhere else first", name)
	}

	cfgPath := UserConfigPath()
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", cfgPath, err)
	}
	root := documentRoot(&doc)

	removed := false
	if ws := mapValue(root, "workspaces"); ws != nil {
		removed = deleteMapKey(ws, name)
	}
	// Not in the block, so it can only have come from the older single `workspace:` key, which
	// is folded into the set at load under its directory name.
	if !removed {
		deleteMapKey(root, "workspace")
	}
	// Leaving default pointing at something that is gone would make every bare `wisp` run from
	// outside a workspace fail. Dropping the key lets the next load choose again.
	if d := mapValue(root, "default"); d != nil && d.Value == name {
		deleteMapKey(root, "default")
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeConfig(cfgPath, out.Bytes())
}

// deleteMapKey removes a key and its value from a mapping, reporting whether it was there.
func deleteMapKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// Reload re-resolves this config from disk, for after the workspace set has been edited.
//
// By name when the workspace has one in the config, so a picker pinned to a workspace stays
// pinned. By search otherwise, which is how it was resolved in the first place.
func (c Config) Reload() (Config, error) {
	if _, ok := c.Workspaces[c.Name]; ok {
		return Load(c.Name)
	}
	return Load("")
}

// register adds the workspace to the user config, preserving everything already in the file.
//
// Written back through a yaml.Node rather than by marshalling the Config struct. Marshalling
// would emit every defaulted field wisp holds and drop every comment the user wrote, turning a
// one-line addition into a rewrite of a file they own and did not ask to have reformatted.
func (c Config) register(name string, loc Location) error {
	path := loc.String()
	cfgPath := UserConfigPath()
	if cfgPath == "" {
		return fmt.Errorf("cannot locate a config directory to record the workspace in")
	}

	var doc yaml.Node
	raw, err := os.ReadFile(cfgPath)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", cfgPath, err)
		}
	case !os.IsNotExist(err):
		return err
	}

	root := documentRoot(&doc)

	// Nothing to merge into, so add the block rather than re-emitting the file. yaml.v3 keeps
	// comments through a round trip but not blank lines, and reflowing a config someone wrote by
	// hand is a poor trade for adding two lines to the end of it. Once a workspaces block exists,
	// it is one wisp is most likely to have written itself, and the encoder path below takes over.
	if mapValue(root, "workspaces") == nil {
		block := fmt.Sprintf("workspaces:\n  %s: %s\n", name, quoteYAML(path))
		if mapValue(root, "default") == nil && mapValue(root, "workspace") == nil {
			block += fmt.Sprintf("default: %s\n", name)
		}
		return writeConfig(cfgPath, appendBlock(raw, block))
	}
	workspaces := mapValue(root, "workspaces")
	if workspaces == nil {
		workspaces = &yaml.Node{Kind: yaml.MappingNode}
		setMapValue(root, "workspaces", workspaces)
	}
	setMapValue(workspaces, name, &yaml.Node{Kind: yaml.ScalarNode, Value: path})

	// Only when nothing already answers the question. Both `default:` and the older
	// `workspace:` key say which one wisp falls back to, and quietly moving that because a new
	// workspace was added would redirect every bare `wisp` run from outside a workspace.
	if mapValue(root, "default") == nil && mapValue(root, "workspace") == nil {
		setMapValue(root, "default", &yaml.Node{Kind: yaml.ScalarNode, Value: name})
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}

	return writeConfig(cfgPath, out.Bytes())
}

// writeConfig replaces the user config through a temp file, so a failed write never leaves
// behind one that no longer parses and a wisp that will not start.
func writeConfig(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// appendBlock adds a block to a config file, leaving what is already there untouched and
// separated by one blank line.
func appendBlock(raw []byte, block string) []byte {
	body := strings.TrimRight(string(raw), "\n")
	if body == "" {
		return []byte(block)
	}
	return []byte(body + "\n\n" + block)
}

// quoteYAML renders a string as yaml would, so a path holding a colon or a leading character
// yaml treats specially survives being written into a hand-assembled line.
func quoteYAML(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return s
	}
	return strings.TrimRight(string(b), "\n")
}

// documentRoot returns the mapping at the top of a config document, building the document and
// the mapping if the file was empty or absent.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode {
		doc.Kind = yaml.DocumentNode
		doc.Content = nil
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	return doc.Content[0]
}

// mapValue is the value node for a key in a mapping, or nil. A yaml mapping's Content is a flat
// run of alternating keys and values, which is why this is not just an index.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMapValue replaces a key's value in place, or appends the pair when the key is new. In place
// matters: it keeps the key where the user put it, along with any comment attached to it.
func setMapValue(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		val)
}

// NestedIn names an ancestor workspace of a path, or "". Nesting one workspace inside another is
// legal but rarely meant: the upward search stops at the nearest, so running wisp from anywhere
// below finds the inner one and the outer one becomes unreachable from there.
func (c Config) NestedIn(path string) string {
	parent := filepath.Dir(path)
	if parent == path {
		return ""
	}
	found := searchUp(parent, c.Vault)
	if found == "" || !strings.HasPrefix(path, found+string(filepath.Separator)) {
		return ""
	}
	return found
}
