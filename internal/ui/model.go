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
		return nil
	}
	return cfg.Open(*fm.chosen, func(msg string) { fmt.Printf("--- %s\n", msg) })
}

type candidatesMsg struct {
	items []wisp.Item
	err   error
}

type previewMsg struct {
	item string
	body string
}

type model struct {
	cfg wisp.Config

	all      []wisp.Item
	filtered []wisp.Item
	cursor   int
	offset   int

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
	return loadCandidates(m.cfg, false)
}

// loadCandidates runs the three sources off the UI goroutine. The GitLab source can block on
// the network, and blocking the update loop would freeze typing.
func loadCandidates(cfg wisp.Config, refresh bool) tea.Cmd {
	return func() tea.Msg {
		if refresh {
			_ = cfg.RefreshCache()
		}
		items, err := cfg.Candidates()
		return candidatesMsg{items: items, err: err}
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
		m.all = msg.items
		m.applyFilter()
		m.status = ""
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
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "enter":
			if it := m.current(); it != nil {
				m.chosen = it
				return m, tea.Quit
			}

		case "up", "ctrl+p":
			m.move(-1)
			return m, m.previewCmd()

		case "down", "ctrl+n":
			m.move(1)
			return m, m.previewCmd()

		case "ctrl+x":
			if it := m.current(); it != nil {
				_ = wisp.KillSession(it.Name)
				m.loading = true
				m.status = "killed " + it.Name
				return m, loadCandidates(m.cfg, false)
			}

		case "ctrl+r":
			m.loading = true
			m.status = "refreshing"
			return m, loadCandidates(m.cfg, true)

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
