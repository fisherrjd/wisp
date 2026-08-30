package wisp

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

	byKey := map[string]Item{}
	var order []string

	// Order is the whole design: live sessions first, then local vault folders, then GitLab.
	// Merge keeps the first name it sees for a given identity and the highest state, so a
	// hand-chosen local slug always beats the one derived from a GitLab title, and an item never
	// appears twice under two spellings.
	for _, s := range c.claim(all) {
		Merge(byKey, &order, Item{Name: s.Item, State: s.State})
	}

	local, err := c.LocalItems()
	if err != nil {
		return Board{}, err
	}
	for _, it := range local {
		Merge(byKey, &order, it)
	}

	items := make([]Item, 0, len(order))
	for _, k := range order {
		items = append(items, byKey[k])
	}
	return Board{Items: items, Peers: c.tally(all)}, nil
}

// Peers is the workspace tallies on their own, for `wisp ws`.
func (c Config) Peers() []Peer {
	all := AllSessions()
	resolveStates(all)
	return c.tally(all)
}

// tally groups resolved sessions by workspace. Sessions from before workspaces existed carry no
// workspace of their own and count towards the default, matching who claims them in the picker.
func (c Config) tally(all []Session) []Peer {
	names := c.WorkspaceNames()
	at := make(map[string]int, len(names))
	peers := make([]Peer, 0, len(names))
	for _, n := range names {
		p := Peer{Name: n, Current: n == c.Name, Ready: c.Ready(), Path: c.Workspace}
		if !p.Current {
			// Loaded rather than guessed at: another workspace can name its vault directory
			// something else in its own .wisp.yaml, and a readiness check against this
			// workspace's name would call it missing when it is only spelled differently.
			if other, err := Load(n); err == nil {
				p.Ready, p.Path = other.Ready(), other.Workspace
			} else {
				p.Ready = false
			}
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
