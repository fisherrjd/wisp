package wisp

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Location is where a workspace lives: a path, plus a host when that path is on another machine.
//
// A remote workspace is not a different kind of thing. It is the same workspace with a host in
// front of it, which is why this is one field on Config rather than a parallel set of remote
// commands.
type Location struct {
	Host string `yaml:"host"`
	Path string `yaml:"path"`
}

func (l Location) IsRemote() bool { return l.Host != "" }

func (l Location) String() string {
	if l.IsRemote() {
		return l.Host + ":" + l.Path
	}
	return l.Path
}

// UnmarshalYAML accepts a scalar or a mapping, because both readings are the natural one at
// different moments:
//
//	work: ~/work                  a path
//	desktop: bigbox:~/work        the scp shorthand, which is already in everyone's fingers
//	laptop: {host: mb, path: ~/w} the long form, for when there is more to say
func (l *Location) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*l = ParseLocation(n.Value)
		return nil
	}
	// A distinct type, or decoding the mapping would call this method again forever.
	type plain Location
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*l = Location(p)
	if l.Path == "" {
		return fmt.Errorf("workspace on %q has no path", l.Host)
	}
	return nil
}

// MarshalYAML writes the shorthand back, so a config wisp has edited still reads like one a
// person wrote.
func (l Location) MarshalYAML() (any, error) { return l.String(), nil }

// ParseLocation reads the scp shorthand.
//
// A colon before the first slash means a host. That makes `bigbox:~/work` remote and
// `/var/lib/a:b` local, with no mode flag to set and no reading anyone has to be taught.
func ParseLocation(s string) Location {
	s = strings.TrimSpace(s)
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	if colon > 0 && (slash < 0 || colon < slash) {
		return Location{Host: s[:colon], Path: s[colon+1:]}
	}
	return Location{Path: s}
}

// Same reports whether two locations name the same workspace. Local paths are compared already
// absolute; remote ones are compared as written, since only the far side can resolve them.
func (l Location) Same(other Location) bool {
	return l.Host == other.Host && l.Path == other.Path
}
