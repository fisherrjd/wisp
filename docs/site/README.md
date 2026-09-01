# docs/site

The documentation site. Vue 3 + Vite + TypeScript, Tailwind v4, after
[fisherrjd/app-template](https://github.com/fisherrjd/app-template): same stack,
same token wiring, the same `dusk` palette.

**The reference pages are `docs/*.md`, rendered.** They are not a copy. Editing
`docs/commands.md` is editing `/docs/commands` on the site, and a new
`docs/<name>.md` becomes `/docs/<name>` with no wiring: the file list, the
routes and the per-route pages in the nix build are all derived from the same
glob. Only the grouping and the reading order are hand-set, in
`src/lib/pages.ts`; a page missing from that map lands in **More** rather than
disappearing.

The landing page is hand-written, because the terminal transcripts on it are
coloured with wisp's own glyph colours and no markdown fence can do that.

## Working on it

```sh
npm install
npm run dev        # http://localhost:3017
npm run build      # typecheck + build to ./dist
npm run typecheck
```

`nodejs` is in both the `default.nix` devshell and `nix develop`.

## Building it the way it ships

```sh
nix build .#wisp-docs
```

`package.nix` builds the same `dist/`, then writes one copy of `index.html` per
route so the history-mode URLs work on a static host with no rewrite rules.
After changing dependencies, update the hash:

```sh
nix run nixpkgs#prefetch-npm-deps -- docs/site/package-lock.json
```

## Two things nobody types twice

- **The version.** `vite.config.ts` reads `Version` and `WireVersion` out of
  `internal/wisp/remote.go`, and `package.nix` reads the same line for the
  derivation's version. The site cannot claim a release wisp does not have.
- **The docs.** See above.

## Theme

One preset, `dusk`, in `src/assets/themes/dusk.css`, with a light half and a
dark half; the switch in the header moves between them and persists to
`localStorage` under `wisp.docs.mode`, falling back to the system preference.
wisp's glyph colours (`--g-live`, `--g-attn`, …) are lifted from
`internal/ui/styles.go` and live in `src/assets/index.css`; they are
deliberately not theme tokens, because a live session is green for a reason of
its own.

app-template's four presets, its `ThemePicker` and its shadcn component set are
not here. A docs site wants a look, not a playground. `clsx` and `tailwind-merge`
stay so that `bunx shadcn-vue@latest add <component>` still works if that
changes.
