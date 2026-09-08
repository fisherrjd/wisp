import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// 3017, after the version this site was cut from. One port per app.
const PORT = 3017

// The version in the header and the footer is read out of the binary's own
// source, not typed here. A page that can claim a version wisp does not have is
// how the last copy of these docs drifted into describing software that had
// stopped existing.
function fromRemoteGo(pattern: RegExp, what: string): string {
  const source = readFileSync(
    fileURLToPath(new URL('../../internal/wisp/remote.go', import.meta.url)),
    'utf8',
  )
  const match = source.match(pattern)
  if (!match) throw new Error(`cannot find ${what} in internal/wisp/remote.go`)
  return match[1]
}

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  define: {
    __WISP_VERSION__: JSON.stringify(fromRemoteGo(/const Version = "([^"]+)"/, 'Version')),
    __WISP_WIRE__: JSON.stringify(fromRemoteGo(/const WireVersion = (\d+)/, 'WireVersion')),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: './dist',
  },
  preview: {
    port: PORT,
  },
  server: {
    host: '0.0.0.0',
    allowedHosts: true,
    port: PORT,
    fs: {
      // src/lib/pages.ts globs ../../../*.md — the reference markdown one level
      // up, which is the whole point of this site. Dev has to be allowed to
      // read it; the build inlines it and never looks again.
      allow: ['../..'],
    },
  },
})
