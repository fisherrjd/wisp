// Builds the hero transcript in HomeView.vue, to the same arithmetic
// internal/ui/view.go uses, so the picture is dimensionally the picker rather
// than an impression of it.
//
//   node scripts/hero-transcript.mjs
//
// and paste the result between the <pre class="term-pre"> tags in
// src/views/HomeView.vue. It exists because the columns have to tile exactly:
// listWidth() + previewWidth() = width, the pane border falls in one column on
// every row, and the footer's rule spans the lot. Counting those spaces by hand
// is how the previous version ended up with the divider two columns adrift on
// one row out of three, in a box half again as wide as its own contents.
//
// If view.go's layout changes, change it here and re-run rather than nudging
// the spaces in the template. It throws rather than truncating when a row does
// not fit, which is the one thing the real renderer does differently — it
// truncates, and a mock that quietly did the same would hide the mistake.
// 144 columns: a real window, wide enough that footerLines() keeps the legend
// and the keys on one line, and narrow enough that the card fits the page
// measure without the page having to grow to meet it.
const W = 144
const LIST = Math.max(24, Math.min(Math.trunc(W * 0.4), W - 20)) // listWidth()
const LIST_INNER = LIST - 2 // border 1 + padding-right 1
const PREVIEW = W - LIST
const PREVIEW_INNER = PREVIEW - 2 // padding-left 2
// Five. This is a landing page, not a bug report: one row per glyph, so a
// session, a folder and a GitLab row are visibly the same list, and no more
// than that. Somebody meeting the tool should not have to read a backlog.
const ROWS = 5

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
// a segment is [text, class|null]; width is the plain text length
const seg = (parts) => ({
  w: parts.reduce((n, [t]) => n + [...t].length, 0),
  html: parts.map(([t, c]) => (c ? `<span class="${c}">${esc(t)}</span>` : esc(t))).join(''),
})
const pad = (n) => ' '.repeat(Math.max(0, n))

const out = []

// ── prompt line: left + gap + (ring   count) ──────────────────────────────
const left = seg([
  ['› ', 'g-acc'],
  ['▏', 'g-acc'],
])
const right = seg([
  ['airbook', 'g-acc-b'],
  [' ?1', 'g-attn'],
  [' · ', 'g-faint'],
  ['eldo', 'g-faint'],
  [' ●3', 'g-live'],
  ['   ', null],
  ['8/8', 'g-faint'],
])
out.push(left.html + pad(W - left.w - right.w) + right.html)

// ── body: list pane | rule | preview pane ─────────────────────────────────
const items = [
  ['●', 'g-live', 'ledger-service/', '318-audit-trail', false],
  ['?', 'g-attn', 'ledger-service/', '327-backfill', true],
  ['○', 'g-folder', 'ledger-service/', '331-fx-rounding', false],
  ['○', 'g-folder', '_adhoc/', 'vq-workspace', false],
  ['+', 'g-remote', 'payments-api/', '1042-idempotency', false],
]

// The selected item is live, so the preview is its agent's pane. Kept short:
// the point is that you can read what it is doing before switching to it, not
// that a hero should reproduce a whole session.
const preview = [
  '● Backfill streams by day now, and the batch size is a flag.',
  '',
  '  A run that died halfway restarted from zero. The resume path has a test now.',
  '',
  'Run the migration against staging?',
]

for (let i = 0; i < ROWS; i++) {
  const it = items[i]
  let row
  if (it) {
    const [glyph, colour, repo, rest, selected] = it
    row = seg([
      selected ? ['▌ ', 'g-acc'] : ['  ', null],
      [glyph, colour],
      [' ', null],
      [repo, 'g-faint'],
      [rest, selected ? 'g-ink-b' : 'g-soft'],
    ])
  } else {
    row = { w: 0, html: '' }
  }
  if (row.w > LIST_INNER) throw new Error(`list row ${i} is ${row.w} > ${LIST_INNER}`)

  const text = preview[i] ?? ''
  if ([...text].length > PREVIEW_INNER)
    throw new Error(`preview row ${i} is ${[...text].length} > ${PREVIEW_INNER}`)
  const pv = text ? `<span class="g-soft">${esc(text)}</span>` : ''

  out.push(row.html + pad(LIST_INNER - row.w) + ' ' + `<span class="g-rule">│</span>` + '  ' + pv)
}

// ── footer: full-width rule, then legend and keys ─────────────────────────
out.push(`<span class="g-rule">${'─'.repeat(W)}</span>`)

const legend = [
  ['●', 'g-live', 'live'],
  ['?', 'g-attn', 'needs input'],
  ['○', 'g-folder', 'folder'],
  ['+', 'g-remote', 'gitlab'],
]
// itemKeys() carries the three verbs, the way out, and the way to everything
// else. ctrl-x, ctrl-w and ctrl-r are on the ctrl-g page. Nothing is closed
// out in this state, so there is no ctrl-t either.
const keys = [
  'enter open',
  'ctrl-n new',
  'ctrl-d done',
  'ctrl-g keys',
  'esc quit',
]

const legendLine = seg(legend.flatMap(([g, c, label], i) => (i ? [['   ', null]] : []).concat([[g, c], [' ' + label, 'g-soft']])))
const keysLine = seg([[keys.join('   '), 'g-soft']])

// view.go puts them on one line only when the gap is >= 2; otherwise each
// half wraps onto as many rows as it needs. W is chosen so it fits.
if (W - legendLine.w - keysLine.w >= 2) {
  out.push(legendLine.html + pad(W - legendLine.w - keysLine.w) + keysLine.html)
} else {
  if (legendLine.w > W || keysLine.w > W) throw new Error('a half needs wrapping; not modelled')
  out.push(legendLine.html)
  out.push(keysLine.html)
}

console.error(
  `W=${W} list=${LIST} listInner=${LIST_INNER} preview=${PREVIEW} previewInner=${PREVIEW_INNER} legend=${legendLine.w} keys=${keysLine.w}`,
)
console.log(out.join('\n'))
