package ui

import (
	"fmt"

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
	if fm.chosen == nil {
		return wisp.LeaveHome()
	}
	return cfg.Open(*fm.chosen, func(msg string) { fmt.Printf("--- %s\n", msg) })
}

// Two messages, not one: local candidates paint immediately, remote ones fold in when the
// network answers. Waiting for both before showing anything made opening the picker feel slow
// for the sake of the least important rows.
type candidatesMsg struct {
	items []wisp.Item
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
	modeFilter mode = iota // typing narrows the list
	modeNew                // typing names a new item, or pastes a GitLab URL
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
		items, _ := cfg.LocalCandidates()
		return candidatesMsg{items: items}
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
		m.local = msg.items
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
		// The new-item line owns every key while it is open, so a URL containing characters
		// that are bindings elsewhere still types through cleanly.
		if m.mode == modeNew {
			return m.updateNew(msg)
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
				if wisp.IsCurrentSession(it.Name) {
					m.status = "that is the session you are in; switch away first, or use wisp home"
					return m, nil
				}
				_ = wisp.KillSession(it.Name)
				m.status = "killed " + it.Name
				// Local only: a kill changes tmux state, not GitLab, and re-querying the
				// network here would stall the list for no new information.
				return m, loadLocal(m.cfg)
			}

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
