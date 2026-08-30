package wisp

import (
	"errors"
	"fmt"
)

// Peer is one workspace's live-session tally, for the picker's header.
//
// It exists because the workspace ring is otherwise invisible. The picker shows one workspace at
// a time, so from inside it nothing hints that another one has an agent waiting on an answer,
// and a ring you cannot see is one you never turn.
type Peer struct {
	Name    string
	Live    int
	Attn    int
	Current bool
	// Ready is false for a workspace named in the config whose vault does not exist yet. wisp
	// never creates one, so the configured set and the set that exists can differ, and the
	// difference should be visible here rather than only on the hop that fails.
	Ready bool
	Path  string
	// Unreachable separates a workspace that is not there from one that could not be asked. Both
	// are unusable, but only one of them is fixed by creating a directory.
	Unreachable bool
	Detail      string
}

// Board is what the picker loads without touching the network: this workspace's items, and a
// tally for every workspace.
//
// Both come out of one pass over tmux. They are needed at the same moment and the needs-input
// check is a capture-pane per session, so gathering them separately would pay that cost twice.
type Board struct {
	Items []Item
	Peers []Peer
}

// Local is the fast path: filesystem and tmux only, no network. It is what the picker paints
// first.
//
// Split out from the remote source because that can take most of a second on a cold cache, and
// blocking the whole list on it made the picker feel slow to open when the part that matters
// most, the sessions already running, was available immediately.
func (c Config) Local() (Board, error) {
	all := AllSessions()
	resolveStates(all)
	// One probe per remote workspace, reused for both the list and the header, because for the
	// workspace you are actually in they answer the same question and a second round trip would
	// be pure latency.
	probes := c.probeRemotes(false)

	var items []Item
	var err error
	if c.IsRemote() {
		p, ok := probes[c.Name]
		switch {
		case !ok:
			err = fmt.Errorf("%s is not in the workspace set", c.Name)
		case p.err != nil:
			err = p.err
		default:
			// Wrapper sessions first, then what the far side reports. Both describe the same
			// items and Merge keeps the higher state, but the wrappers are observed here rather
			// than reported, so they are the ones whose name and state win.
			items = MergeAll(sessionItems(c.claim(all)), p.board.AsItems())
			if p.board.Note != "" {
				err = errors.New(p.board.Note)
			}
		}
	} else {
		items, err = c.Items(all)
	}

	// The tally goes back even when the list failed. A host that will not answer should show as
	// a workspace you cannot reach, not as one that vanished.
	return Board{Items: items, Peers: c.tally(all, probes)}, err
}

// Items is this workspace's own items and nothing else: live sessions, then the vault.
//
// Deliberately free of any reference to another workspace. This is what `board` answers with,
// and a board that consulted the workspaces its own machine has configured would walk from host
// to host with nothing to stop it.
func (c Config) Items(all []Session) ([]Item, error) {
	local, err := c.LocalItems()
	if err != nil {
		return nil, err
	}
	// Order is the whole design: live sessions first, then vault folders, then GitLab. Merge
	// keeps the first name it sees for a given identity and the highest state, so a hand-chosen
	// local slug always beats the one derived from a GitLab title, and an item never appears
	// twice under two spellings.
	return MergeAll(sessionItems(c.claim(all)), local), nil
}

// BoardItems is Items with the session scan done for you, for callers outside a picker load.
func (c Config) BoardItems() ([]Item, error) {
	all := AllSessions()
	resolveStates(all)
	return c.Items(all)
}

func sessionItems(sessions []Session) []Item {
	out := make([]Item, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, Item{Name: s.Item, State: s.State})
	}
	return out
}

// Peers is the workspace tallies on their own, for `wisp ws`.
func (c Config) Peers() []Peer {
	all := AllSessions()
	resolveStates(all)
	return c.tally(all, c.probeRemotes(false))
}

// tally groups resolved sessions by workspace. Sessions from before workspaces existed carry no
// workspace of their own and count towards the default, matching who claims them in the picker.
func (c Config) tally(all []Session, probes map[string]probe) []Peer {
	names := c.WorkspaceNames()
	at := make(map[string]int, len(names))
	peers := make([]Peer, 0, len(names))
	for _, n := range names {
		p := Peer{Name: n, Current: n == c.Name, Ready: c.Ready(), Path: c.Location.String()}
		if !p.Current {
			// Loaded rather than guessed at: another workspace can name its vault directory
			// something else in its own .wisp.yaml, and a readiness check against this
			// workspace's name would call it missing when it is only spelled differently.
			if other, err := Load(n); err == nil {
				p.Ready, p.Path = other.Ready(), other.Location.String()
			} else {
				p.Ready = false
			}
		}
		// A remote workspace's counts are the far side's to report, and whether it answered at
		// all is what "ready" means for one. Sessions running there that nothing here is
		// attached to are invisible locally, which is the whole reason for asking.
		if pr, ok := probes[n]; ok {
			if pr.err != nil {
				p.Ready, p.Unreachable, p.Detail = false, true, pr.err.Error()
			} else {
				p.Ready = pr.board.Ready
				p.Live, p.Attn = pr.board.Live, pr.board.Attn
				if !p.Ready {
					p.Detail = "no vault at " + p.Path
				}
			}
			at[n] = len(peers)
			peers = append(peers, p)
			continue
		}
		at[n] = len(peers)
		peers = append(peers, p)
	}

	def := c.DefaultName()
	for _, s := range all {
		ws := s.WS
		if ws == "" {
			ws = def
		}
		i, ok := at[ws]
		if !ok {
			continue // a session belonging to a workspace no longer in the config
		}
		// Remote counts come from the far side, which already knows about every session there,
		// including the ones this machine is attached to. Adding the wrapper would count it
		// twice.
		if _, remote := probes[ws]; remote {
			continue
		}
		peers[i].Live++
		if s.State == StateNeedsInput {
			peers[i].Attn++
		}
	}
	return peers
}

// MergeAll folds later lists into the first, keeping its ordering and precedence. Used to add
// remote items to an already-painted local list.
func MergeAll(lists ...[]Item) []Item {
	byKey := map[string]Item{}
	var order []string
	for _, list := range lists {
		for _, it := range list {
			Merge(byKey, &order, it)
		}
	}
	out := make([]Item, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}
