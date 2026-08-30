package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/fisherrjd/wisp/internal/wisp"
)

// The list is fixed at 40% so long item names stay readable while the preview still gets the
// majority of the width.
const listFraction = 0.40

// The footer's contents, declared once so the height calculation and the renderer read the
// same list.
var (
	footerStates = []wisp.State{wisp.StateLive, wisp.StateNeedsInput, wisp.StateFolder, wisp.StateRemote}
	footerKeys   = []string{"enter open", "ctrl-n new", "ctrl-w workspace", "ctrl-x kill", "ctrl-r refresh", "esc quit"}
	// The create line has its own keys, since most of the list bindings do not apply while a
	// name is being typed.
	newKeys = []string{"enter create", "esc cancel"}
)

// legendWidth is the legend and key hints laid side by side, used to decide whether the footer
// needs to stack them onto two lines.
func (m model) activeKeys() []string {
	if m.mode == modeNew {
		return newKeys
	}
	return footerKeys
}

func (m model) footerStacks() bool {
	// Measured from the same slices the renderer uses, rather than guessed, so adding a key
	// cannot silently break the height calculation and overflow the terminal.
	legend := 0
	for _, s := range footerStates {
		legend += 2 + len(s.Label()) + 3
	}
	keys := 0
	for _, k := range m.activeKeys() {
		keys += len(k) + 3
	}
	return m.width-legend-keys < 2
}

// footerHeight must agree with renderFooter or the panes will overflow the terminal. Both are
// driven by the same two predicates, so they cannot drift.
func (m model) footerHeight() int {
	h := 1 // the rule above the footer
	if m.footerStacks() {
		h += 2
	} else {
		h++
	}
	if m.status != "" {
		h++
	}
	return h
}

// listWidth and previewWidth are the total widths handed to lipgloss, and they tile the
// terminal exactly. The *Inner variants subtract the border and padding those styles add:
// lipgloss counts both inside Width, so content sized to the outer width gets re-wrapped, and a
// wrapped line pushes every row below it out of alignment with the other pane.
func (m model) listWidth() int {
	w := int(float64(m.width) * listFraction)
	return clamp(w, 24, m.width-20)
}

func (m model) previewWidth() int { return max(20, m.width-m.listWidth()) }

// listStyle draws a right border (1) and pads right (1).
func (m model) listInner() int { return max(1, m.listWidth()-2) }

// previewStyle pads left (2).
func (m model) previewInner() int { return max(1, m.previewWidth()-2) }

// listRows is the height shared by both panes. Deriving one number and handing it to both is
// what keeps them ending on the same row.
func (m model) listRows() int { return max(1, m.height-1-m.footerHeight()) }

func (m model) View() string {
	if m.width == 0 {
		return "" // pre-first-resize; drawing now would flash a wrong layout
	}

	rows := m.listRows()
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		listStyle.Width(m.listWidth()).Height(rows).Render(m.renderList(rows)),
		previewStyle.Width(m.previewWidth()).Height(rows).Render(m.renderPreview(rows)),
	)

	return strings.Join([]string{m.renderPrompt(), body, m.renderFooter()}, "\n")
}

func (m model) renderPrompt() string {
	if m.mode == modeNew {
		// A distinct label, because this line creates rather than filters and the two look
		// identical otherwise.
		left := newLabel.Render(" new ") + " " + m.input + promptStyle.Render("▏")
		right := countStyle.Render("name, or paste a gitlab link")
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}

	count := fmt.Sprintf("%d/%d", len(m.filtered), len(m.all))
	if m.loading {
		count = "…"
	}
	left := promptStyle.Render("› ") + m.query + promptStyle.Render("▏")
	right := countStyle.Render(count)
	if ring := m.renderPeers(); ring != "" {
		right = ring + countStyle.Render("   ") + right
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// renderPeers draws the workspace ring: every workspace, the current one lit, each with a tally
// of what is running there.
//
// This is the only place the cross-workspace view survives. The list itself is scoped to one
// workspace on purpose, but an agent waiting on an answer somewhere else is a reason to hop, so
// the count that matters most, needs-input, is the one that displaces the plain live count.
func (m model) renderPeers() string {
	if len(m.peers) < 2 {
		return "" // nothing to hop to, so the ring is noise
	}
	parts := make([]string, 0, len(m.peers))
	for _, p := range m.peers {
		style := wsOther
		if p.Current {
			style = wsCurrent
		}
		s := style.Render(p.Name)
		switch {
		// Configured but never created. Marked rather than hidden, because a workspace missing
		// from a list you wrote yourself reads as wisp losing it rather than as a path that
		// does not exist. ctrl-w steps over these.
		case !p.Ready:
			s = wsMissing.Render(p.Name + " ✗")
		case p.Attn > 0:
			s += lipgloss.NewStyle().Foreground(colAttn).Render(fmt.Sprintf(" ?%d", p.Attn))
		case p.Live > 0:
			s += lipgloss.NewStyle().Foreground(colLive).Render(fmt.Sprintf(" ●%d", p.Live))
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, wsOther.Render(" · "))
}

func (m model) renderList(rows int) string {
	if len(m.filtered) == 0 {
		if m.loading {
			return hintStyle.Render("  loading…")
		}
		return hintStyle.Render("  no matches")
	}

	var b strings.Builder
	end := min(m.offset+rows, len(m.filtered))
	for i := m.offset; i < end; i++ {
		it := m.filtered[i]
		selected := i == m.cursor

		lead := "  "
		if selected {
			lead = pointer.String() + " "
		}
		glyph := lipgloss.NewStyle().Foreground(glyphColor(it.State)).Render(it.State.Glyph())

		// Dim the repo prefix so the part that differs between rows is what reads first.
		name := it.Name
		style := rowStyle
		if selected {
			style = rowSelected
		}
		if idx := strings.Index(name, "/"); idx >= 0 {
			name = repoStyle.Render(name[:idx+1]) + style.Render(name[idx+1:])
		} else {
			name = style.Render(name)
		}

		line := lead + glyph + " " + name
		b.WriteString(truncate(line, m.listInner()))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) renderPreview(rows int) string {
	if m.preview == "" {
		return hintStyle.Render("no preview")
	}
	lines := strings.Split(m.preview, "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	// Truncate rather than wrap. A wrapped line makes the preview taller than rows and shoves
	// the pane out of alignment with the list beside it.
	w := m.previewInner()
	for i, l := range lines {
		lines[i] = previewText.Render(truncate(sanitizeLine(l), w))
	}
	// Pad to exactly rows so the block's height never depends on the item being previewed.
	// Without this the layout visibly shifts while moving down the list, because a short note
	// and a long one produce different heights before lipgloss gets a chance to pad.
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m model) renderFooter() string {
	legend := []string{}
	for _, s := range footerStates {
		legend = append(legend,
			lipgloss.NewStyle().Foreground(glyphColor(s)).Render(s.Glyph())+" "+keyStyle.Render(s.Label()))
	}

	left := strings.Join(legend, "   ")
	right := keyStyle.Render(strings.Join(m.activeKeys(), "   "))

	// Stacking when it does not fit, rather than truncating, is the whole reason for leaving
	// fzf, whose header could only ever truncate.
	var bar string
	if m.footerStacks() {
		bar = left + "\n" + right
	} else {
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		bar = left + strings.Repeat(" ", gap) + right
	}

	// The status line sits above the legend rather than replacing it. A missing gitlab config
	// persists for the whole session, and swapping out the legend for it would trade one piece
	// of permanently missing information for another.
	if m.status != "" {
		bar = errStyle.Render(truncate(m.status, m.width)) + "\n" + bar
	}
	return footerStyle.Width(m.width).Render(bar)
}

// sanitizeLine makes a line of arbitrary file content safe to measure and print.
//
// Tabs are the important case: lipgloss.Width counts a tab as one cell but a terminal renders
// it as up to eight, so a tabbed markdown line passes the truncation check and then wraps
// anyway. That is what made the layout jump while scrolling the list.
//
// Other C0 control characters are dropped outright: they would move the cursor or start an
// escape sequence, and nothing in a notes file needs them.
func sanitizeLine(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// truncate cuts a possibly-styled string to a display width, accounting for ANSI escapes.
func truncate(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
