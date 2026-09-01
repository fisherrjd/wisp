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
    <header class="sticky top-0 z-40 border-b bg-background/85 backdrop-blur">
      <div class="mx-auto flex h-14 w-full max-w-6xl items-center gap-5 px-6">
        <RouterLink to="/" class="flex shrink-0 items-baseline gap-2.5 no-underline">
          <b class="font-mono text-[1.05rem] font-semibold tracking-tight">wisp</b>
          <span
            class="hidden rounded-full border bg-muted px-1.5 py-0.5 font-mono text-[0.7rem] text-muted-foreground sm:inline-block"
          >
            {{ VERSION }} · wire {{ WIRE }}
          </span>
        </RouterLink>

        <nav class="flex items-center gap-4 text-sm text-muted-foreground">
          <RouterLink
            v-for="link in links"
            :key="link.to"
            :to="link.to"
            class="relative py-1 whitespace-nowrap no-underline transition-colors hover:text-foreground"
            :class="
              isActive(link.to)
                ? 'text-foreground after:absolute after:-bottom-0.5 after:left-0 after:h-0.5 after:w-full after:rounded-full after:bg-primary'
                : ''
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
      <RouterView v-slot="{ Component }">
        <Transition name="page" mode="out-in">
          <component :is="Component" />
        </Transition>
      </RouterView>
    </main>

    <footer class="border-t">
      <div
        class="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-2 px-6 py-4 text-xs text-muted-foreground"
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
