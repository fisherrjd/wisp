package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/fisherrjd/wisp/internal/wisp"
)

// Adaptive colours so the picker works on light and dark terminals. The glyph colours carry
// state, so they are the one place saturation is spent; everything else stays quiet.
var (
	colLive   = lipgloss.AdaptiveColor{Light: "#2F7D52", Dark: "#62BE8A"}
	colAttn   = lipgloss.AdaptiveColor{Light: "#91650E", Dark: "#DCA845"}
	colFolder = lipgloss.AdaptiveColor{Light: "#7C7891", Dark: "#8A86A0"}
	colRemote = lipgloss.AdaptiveColor{Light: "#41648F", Dark: "#7FA3D6"}

	colInk   = lipgloss.AdaptiveColor{Light: "#1B1926", Dark: "#E6E3EF"}
	colSoft  = lipgloss.AdaptiveColor{Light: "#5A5670", Dark: "#9D99B0"}
	colFaint = lipgloss.AdaptiveColor{Light: "#86829B", Dark: "#736F87"}
	colRule  = lipgloss.AdaptiveColor{Light: "#CFCADD", Dark: "#3A3849"}
	colAcc   = lipgloss.AdaptiveColor{Light: "#554CB8", Dark: "#A79EF5"}
)

func glyphColor(s wisp.State) lipgloss.AdaptiveColor {
	switch s {
	case wisp.StateNeedsInput:
		return colAttn
	case wisp.StateLive:
		return colLive
	case wisp.StateFolder:
		return colFolder
	default:
		return colRemote
	}
}

var (
	// The list pane owns the left column; the preview owns the right. Both are given the same
	// explicit height, which is the thing fzf could not be made to do.
	listStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(colRule).
			PaddingRight(1)

	previewStyle = lipgloss.NewStyle().PaddingLeft(2)

	promptStyle = lipgloss.NewStyle().Foreground(colAcc)
	countStyle  = lipgloss.NewStyle().Foreground(colFaint)

	rowStyle    = lipgloss.NewStyle().Foreground(colSoft)
	rowSelected = lipgloss.NewStyle().Foreground(colInk).Bold(true)
	repoStyle   = lipgloss.NewStyle().Foreground(colFaint)
	pointer     = lipgloss.NewStyle().Foreground(colAcc).SetString("▌")

	previewText = lipgloss.NewStyle().Foreground(colSoft)
	titleStyle  = lipgloss.NewStyle().Foreground(colInk).Bold(true)

	// The footer spans the full terminal width, under both panes.
	footerStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(colRule).
			Foreground(colFaint).
			PaddingTop(0)

	keyStyle  = lipgloss.NewStyle().Foreground(colSoft)
	errStyle  = lipgloss.NewStyle().Foreground(colAttn)
	hintStyle = lipgloss.NewStyle().Foreground(colFaint).Italic(true)
)
