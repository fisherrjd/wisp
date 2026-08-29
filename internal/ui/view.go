package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/fisherrjd/wisp/internal/wisp"
)

// Layout constants. The list is fixed at 40% so long item names stay readable while the preview
// still gets the majority of the width.
const (
	listFraction = 0.40
	chromeRows   = 4 // prompt, count, footer rule, footer text
)

func (m model) listWidth() int {
	w := int(float64(m.width) * listFraction)
	return clamp(w, 24, m.width-20)
}

func (m model) previewWidth() int { return max(20, m.width-m.listWidth()-3) }

// listRows is the height shared by both panes. Deriving one number and giving it to both is
// what keeps them ending on the same row.
func (m model) listRows() int { return max(1, m.height-chromeRows) }

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
	count := fmt.Sprintf("%d/%d", len(m.filtered), len(m.all))
	if m.loading {
		count = "…"
	}
	left := promptStyle.Render("› ") + m.query + promptStyle.Render("▏")
	right := countStyle.Render(count)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
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
		b.WriteString(truncate(line, m.listWidth()-1))
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
	w := m.previewWidth()
	for i, l := range lines {
		lines[i] = previewText.Render(truncate(l, w))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderFooter() string {
	if m.status != "" {
		return footerStyle.Width(m.width).Render(errStyle.Render(m.status))
	}

	legend := []string{}
	for _, s := range []wisp.State{wisp.StateLive, wisp.StateNeedsInput, wisp.StateFolder, wisp.StateRemote} {
		legend = append(legend,
			lipgloss.NewStyle().Foreground(glyphColor(s)).Render(s.Glyph())+" "+keyStyle.Render(s.Label()))
	}
	keys := []string{"enter open", "ctrl-x kill", "ctrl-r refresh", "esc quit"}

	left := strings.Join(legend, "   ")
	right := keyStyle.Render(strings.Join(keys, "   "))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		// Too narrow to sit side by side: stack instead of truncating, which is the failure the
		// fzf version could not avoid.
		return footerStyle.Width(m.width).Render(left + "\n" + right)
	}
	return footerStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

// truncate cuts a possibly-styled string to a display width, accounting for ANSI escapes.
func truncate(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
