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
// legendEntry is one glyph and what it means. The workspace list reuses the item list's glyphs
// for the same feelings one layer up, so the legend has to change with the mode: a "●" that
// means a running session and a "●" that means a workspace holding one are not the same claim.
type legendEntry struct {
	glyph  string
	label  string
	colour lipgloss.AdaptiveColor
}

var wsLegend = []legendEntry{
	{"●", "running", colLive},
	{"?", "waiting", colAttn},
	{"○", "idle", colFolder},
	{"✗", "missing", colFaint},
	{"⚠", "unreachable", colAttn},
}

// wsGlyph is the one place a workspace's state becomes a mark, so the header and the list cannot
// disagree about what a workspace looks like.
//
// Unreachable is separate from missing on purpose. Both are unusable, but one is fixed by
// creating a directory and the other by fixing ssh, and a single glyph for both would send you
// after the wrong one.
func wsGlyph(p wisp.Peer) (string, lipgloss.AdaptiveColor) {
	switch {
	case p.Unreachable:
		return "⚠", colAttn
	case !p.Ready:
		return "✗", colFaint
	case p.Attn > 0:
		return "?", colAttn
	case p.Live > 0:
		return "●", colLive
	default:
		return "○", colFolder
	}
}

var (
	footerStates = []wisp.State{wisp.StateLive, wisp.StateNeedsInput, wisp.StateFolder, wisp.StateRemote}
	// The create line has its own keys, since most of the list bindings do not apply while a
	// name is being typed.
	newKeys = []string{"enter create", "esc cancel"}
	// The first-run question. esc is "later" rather than "cancel" because the question is not
	// dismissed, it is deferred: the built-in keeps running and the server remembers the answer.
	bindKeys = []string{"enter bind it here", "esc later"}
	// Workspace mode. ctrl-x is "forget" rather than "kill": it edits the config and leaves
	// every file and every session alone, and calling both of them kill would be a lie about
	// one of them.
	wsKeys     = []string{"enter go", "←→ machine", "n workspace", "a machine", "x forget", "esc back"}
	newWSKey   = []string{"enter create", "esc cancel", "-p to create the directory"}
	newHostKey = []string{"enter add", "esc cancel"}
	// The closing line. ctrl-d is on it because it is the key that opened it, and pressing it
	// again is the least surprising way to say "there is nothing to write".
	closeKeys = []string{"enter close it out", "ctrl-d close it bare", "esc cancel"}
)

// legendWidth is the legend and key hints laid side by side, used to decide whether the footer
// needs to stack them onto two lines.
func (m model) activeKeys() []string {
	switch m.mode {
	case modeNew, modeNewRepo:
		return newKeys
	case modeBindWorkflow:
		return bindKeys
	case modeWorkspace:
		return wsKeys
	case modeNewWS:
		return newWSKey
	case modeNewHost:
		return newHostKey
	case modeClose:
		return closeKeys
	case modeHelp:
		// The page is the key list. Repeating it underneath would be the only footer on screen
		// that says less than the thing above it.
		return []string{"esc back"}
	}
	return m.itemKeys()
}

// itemKeys is the item list's hints, worded for what the next press will do rather than for the
// state the list is in, and listing only the presses that would do anything.
//
// All eight at once came to 120 columns, wider than an ordinary terminal, so footerLines
// stacked them onto a second row for most people most of the time. The bar was the busiest
// thing on a screen whose whole point is a calm list.
//
// So the footer carries the three verbs, the way out, and the way to everything else, and
// nothing else carries it: five hints, 62 columns, which keeps it on one line beside the legend
// down to a 108-column terminal. ctrl-x, ctrl-w, ctrl-r and the tree's own letters live on the
// ctrl-g page, which exists precisely so this line does not have to be a manifest.
//
// ctrl-w is the one that hurts to drop, since the workspace ring is half of what wisp is. The
// header above already carries that: it names the other machines and tallies what is waiting on
// each. A row of hints repeating that the tree exists is not what makes it discoverable.
//
// Every key still works when it is not listed. Only the advertising is conditional, and it
// costs nothing but the hint.
func (m model) itemKeys() []string {
	it := m.current()
	keys := make([]string, 0, 7)

	// enter and ctrl-d both act on the highlighted row, so neither means anything without one.
	if it != nil {
		keys = append(keys, "enter open")
	}
	keys = append(keys, "ctrl-n new")
	if it != nil {
		keys = append(keys, "ctrl-d done")
	}
	// ctrl-t is the way back from a mistaken ctrl-d, so it appears the moment there is something
	// to come back to and stays while the closed-out items are on screen. This is the one hint
	// that must never wait for the user to already know about it, which is why it is here and
	// not only on the ctrl-g page.
	switch {
	case m.showDone:
		keys = append(keys, "ctrl-t hide closed")
	case m.hasDone:
		keys = append(keys, "ctrl-t show closed")
	}
	// Unconditional, because it is the answer to "what else is there", and a hint that only
	// appeared once you already knew would be answering nobody.
	return append(keys, "ctrl-g keys", "esc quit")
}

// activeLegend is the glyph key for the list currently on screen.
func (m model) activeLegend() []legendEntry {
	if m.mode == modeHelp {
		return nil // the page has its own, spelled out
	}
	if m.mode == modeWorkspace || m.mode == modeNewWS || m.mode == modeNewHost {
		return wsLegend
	}
	out := make([]legendEntry, 0, len(footerStates)+1)
	for _, s := range footerStates {
		out = append(out, legendEntry{s.Glyph(), s.Label(), glyphColor(s)})
	}
	// Only while they are being shown. A key for a mark that is not on screen is noise, and the
	// footer is already the widest thing competing for the bottom line.
	if m.showDone {
		out = append(out, legendEntry{"✓", "done", colFaint})
	}
	return out
}

// footerLines is the footer's contents, already laid out, one string per line.
//
// The renderer and the height calculation both read this rather than each deciding for
// themselves. They used to share two predicates instead, which held until the key list outgrew a
// narrow terminal: the line wrapped to a third row the height never counted, and the panes above
// slid off the top.
func (m model) footerLines() []string {
	legend := make([]string, 0, len(m.activeLegend()))
	for _, e := range m.activeLegend() {
		legend = append(legend, lipgloss.NewStyle().Foreground(e.colour).Render(e.glyph)+" "+keyStyle.Render(e.label))
	}
	keys := make([]string, 0, len(m.activeKeys()))
	for _, k := range m.activeKeys() {
		keys = append(keys, keyStyle.Render(k))
	}

	left, right := strings.Join(legend, "   "), strings.Join(keys, "   ")
	// With nothing on the left, the keys are the whole bar and start at the margin. Right-
	// aligning them against an empty line leaves them floating off in the corner.
	if left == "" {
		return []string{right}
	}
	if gap := m.width - lipgloss.Width(left) - lipgloss.Width(right); gap >= 2 {
		return []string{left + strings.Repeat(" ", gap) + right}
	}
	// Stacking when it does not fit, rather than truncating, is the whole reason for leaving
	// fzf, whose header could only ever truncate. Each half wraps onto as many rows as it needs
	// and every row is counted.
	return append(wrapItems(legend, "   ", m.width), wrapItems(keys, "   ", m.width)...)
}

// wrapItems packs items onto as few lines as fit the width, never splitting one.
func wrapItems(items []string, sep string, width int) []string {
	var lines []string
	cur := ""
	for _, it := range items {
		next := it
		if cur != "" {
			next = cur + sep + it
		}
		if cur != "" && lipgloss.Width(next) > width {
			lines = append(lines, cur)
			cur = it
			continue
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// footerHeight must agree with renderFooter or the panes overflow the terminal. Both come from
// footerLines, so they cannot drift.
func (m model) footerHeight() int {
	h := 1 + len(m.footerLines()) // the rule above the footer, then its rows
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

	// The key list takes the whole body rather than floating over it. Compositing a box on top
	// of two panes is a layer lipgloss does not have, and a page you are reading does not need
	// to show you the list you are not reading.
	if m.mode == modeHelp {
		return strings.Join([]string{
			m.renderPrompt(),
			lipgloss.NewStyle().Width(m.width).Height(rows).Render(m.renderHelp(rows)),
			m.renderFooter(),
		}, "\n")
	}

	left, right := m.renderList(rows), m.renderPreview(rows)
	if m.mode == modeWorkspace || m.mode == modeNewWS || m.mode == modeNewHost {
		left, right = m.renderWorkspaces(rows), m.renderWorkspaceDetail(rows)
	}
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		listStyle.Width(m.listWidth()).Height(rows).Render(left),
		previewStyle.Width(m.previewWidth()).Height(rows).Render(right),
	)

	return strings.Join([]string{m.renderPrompt(), body, m.renderFooter()}, "\n")
}

func (m model) renderPrompt() string {
	if m.mode == modeWorkspace || m.mode == modeNewWS || m.mode == modeNewHost {
		label, hint := " workspaces ", "enter to go there"
		typed := ""
		switch m.mode {
		case modeNewWS:
			// Named, so it is obvious which machine the path will be read on. The cursor already
			// decided; saying so is what stops it being a surprise.
			where := "here"
			if sys := m.currentSystem(); sys != "" {
				where = "on " + sys
			}
			label, hint = " new workspace ", "name, then a path "+where
			typed = " " + m.input + promptStyle.Render("▏")
		case modeNewHost:
			label, hint = " add machine ", "an ssh target, like jade@eldo"
			typed = " " + m.input + promptStyle.Render("▏")
		}
		left := newLabel.Render(label) + typed
		right := countStyle.Render(hint)
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if m.mode == modeHelp {
		left := newLabel.Render(" keys ")
		right := countStyle.Render("esc closes")
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if m.mode == modeClose {
		// Named, because the line is about one item and the list behind it is not scrolled to
		// make that obvious.
		left := newLabel.Render(" closing out ") + " " + m.input + promptStyle.Render("▏")
		right := countStyle.Render(truncate("one line on "+m.closing, max(12, m.width/3)))
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if m.mode == modeBindWorkflow {
		// Asked once, on the prompt line, so the list it is about stays on screen underneath.
		var choices []string
		for i, e := range m.wfChoices {
			label := e.Addr
			if e.Note != "" {
				label += " (" + e.Note + ")"
			}
			if i == m.wfCursor {
				choices = append(choices, rowSelected.Reverse(true).Render(" "+label+" "))
			} else {
				choices = append(choices, repoStyle.Render(" "+label+" "))
			}
		}
		left := newLabel.Render(" workflow ") + promptStyle.Render(" nothing binds one here, run  ") + strings.Join(choices, " ")
		right := countStyle.Render("← → pick, enter binds it to this workspace")
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if m.mode == modeNewRepo {
		// The typed name stays on screen so the question reads as one line: "new <name> in
		// <which repo>". The highlighted choice is the one enter takes.
		var choices []string
		for i, r := range m.repoChoices {
			label := r
			if r == "" {
				label = "none"
			}
			if i == m.repoCursor {
				choices = append(choices, rowSelected.Reverse(true).Render(" "+label+" "))
			} else {
				choices = append(choices, repoStyle.Render(" "+label+" "))
			}
		}
		left := newLabel.Render(" new ") + " " + strings.TrimSpace(m.input) + promptStyle.Render("  in ") + strings.Join(choices, " ")
		right := countStyle.Render("← → pick a repo, enter makes it")
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if m.mode == modeNew {
		// A distinct label, because this line creates rather than filters and the two look
		// identical otherwise.
		left := newLabel.Render(" new ") + " " + m.input + promptStyle.Render("▏")
		right := countStyle.Render("name, repo/name, or paste a link")
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
	// Machines, not workspaces. The header has room for one level, and a machine with an agent
	// waiting is the thing worth seeing from inside a list that shows neither; which of its
	// workspaces it is in is what ctrl-w is for.
	systems := wisp.Systems(m.peers)
	if len(systems) < 2 && len(m.peers) < 2 {
		return ""
	}
	parts := make([]string, 0, len(systems))
	for _, sys := range systems {
		name := sys.Name
		if name == "" {
			name = wisp.ThisSystem()
		}
		style := wsOther
		if sys.Current {
			style = wsCurrent
		}
		s := style.Render(name)
		switch {
		// Unusable, one way or another. Marked rather than hidden, because a machine missing
		// from a list you wrote yourself reads as wisp losing it rather than as something to fix.
		case !sys.Reachable:
			s = wsMissing.Render(name) + lipgloss.NewStyle().Foreground(colAttn).Render(" ⚠")
		case sys.Attn > 0:
			s += lipgloss.NewStyle().Foreground(colAttn).Render(fmt.Sprintf(" ?%d", sys.Attn))
		case sys.Live > 0:
			s += lipgloss.NewStyle().Foreground(colLive).Render(fmt.Sprintf(" ●%d", sys.Live))
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
		// Done overrides the state glyph only while nothing is running. A live closed-out item
		// keeps its "●": something is on the machine holding an agent, and marking the row
		// finished would hide the one fact that still needs acting on.
		mark, colour := it.State.Glyph(), glyphColor(it.State)
		if it.Done && it.State < wisp.StateLive {
			mark, colour = "✓", colFaint
		}
		glyph := lipgloss.NewStyle().Foreground(colour).Render(mark)

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

// renderWorkspaces is the left pane in workspace mode. Same shape as the item list, because it
// is the same gesture one layer up: a cursor, a glyph carrying state, enter to go.
// renderWorkspaces is the left pane in workspace mode: the two levels above a session, drawn as
// the tree they are.
//
// Machines are headers rather than rows. `eldo` and `eldo/side` in one flat list is the tree
// written out as strings, which reads as four unrelated names when it is one machine holding
// three things.
func (m model) renderWorkspaces(rows int) string {
	if len(m.peers) == 0 {
		return hintStyle.Render("  no workspaces")
	}
	// Windowed by wsOffset exactly as the item list is by offset. Rendering the whole tree made
	// the pane taller than the terminal on any machine holding more workspaces than it has lines.
	all := m.wsRows()
	end := min(m.wsOffset+rows, len(all))
	var out []string
	for i := m.wsOffset; i < end; i++ {
		r := all[i]
		sel := i == m.wsCursor
		lead := "  "
		if sel {
			lead = pointer.String() + " "
		}
		if r.Head != nil {
			out = append(out, truncate(lead+m.renderSystemHeader(*r.Head, sel), m.listInner()))
			continue
		}
		p := *r.Peer
		glyph, colour := wsGlyph(p)
		style := rowStyle
		if sel {
			style = rowSelected
		}
		// The workspace's own name under its machine. The qualified form is what you type, not
		// what you read: repeating `eldo/` on every row beneath a header saying `eldo` spends
		// the width the name itself needs.
		label := p.Workspace
		if label == "" {
			label = p.Name
		}
		line := lead + "  " + lipgloss.NewStyle().Foreground(colour).Render(glyph) + " " + style.Render(label)
		if p.Current {
			line += repoStyle.Render("  (here)")
		}
		out = append(out, truncate(line, m.listInner()))
	}
	return strings.Join(out, "\n")
}

func (m model) renderSystemHeader(sys wisp.System, selected bool) string {
	name := sys.Name
	if name == "" {
		name = wisp.ThisSystem()
	}
	style := wsOther
	switch {
	case selected:
		style = wsHeadSelected
	case sys.Current:
		style = wsCurrent
	}
	head := style.Render(name)
	switch {
	case !sys.Reachable:
		head = wsMissing.Render(name) + lipgloss.NewStyle().Foreground(colAttn).Render(" ⚠")
	case sys.Attn > 0:
		head += lipgloss.NewStyle().Foreground(colAttn).Render(fmt.Sprintf("  ?%d", sys.Attn))
	case sys.Live > 0:
		head += lipgloss.NewStyle().Foreground(colLive).Render(fmt.Sprintf("  ●%d", sys.Live))
	}
	return head
}

// renderWorkspaceDetail is the right pane in workspace mode: where the highlighted workspace
// points and what is running in it. The path is the thing you actually need to see, since a
// workspace that will not open is almost always one pointing somewhere unexpected.
func (m model) renderWorkspaceDetail(rows int) string {
	p := m.peer()
	if p == nil {
		return hintStyle.Render("no workspaces")
	}
	var lines []string
	lines = append(lines, titleStyle.Render(p.Name), "", previewText.Render(p.Path), "")
	if p.System != "" {
		lines = append(lines, previewText.Render("on "+p.System), "")
	}
	switch {
	case p.Unreachable:
		lines = append(lines, errStyle.Render("unreachable"), "", previewText.Render(p.Detail))
	case !p.Ready:
		lines = append(lines,
			errStyle.Render("does not exist yet"),
			"",
			previewText.Render("ctrl-n makes one; add -p to create the directory too."))
	default:
		lines = append(lines, previewText.Render(fmt.Sprintf("%d live, %d waiting on you", p.Live, p.Attn)))
	}
	// Not sanitizeLine: these strings are wisp's own and already carry styling, and stripping
	// control characters would take the escape sequences with them.
	for i, l := range lines {
		lines[i] = truncate(l, m.previewInner())
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines[:rows], "\n")
}

// helpEntry is one binding and what pressing it does, worded the way the footer words things.
type helpEntry struct{ keys, does string }

type helpSection struct {
	title   string
	entries []helpEntry
}

// The whole vocabulary, including the tree's plain letters, which are otherwise only
// discoverable by being in the tree. Two columns, because a single one is taller than a short
// terminal and this is the one screen that must never need scrolling.
var helpLeft = []helpSection{
	{"the list", []helpEntry{
		{"enter", "open it"},
		{"ctrl-n", "new item: a name, repo/name, or a link; a bare name is asked for its repo"},
		{"ctrl-d", "close it out"},
		{"ctrl-t", "show or hide closed"},
		{"ctrl-x", "kill its session"},
		{"ctrl-r", "refresh gitlab"},
	}},
	{"typing", []helpEntry{
		{"↑ ↓  ctrl-k ctrl-j", "move the cursor"},
		{"ctrl-u", "clear the filter"},
		{"ctrl-g", "this page"},
		{"esc", "quit, leaving it running"},
	}},
}

var helpRight = []helpSection{
	{"machines and workspaces", []helpEntry{
		{"ctrl-w", "the tree, and back"},
		{"enter", "go there"},
		{"← →  h l", "jump a whole machine"},
		{"n", "new workspace on this machine"},
		{"a", "add a machine"},
		{"x", "forget it, touching no files"},
	}},
	{"glyphs", []helpEntry{
		{"● live", "a session is running"},
		{"? needs input", "the agent is waiting on you"},
		{"○ folder", "a vault folder, no session"},
		{"+ gitlab", "on gitlab, nothing local"},
		{"✓ done", "closed out, under ctrl-t"},
	}},
}

// renderHelp lays the sections into two columns that tile the width the same way the panes do.
func (m model) renderHelp(rows int) string {
	half := m.width / 2
	left := renderHelpColumn(helpLeft, half-2, 20)
	right := renderHelpColumn(helpRight, m.width-half-2, 15)

	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(half).PaddingLeft(2).Render(strings.Join(left, "\n")),
		lipgloss.NewStyle().Width(m.width-half).PaddingLeft(2).Render(strings.Join(right, "\n")),
	)
	// Truncated rather than wrapped, for the same reason the preview is: a wrapped row makes
	// the block taller than rows and pushes the footer off the bottom.
	lines := strings.Split(body, "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

func renderHelpColumn(sections []helpSection, width, keyCol int) []string {
	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, titleStyle.Render(s.title))
		for _, e := range s.entries {
			pad := keyCol - lipgloss.Width(e.keys)
			if pad < 1 {
				pad = 1
			}
			line := "  " + rowSelected.Render(e.keys) + strings.Repeat(" ", pad) + rowStyle.Render(e.does)
			out = append(out, truncate(line, width))
		}
	}
	return out
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
	bar := strings.Join(m.footerLines(), "\n")

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
