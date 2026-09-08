<script setup lang="ts">
import { RouterLink, RouterView, useRoute } from 'vue-router'
import ThemeToggle from '@/components/ThemeToggle.vue'
import { REPO, VERSION, WIRE } from '@/lib/meta'

const route = useRoute()

const links = [
  { to: '/', label: 'Overview' },
  { to: '/docs', label: 'Reference' },
]

// `/` must not light up for every route
function isActive(to: string) {
  return to === '/' ? route.path === '/' : route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <div class="flex min-h-screen flex-col bg-background text-foreground">
    <header class="bg-background">
      <div class="mx-auto flex h-20 w-full max-w-6xl items-center gap-5 px-6">
        <RouterLink to="/" class="flex shrink-0 items-baseline gap-2.5 no-underline">
          <b class="font-mono text-[1.05rem] font-semibold tracking-tight">wisp</b>
          <span
            class="hidden rounded-full bg-muted px-2.5 py-1 font-mono text-[0.75rem] text-muted-foreground sm:inline-block"
          >
            {{ VERSION }} · wire {{ WIRE }}
          </span>
        </RouterLink>

        <nav class="flex items-center gap-4 text-sm text-muted-foreground">
          <RouterLink
            v-for="link in links"
            :key="link.to"
            :to="link.to"
            class="rounded-full px-3.5 py-1.5 whitespace-nowrap no-underline transition-colors hover:bg-muted hover:text-foreground"
            :class="
              isActive(link.to) ? 'bg-muted font-semibold text-foreground' : ''
            "
          >
            {{ link.label }}
          </RouterLink>
        </nav>

        <div class="ml-auto flex items-center gap-4">
          <a
            class="hidden text-sm text-muted-foreground no-underline transition-colors hover:text-foreground sm:inline"
            :href="REPO"
            target="_blank"
            rel="noreferrer noopener"
          >
            GitHub
          </a>
          <ThemeToggle />
        </div>
      </div>
    </header>

    <main class="mx-auto w-full max-w-6xl flex-1 px-6 pb-24">
      <RouterView />
    </main>

    <footer>
      <div
        class="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-2 px-6 py-8 text-sm text-muted-foreground"
      >
        <span>
          wisp {{ VERSION }} · wire {{ WIRE }} · MIT. The reference pages on
          this site are <span class="font-mono">docs/*.md</span> rendered, not retyped.
        </span>
        <span>
          Dusk, after <span class="font-mono">fisherrjd/app-template</span>. Glyph colours are
          wisp's own.
        </span>
      </div>
    </footer>
  </div>
</template>
