package wisp

import (
	"errors"
	"sort"
	"sync"
)

// Peer is one workspace's live-session tally, for the picker's header.
//
// It exists because the workspace ring is otherwise invisible. The picker shows one workspace at
// a time, so from inside it nothing hints that another one has an agent waiting on an answer,
// and a ring you cannot see is one you never turn.
type Peer struct {
	// Name is what you hop to: `near`, `eldo`, `eldo/side`. System and Workspace are the same
	// thing split into the two levels above a session, so the picker can show the tree rather
	// than a flat list of names with slashes in them.
	Name      string
	System    string // the machine, or "" for this one
	Workspace string // that machine's own name for it

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
	resolveStates(all, c.NeedsInputMarker())
	// One probe per remote workspace, reused for both the list and the header, because for the
	// workspace you are actually in they answer the same question and a second round trip would
	// be pure latency.
	//
	// The two probe rounds overlap. Each is internally parallel but they used to run one after the
	// other, which made the picker wait the sum rather than the slower of the two: a machine that
	// is asleep costs the ssh connect timeout once per round.
	hosts, probesReady := c.probeHostsAsync()
	probes := c.probeRemotes(false)

	var items []Item
	var err error
	if c.IsRemote() {
		items, err = c.remoteItems(all, probes)
	} else {
		items, err = c.Items(all)
	}

	probesReady()
	// The tally goes back even when the list failed. A host that will not answer should show as
	// a workspace you cannot reach, not as one that vanished.
	return Board{Items: items, Peers: c.tally(all, *hosts, probes)}, err
}

// probeHostsAsync starts the machine probes and hands back the destination plus the wait for it,
// so the caller can get on with the remote-workspace probes rather than queueing behind these.
func (c Config) probeHostsAsync() (*map[string]hostProbe, func()) {
	out := new(map[string]hostProbe)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		*out = c.probeHosts()
	}()
	return out, wg.Wait
}

// remoteItems is what a remote workspace holds: the wrapper sessions attached from here, then
// what the machine that owns it reports.
//
// Wrappers first because Merge keeps the first name and the highest state, and their state is
// observed here rather than reported from the other end.
func (c Config) remoteItems(all []Session, probes map[string]probe) ([]Item, error) {
	wrappers := sessionItems(c.claim(all))
	board, err := c.Location.Board(false)
	if p, ok := probes[c.Name]; ok {
		board, err = p.board, p.err
	}
	if err != nil {
		return wrappers, err
	}
	items := MergeAll(wrappers, board.AsItems())
	if board.Note != "" {
		return items, errors.New(board.Note)
	}
	return items, nil
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
	resolveStates(all, c.NeedsInputMarker())
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
	resolveStates(all, c.NeedsInputMarker())
	// Overlapped for the same reason Local overlaps them: two independent rounds of ssh, and
	// running them in turn makes `wisp ws` and every ring hop wait the sum of both.
	hosts, ready := c.probeHostsAsync()
	probes := c.probeRemotes(false)
	ready()
	return c.tally(all, *hosts, probes)
}

// LocalPeers is the tallies for this machine's own workspaces, asking no other machine.
//
// What `wisp ws --json` answers with. A machine enumerating its own remote workspaces would let
// two of them holding each other enumerate forever, the same trap `board` has a guard for.
func (c Config) LocalPeers() []Peer {
	all := AllSessions()
	resolveStates(all, c.NeedsInputMarker())
	// Registered workspaces only. The one the current directory happens to resolve to is not
	// something this machine was told to hold, and reporting it would put a row in the asking
	// machine's ring for wherever an ssh session happened to land.
	var out []Peer
	for _, p := range c.tally(all, nil, nil) {
		if loc, ok := c.Workspaces[p.Name]; ok && !loc.IsRemote() {
			out = append(out, p)
		}
	}
	return out
}

// tally groups resolved sessions by workspace. Sessions from before workspaces existed carry no
// workspace of their own and count towards the default, matching who claims them in the picker.
func (c Config) tally(all []Session, hosts map[string]hostProbe, probes map[string]probe) []Peer {
	names := c.WorkspaceNames()
	at := make(map[string]int, len(names))
	peers := make([]Peer, 0, len(names))
	for _, n := range names {
		// A machine stands in for whatever it holds, so its bare name is replaced by one row per
		// workspace it reports. The bare name survives only when it cannot be asked, which is
		// the one case where there is nothing better to show.
		if pr, ok := hosts[n]; ok {
			peers = append(peers, c.hostPeers(n, pr)...)
			continue
		}
		p := Peer{Name: n, Workspace: n, Current: n == c.Name, Ready: c.Ready(), Path: c.Location.String()}
		// A workspace written down with a path can still be on another machine, and it belongs
		// under that machine in the tree rather than under this one. Only where it lives decides
		// that, not how it came to be known.
		if loc, ok := c.Workspaces[n]; ok && loc.IsRemote() {
			p.System = HostName(loc.Host)
		}
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
	// This machine first, then one machine at a time. The order is the tree flattened, so the
	// picker can group by walking it once and the ring visits everything on a machine before
	// moving to the next.
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].System != peers[j].System {
			if peers[i].System == "" || peers[j].System == "" {
				return peers[i].System == ""
			}
			return peers[i].System < peers[j].System
		}
		return peers[i].Name < peers[j].Name
	})
	return peers
}

// Systems is the peers grouped by machine, in ring order, with this machine first.
func Systems(peers []Peer) []System {
	var out []System
	for _, p := range peers {
		if len(out) == 0 || out[len(out)-1].Name != p.System {
			out = append(out, System{Name: p.System})
		}
		s := &out[len(out)-1]
		s.Peers = append(s.Peers, p)
		s.Live += p.Live
		s.Attn += p.Attn
		s.Current = s.Current || p.Current
		s.Reachable = s.Reachable || !p.Unreachable
	}
	return out
}

// System is one machine and the workspaces on it: the top of the three levels wisp moves
// between, above workspaces and sessions.
type System struct {
	Name      string // "" for this machine
	Peers     []Peer
	Live      int
	Attn      int
	Current   bool
	Reachable bool
}

// hostPeers turns one machine's answer into rows.
//
// Its default workspace takes the machine's bare name, because a machine usually holds one and
// calling it `eldo/work` when `eldo` would do reads as ceremony. Only the machines with more
// than one need the longer form, and only for the others.
func (c Config) hostPeers(host string, pr hostProbe) []Peer {
	if pr.err != nil {
		return []Peer{{Name: host, System: host, Path: c.Hosts[host], Unreachable: true, Detail: pr.err.Error()}}
	}
	if len(pr.host.Workspaces) == 0 {
		return []Peer{{
			Name: host, System: host, Path: c.Hosts[host], Ready: false,
			Detail: "no workspaces registered on " + c.Hosts[host] + "; make one there, or add it here with a path",
		}}
	}
	out := make([]Peer, 0, len(pr.host.Workspaces))
	for _, ws := range pr.host.Workspaces {
		name := qualify(host, ws.Name, ws.Name == pr.host.Default)
		p := Peer{
			Name:      name,
			System:    host,
			Workspace: ws.Name,
			Current:   name == c.Name,
			Ready:     ws.Ready,
			Path:      c.Hosts[host] + ":" + ws.Path,
			Live:      ws.Live,
			Attn:      ws.Attn,
		}
		if !p.Ready {
			p.Detail = "no vault at " + ws.Path + " on " + c.Hosts[host]
		}
		out = append(out, p)
	}
	return out
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
