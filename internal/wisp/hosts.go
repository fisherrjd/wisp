package wisp

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// HostSet is the machines wisp can reach, name to ssh target.
//
// A host is registered instead of each of its workspaces because the machine already knows what
// it holds. Listing them here as well would be a second copy of a list that only one side owns,
// and the two would drift the moment a workspace was made over there.
type HostSet map[string]string

// UnmarshalYAML accepts a list or a mapping. The list is the common case, where the name is the
// hostname and there is nothing to say; the mapping is for when you want to call it something
// else.
//
//	hosts:
//	  - jade@eldo
//	  - bigbox
//
//	hosts:
//	  box: jade@eldo
func (h *HostSet) UnmarshalYAML(n *yaml.Node) error {
	out := HostSet{}
	switch n.Kind {
	case yaml.SequenceNode:
		for _, item := range n.Content {
			target := strings.TrimSpace(item.Value)
			if target == "" {
				continue
			}
			out[HostName(target)] = target
		}
	case yaml.MappingNode:
		var m map[string]string
		if err := n.Decode(&m); err != nil {
			return err
		}
		for name, target := range m {
			out[wsToken(name)] = target
		}
	default:
		return fmt.Errorf("hosts must be a list or a mapping")
	}
	*h = out
	return nil
}

// HostName is the default name for an ssh target: the machine, without the account in front or
// the domain behind. `jade@eldo.local` is `eldo`, which is what anyone would call it.
func HostName(target string) string {
	if i := strings.LastIndexByte(target, '@'); i >= 0 {
		target = target[i+1:]
	}
	if i := strings.IndexByte(target, ':'); i >= 0 {
		target = target[:i]
	}
	if i := strings.IndexByte(target, '.'); i > 0 {
		target = target[:i]
	}
	return wsToken(target)
}

// HostNames is the configured machines, sorted.
func (c Config) HostNames() []string {
	out := make([]string, 0, len(c.Hosts))
	for n := range c.Hosts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// splitQualified breaks a workspace name into the machine and that machine's own name for the
// workspace.
//
// A bare host name means that machine's default workspace, because one machine usually holds one
// workspace and naming it twice reads as a mistake. The qualified form is only needed by the
// machines that hold more than one.
func (c Config) splitQualified(name string) (host, remote string, ok bool) {
	if h, rest, found := strings.Cut(name, "/"); found {
		if _, known := c.Hosts[h]; known {
			return h, rest, true
		}
		return "", "", false
	}
	if _, known := c.Hosts[name]; known {
		return name, "", true
	}
	return "", "", false
}

// qualify is the reverse: what a workspace on a machine is called here.
func qualify(host, remote string, isDefault bool) string {
	if remote == "" || isDefault {
		return host
	}
	return host + "/" + remote
}
