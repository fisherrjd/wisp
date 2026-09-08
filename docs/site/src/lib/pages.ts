// The reference pages are docs/*.md, one level up. They are bundled at build
// time rather than fetched, so the site cannot serve a page the build did not
// have, and a deploy is one directory of static files with nothing behind it.
//
// Nothing here is a copy. The markdown is the source; the copy that used to
// live in a hand-written index.html is what drifted until it documented a wisp
// with no workspaces and no hosts, and nothing said so.
const sources = import.meta.glob('../../../*.md', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

export type DocPage = {
  slug: string
  title: string
  blurb: string
  body: string
  group: string
  note?: string
}

// Order and grouping for the pages we know about. A new docs/*.md needs no
// entry: it lands in "More" at the end, on the site, rather than being
// invisible until someone remembers this file.
const KNOWN: Record<string, { group: string; order: number; note?: string }> = {
  items: { group: 'Concepts', order: 0 },
  commands: { group: 'Using it', order: 1 },
  picker: { group: 'Using it', order: 2 },
  sessions: { group: 'Using it', order: 3 },
  configuration: { group: 'Setting up', order: 4 },
  'remote-workspaces': { group: 'Setting up', order: 5 },
  hooks: { group: 'Design', order: 6, note: 'design' },
}

export const GROUP_ORDER = ['Concepts', 'Using it', 'Setting up', 'Design', 'More']

// `# Title` on the first line, then the lede paragraph. Every reference page is
// written this way; one that is not still renders, it just has a thinner card.
function split(slug: string, raw: string): { title: string; blurb: string; body: string } {
  const text = raw.replace(/^﻿/, '')
  const match = text.match(/^#[^\S\n]+(.+?)[^\S\n]*\n/)
  if (!match) return { title: slug, blurb: '', body: text }

  const body = text.slice(match[0].length).replace(/^\s+/, '')
  const lede = body.split(/\n\s*\n/, 1)[0] ?? ''
  // A lede that is a heading or a fence is not a lede.
  const blurb = /^[#`|>\-*\d]/.test(lede) ? '' : plain(lede)
  return { title: match[1].trim(), blurb, body }
}

// Card blurbs are text, not markup: the backticks and asterisks that read as
// emphasis in the file read as noise in a one-line summary.
function plain(text: string): string {
  return text
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/(^|\W)[*_]([^*_]+)[*_](?=\W|$)/g, '$1$2')
    .replace(/\s+/g, ' ')
    .trim()
}

export const PAGES: DocPage[] = Object.entries(sources)
  .map(([path, raw]) => {
    const slug = path.slice(path.lastIndexOf('/') + 1).replace(/\.md$/, '')
    const known = KNOWN[slug]
    return {
      slug,
      group: known?.group ?? 'More',
      note: known?.note,
      ...split(slug, raw),
    }
  })
  .sort((a, b) => {
    const ao = KNOWN[a.slug]?.order ?? Number.MAX_SAFE_INTEGER
    const bo = KNOWN[b.slug]?.order ?? Number.MAX_SAFE_INTEGER
    return ao - bo || a.slug.localeCompare(b.slug)
  })

export function pageBySlug(slug: string): DocPage | undefined {
  return PAGES.find((p) => p.slug === slug)
}

export type PageGroup = { group: string; pages: DocPage[] }

export const GROUPED: PageGroup[] = GROUP_ORDER.map((group) => ({
  group,
  pages: PAGES.filter((p) => p.group === group),
})).filter((g) => g.pages.length > 0)
