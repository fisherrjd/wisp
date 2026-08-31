package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/fisherrjd/wisp/internal/wisp"
)

// Run starts the picker. It returns the chosen item, if any, by opening it directly: the
// program exits into tmux, so there is no value to hand back to a caller.
func Run(cfg wisp.Config) error {
	m := newModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}
	fm := final.(model)
	switch {
	case fm.chosen != nil:
		return cfg.Open(*fm.chosen, func(msg string) { fmt.Printf("--- %s\n", msg) })
	case fm.hop != "":
		// The home session's loop redraws the picker behind us as soon as this returns, so the
		// workspace we left is still warm when we hop back to it.
		return cfg.Hop(fm.hop)
	default:
		return cfg.LeaveHome()
	}
}

// Two messages, not one: local candidates paint immediately, remote ones fold in when the
// network answers. Waiting for both before showing anything made opening the picker feel slow
// for the sake of the least important rows.
type candidatesMsg struct {
	board wisp.Board
}

type remoteMsg struct {
	items []wisp.Item
	err   error
}

type previewMsg struct {
	item string
	body string
}

// mode is which line the typed characters go to. The picker has exactly two states, and
// keeping them explicit avoids the usual pile of booleans.
type mode int

const (
	modeFilter    mode = iota // typing narrows the list
	modeNew                   // typing names a new item, or pastes a GitLab URL
	modeWorkspace             // the list is the machine and workspace tree, not items
	modeNewWS                 // typing names a new workspace
	modeNewHost               // typing names a machine to reach
)

type model struct {
	cfg wisp.Config

	// local and remote are kept apart so a remote refresh never drops locally-known items,
	// and so the two can arrive independently. all is the merged view the list renders.
	local    []wisp.Item
	remote   []wisp.Item
	all      []wisp.Item
	filtered []wisp.Item
	cursor   int
	offset   int

	// peers is every workspace's live tally, shown in the header and, in workspace mode, as the
	// list itself. Without it the workspace ring is invisible from inside any one of its members.
	peers    []wisp.Peer
	wsCursor int

	mode  mode
	input string // the new-item line, kept separate so cancelling restores the filter intact

	query   string
	preview string
	// previewFor guards against a slow preview landing after the cursor has moved on.
	previewFor string

	width, height int
	loading       bool
	status        string
	chosen        *wisp.Item
	// hop is the workspace to move to once the picker exits, set by ctrl-w. Like chosen, the
	// move happens after Run returns, because it replaces this process with tmux.
	hop string
}

func newModel(cfg wisp.Config) model {
	return model{cfg: cfg, loading: true, status: "loading"}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadLocal(m.cfg), loadRemote(m.cfg, false))
}

// loadLocal is the fast path: filesystem and tmux only, no network.
func loadLocal(cfg wisp.Config) tea.Cmd {
	return func() tea.Msg {
		board, _ := cfg.Local()
		return candidatesMsg{board: board}
	}
}

// loadRemote runs off the UI goroutine, since it can block on the network for most of a second
// on a cold cache and blocking the update loop would freeze typing.
func loadRemote(cfg wisp.Config, refresh bool) tea.Cmd {
	return func() tea.Msg {
		if refresh {
			_ = cfg.RefreshCache()
		}
		items, err := cfg.GitLabItems()
		return remoteMsg{items: items, err: err}
	}
}

func loadPreview(cfg wisp.Config, it wisp.Item, width int) tea.Cmd {
	return func() tea.Msg {
		return previewMsg{item: it.Name, body: cfg.Preview(it, width)}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.previewCmd()

	case candidatesMsg:
		m.loading = false
		m.local = msg.board.Items
		m.peers = msg.board.Peers
		if m.wsCursor >= len(m.peers) {
			m.wsCursor = max(0, len(m.peers)-1)
		}
		m.all = wisp.MergeAll(m.local, m.remote)
		m.applyFilter()
		return m, m.previewCmd()

	case remoteMsg:
		m.remote = msg.items
		m.all = wisp.MergeAll(m.local, m.remote)
		m.applyFilter()
		if msg.err != nil {
			// Remote failures are shown, not swallowed: an empty "+" section otherwise looks
			// like having no assigned items rather than a broken query.
			m.status = msg.err.Error()
		}
		return m, m.previewCmd()

	case previewMsg:
		if msg.item == m.previewFor {
			m.preview = msg.body
		}
		return m, nil

	case tea.KeyMsg:
		// Each mode owns every key while it is open. For the typed lines that is what lets a URL
		// or a path containing characters that are bindings elsewhere still type through cleanly.
		switch m.mode {
		case modeNew:
			return m.updateNew(msg)
		case modeWorkspace:
			return m.updateWorkspace(msg)
		case modeNewWS:
			return m.updateNewWS(msg)
		case modeNewHost:
			return m.updateNewHost(msg)
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "ctrl+n":
			m.mode = modeNew
			m.input = ""
			m.status = ""
			return m, nil

		case "enter":
			if it := m.current(); it != nil {
				m.chosen = it
				return m, tea.Quit
			}

		// ctrl+j / ctrl+k rather than the emacs ctrl+p / ctrl+n, because ctrl+n is wanted for
		// creating an item and splitting the pair across two idioms reads worse than moving
		// both.
		case "up", "ctrl+k":
			m.move(-1)
			return m, m.previewCmd()

		case "down", "ctrl+j":
			m.move(1)
			return m, m.previewCmd()

		case "ctrl+x":
			if it := m.current(); it != nil {
				// Refuse to kill the session wisp is drawn on. tmux tears the client down with
				// the session, so from a popup this looks like wisp crashing rather than like
				// the kill succeeding. Running `wisp home` keeps the picker in its own session,
				// where this cannot come up.
				if m.cfg.IsCurrentSession(it.Name) {
					m.status = "that is the session you are in; switch away first, or use wisp home"
					return m, nil
				}
				_ = m.cfg.KillSession(it.Name)
				m.status = "killed " + it.Name
				// Local only: a kill changes tmux state, not GitLab, and re-querying the
				// network here would stall the list for no new information.
				return m, loadLocal(m.cfg)
			}

		// The outer ring, as a list rather than a blind step. Cycling is right for a tmux
		// binding, where one key is the whole interface; here there is a screen to put the
		// workspaces on, so you can see which one has an agent waiting and go straight to it.
		case "ctrl+w":
			m.mode = modeWorkspace
			m.status = ""
			// On the workspace you are in, not at the top. Row indices count the machine
			// headers too, so this cannot be the position in the peer list.
			m.wsCursor = 0
			for i, r := range m.wsRows() {
				if r.Peer != nil && r.Peer.Current {
					m.wsCursor = i
				}
			}
			return m, nil

		case "ctrl+r":
			m.status = "refreshing gitlab"
			return m, tea.Batch(loadLocal(m.cfg), loadRemote(m.cfg, true))

		case "backspace":
			if m.query != "" {
				m.query = m.query[:len(m.query)-1]
				m.applyFilter()
				return m, m.previewCmd()
			}

		case "ctrl+u":
			m.query = ""
			m.applyFilter()
			return m, m.previewCmd()

		default:
			// Bubble Tea coalesces characters that arrive in the same read into a single
			// KeyRunes message, so a fast typist or a paste delivers "abc" in one event, not
			// three. Append all of them: checking for a single rune silently drops the rest.
			switch msg.Type {
			case tea.KeyRunes:
				m.query += string(msg.Runes)
			case tea.KeySpace:
				m.query += " "
			default:
				return m, nil
			}
			m.applyFilter()
			return m, m.previewCmd()
		}
	}
	return m, nil
}

// updateNew handles the create line. Enter builds the item and opens it, which is the same
// path a normal selection takes, so a new item lands in a session exactly like an existing one.
func (m model) updateNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeFilter
		m.input = ""
		m.status = ""
		return m, nil

	case "enter":
		it, err := m.cfg.NewItem(m.input)
		if err != nil {
			// Stay on the line with the text intact: these errors are things the user can
			// correct in place, like a URL for an item that is not assigned to them.
			m.status = err.Error()
			return m, nil
		}
		m.chosen = &it
		return m, tea.Quit

	case "backspace":
		if m.input != "" {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.input = ""
		return m, nil

	default:
		switch msg.Type {
		case tea.KeyRunes:
			m.input += string(msg.Runes)
		case tea.KeySpace:
			m.input += " "
		}
		return m, nil
	}
}

func (m *model) move(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	// Keep the cursor inside the visible window.
	rows := m.listRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
}

func (m *model) applyFilter() {
	if m.query == "" {
		m.filtered = m.all
	} else {
		names := make([]string, len(m.all))
		for i, it := range m.all {
			names[i] = it.Name
		}
		matches := fuzzy.Find(m.query, names)
		m.filtered = make([]wisp.Item, 0, len(matches))
		for _, mt := range matches {
			m.filtered = append(m.filtered, m.all[mt.Index])
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
	m.offset = 0
	if m.cursor >= m.listRows() {
		m.offset = m.cursor - m.listRows() + 1
	}
}

// updateWorkspace handles the tree: pick a workspace, make one, add a machine, forget either.
func (m model) updateWorkspace(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.wsRows()
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+w":
		m.mode = modeFilter
		m.status = ""
		return m, nil

	case "up", "k", "ctrl+k":
		if m.wsCursor > 0 {
			m.wsCursor--
		}
		return m, nil

	case "down", "j", "ctrl+j":
		if m.wsCursor < len(rows)-1 {
			m.wsCursor++
		}
		return m, nil

	// A whole machine at a time. Up and down walk one row, which on a machine holding several
	// workspaces means several presses to get past it; left and right are the level above.
	case "left", "h":
		m.wsCursor = m.systemStep(rows, -1)
		return m, nil

	case "right", "l":
		m.wsCursor = m.systemStep(rows, 1)
		return m, nil

	case "enter":
		// A machine stands for its default workspace, which is the one thing going to a machine
		// can mean.
		p := m.target(rows)
		switch {
		case p == nil:
			return m, nil
		case p.Current:
			m.mode = modeFilter
			return m, nil
		case !p.Ready:
			// Named outright, so say why rather than skipping to one that works. The user
			// pointed at this row.
			if p.Detail != "" {
				m.status = p.Name + ": " + p.Detail
			} else {
				m.status = p.Name + " does not exist yet: " + p.Path
			}
			return m, nil
		}
		m.hop = p.Name
		return m, tea.Quit

	// Plain letters, because nothing here types: the tree is a list, not a filter box. Chords
	// would also be a lottery, since wisp lives inside tmux and whichever chord someone has
	// chosen as their prefix never arrives. ctrl-a is a common one and reaches nothing.
	//
	// Two different things, so two keys. A machine is an ssh target and gets you everything on
	// it; a workspace is a directory on one machine.
	case "n", "ctrl+n":
		m.mode = modeNewWS
		m.input = ""
		m.status = ""
		return m, nil

	case "a":
		m.mode = modeNewHost
		m.input = ""
		m.status = ""
		return m, nil

	// Forgets, and only that: nothing on disk is touched and any session running there keeps
	// running. The same key kills a session in the item list, which is a heavier thing, so the
	// footer says "forget" here rather than "kill".
	case "x", "ctrl+x":
		name := m.forgetTarget(rows)
		if name == "" {
			return m, nil
		}
		if err := m.cfg.Unregister(name); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.status = "forgot " + name + " (nothing on disk was touched)"
		return m, m.reload()
	}
	return m, nil
}

// wsRow is one line of the tree: a machine, or a workspace on one.
//
// Machines are rows rather than decoration because they are a thing you act on: going to one
// means its default workspace, forgetting one drops everything it holds, and a new workspace
// belongs to whichever machine the cursor is in.
type wsRow struct {
	System string
	Head   *wisp.System
	Peer   *wisp.Peer
}

func (m model) wsRows() []wsRow {
	var rows []wsRow
	for _, sys := range wisp.Systems(m.peers) {
		s := sys
		rows = append(rows, wsRow{System: sys.Name, Head: &s})
		for i := range sys.Peers {
			p := sys.Peers[i]
			rows = append(rows, wsRow{System: sys.Name, Peer: &p})
		}
	}
	return rows
}

func (m model) row(rows []wsRow) *wsRow {
	if m.wsCursor < 0 || m.wsCursor >= len(rows) {
		return nil
	}
	return &rows[m.wsCursor]
}

// target is the workspace the cursor means. On a machine that is its default one, which is the
// first row under it.
func (m model) target(rows []wsRow) *wisp.Peer {
	r := m.row(rows)
	switch {
	case r == nil:
		return nil
	case r.Peer != nil:
		return r.Peer
	case len(r.Head.Peers) > 0:
		return &r.Head.Peers[0]
	}
	return nil
}

// forgetTarget is what ctrl-x drops. On a machine that is the machine itself, which is the only
// entry there is: the workspaces under it were never registered here.
func (m model) forgetTarget(rows []wsRow) string {
	r := m.row(rows)
	switch {
	case r == nil:
		return ""
	case r.Head != nil:
		return r.Head.Name // "" for this machine, which Unregister refuses by name
	default:
		return r.Peer.Name
	}
}

// updateNewWS handles the create line: `<name> [path]`, with -p to create the directory.
func (m model) updateNewWS(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeWorkspace
		m.input = ""
		m.status = ""
		return m, nil

	case "enter":
		name, path, mkdir := parseWorkspaceLine(m.input)
		if name == "" {
			m.status = "give it a name, and a path"
			return m, nil
		}
		if path == "" {
			// The picker runs at the workspace root, so defaulting to the current directory
			// here would only ever re-register the workspace you are already in.
			m.status = "give it a path: `" + name + " ~/somewhere`"
			return m, nil
		}
		// The path is on whichever machine the cursor is in. That is what the tree is for: a
		// workspace made while looking at a machine belongs to that machine, with no host
		// prefix to remember and no way to typo it.
		if sys := m.currentSystem(); sys != "" {
			path = m.cfg.Hosts[sys] + ":" + path
		}
		summary, err := m.cfg.CreateWorkspace(name, path, mkdir)
		if err != nil {
			// Stay on the line with the text intact: a missing directory is fixed by adding -p,
			// which is one keystroke from here.
			m.status = firstLine(err.Error())
			return m, nil
		}
		m.mode = modeWorkspace
		m.input = ""
		m.status = name + ": " + summary
		return m, m.reload()

	case "backspace":
		if m.input != "" {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.input = ""
		return m, nil

	default:
		switch msg.Type {
		case tea.KeyRunes:
			m.input += string(msg.Runes)
		case tea.KeySpace:
			m.input += " "
		}
		return m, nil
	}
}

// updateNewHost handles the add-machine line: `<target>`, or `<name> <target>` to call it
// something other than its hostname.
func (m model) updateNewHost(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeWorkspace
		m.input = ""
		m.status = ""
		return m, nil

	case "enter":
		fields := strings.Fields(m.input)
		var name, target string
		switch len(fields) {
		case 0:
			m.status = "give it an ssh target, like `jade@eldo`"
			return m, nil
		case 1:
			target = fields[0] // named after the machine
		default:
			name, target = fields[0], fields[1]
		}
		summary, err := m.cfg.AddHost(name, target)
		if err != nil {
			m.status = firstLine(err.Error())
			return m, nil
		}
		m.mode = modeWorkspace
		m.input = ""
		m.status = summary
		return m, m.reload()

	case "backspace":
		if m.input != "" {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.input = ""
		return m, nil

	default:
		switch msg.Type {
		case tea.KeyRunes:
			m.input += string(msg.Runes)
		case tea.KeySpace:
			m.input += " "
		}
		return m, nil
	}
}

// currentSystem is the machine the cursor is in, or "" for this one.
func (m model) currentSystem() string {
	if r := m.row(m.wsRows()); r != nil {
		return r.System
	}
	return ""
}

// parseWorkspaceLine splits `[-p] <name> [path]`. The flag is accepted anywhere, since it reads
// as naturally after the path as before the name.
func parseWorkspaceLine(s string) (name, path string, mkdir bool) {
	var words []string
	for _, f := range strings.Fields(s) {
		if f == "-p" || f == "--parents" {
			mkdir = true
			continue
		}
		words = append(words, f)
	}
	if len(words) > 0 {
		name = words[0]
	}
	if len(words) > 1 {
		path = strings.Join(words[1:], " ")
	}
	return name, path, mkdir
}

// firstLine keeps a multi-line error to what fits on the status line. The full text is on the
// command line version of the same operation.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// reload re-reads the config and the board after the workspace set has changed, so the list
// reflects the edit without leaving the picker.
func (m *model) reload() tea.Cmd {
	if cfg, err := m.cfg.Reload(); err == nil {
		m.cfg = cfg
	}
	return loadLocal(m.cfg)
}

// systemStep moves the cursor to the first workspace of the next or previous machine, wrapping.
// Landing on the first rather than the same position within it: the machines hold different
// numbers of workspaces, so there is no same position to keep.
func (m model) systemStep(rows []wsRow, delta int) int {
	var heads []int
	at := 0
	for i, r := range rows {
		if r.Head == nil {
			continue
		}
		if i <= m.wsCursor {
			at = len(heads)
		}
		heads = append(heads, i)
	}
	if len(heads) < 2 {
		return m.wsCursor
	}
	// Positive modulo: Go's % keeps the sign of the dividend, so -1 % n is -1, not n-1.
	return heads[((at+delta)%len(heads)+len(heads))%len(heads)]
}

func (m model) peer() *wisp.Peer {
	if m.wsCursor < 0 || m.wsCursor >= len(m.peers) {
		return nil
	}
	p := m.peers[m.wsCursor]
	return &p
}

func (m model) current() *wisp.Item {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	it := m.filtered[m.cursor]
	return &it
}

func (m *model) previewCmd() tea.Cmd {
	it := m.current()
	if it == nil {
		m.preview = ""
		m.previewFor = ""
		return nil
	}
	m.previewFor = it.Name
	return loadPreview(m.cfg, *it, m.previewInner())
}
