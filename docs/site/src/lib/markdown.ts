import DOMPurify from 'dompurify'
import { marked } from 'marked'

export type TocEntry = { id: string; text: string; depth: number }
export type Rendered = { html: string; toc: TocEntry[] }

// GitHub's heading slugs, near enough: the reference pages already link to each
// other with them (`sessions.md#the-home-loop`), so the site has to produce the
// same ids or those links land at the top of the page instead of the section.
function slugify(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N} _-]/gu, '')
    .replace(/[ _]+/g, '-')
}

/**
 * Markdown to sanitized HTML, with the three things a reference page needs on
 * top of the prose: linkable headings, tables that scroll in their own box, and
 * `other-page.md` links rewritten to routes.
 *
 * Post-processing the sanitized DOM rather than overriding marked's renderer
 * keeps this working across marked's renderer-signature changes, and puts our
 * own markup in after DOMPurify rather than through it.
 */
export function render(source: string): Rendered {
  const dirty = marked.parse(source, { async: false })
  const doc = new DOMParser().parseFromString(DOMPurify.sanitize(dirty), 'text/html')

  const toc: TocEntry[] = []
  const seen = new Map<string, number>()

  doc.querySelectorAll('h2, h3, h4').forEach((heading) => {
    const text = heading.textContent?.trim() ?? ''
    const base = slugify(text) || 'section'
    const n = seen.get(base) ?? 0
    seen.set(base, n + 1)
    const id = n === 0 ? base : `${base}-${n}`

    heading.id = id
    const anchor = doc.createElement('a')
    anchor.className = 'anchor'
    anchor.setAttribute('href', `#${id}`)
    while (heading.firstChild) anchor.appendChild(heading.firstChild)
    heading.appendChild(anchor)

    toc.push({ id, text, depth: Number(heading.tagName.slice(1)) })
  })

  doc.querySelectorAll('table').forEach((table) => {
    const box = doc.createElement('div')
    box.className = 'table-scroll'
    table.replaceWith(box)
    box.appendChild(table)
  })

  doc.querySelectorAll('a[href]').forEach((link) => {
    const href = link.getAttribute('href') ?? ''
    const local = href.match(/^(?:\.\/)?([\w.-]+)\.md(#.*)?$/)
    if (local) {
      link.setAttribute('href', `/docs/${local[1]}${local[2] ?? ''}`)
      return
    }
    if (/^https?:/i.test(href)) {
      link.setAttribute('target', '_blank')
      link.setAttribute('rel', 'noreferrer noopener')
    }
  })

  return { html: doc.body.innerHTML, toc }
}
