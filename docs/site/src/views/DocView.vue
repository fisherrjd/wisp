<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import MarkdownView from '@/components/content/MarkdownView.vue'
import TocNav from '@/components/TocNav.vue'
import type { TocEntry } from '@/lib/markdown'
import { GROUPED, PAGES, pageBySlug } from '@/lib/pages'

const route = useRoute()
const page = computed(() => pageBySlug(String(route.params.slug)))

const toc = ref<TocEntry[]>([])
watch(page, () => (toc.value = []))

// The router's scrollBehavior fires before this view exists: App.vue's route
// transition is `mode="out-in"`, so the old page has to leave before the
// markdown is in the DOM to scroll to. Land on the section once it is there,
// which is what `commands.md` linking to `sessions.md#the-home-loop` needs.
function onToc(entries: TocEntry[]) {
  toc.value = entries
  const hash = route.hash
  if (!hash) return
  nextTick(() => {
    const el = document.getElementById(decodeURIComponent(hash.slice(1)))
    if (!el) return
    // instant, not smooth: a deep link should arrive, not travel six thousand
    // pixels first
    window.scrollTo({ top: el.getBoundingClientRect().top + window.scrollY - 80 })
  })
}

// prev/next along the reading order the sidebar shows
const neighbours = computed(() => {
  const i = PAGES.findIndex((p) => p.slug === page.value?.slug)
  return { prev: i > 0 ? PAGES[i - 1] : null, next: i >= 0 ? (PAGES[i + 1] ?? null) : null }
})

function isActive(slug: string) {
  return route.params.slug === slug
}
</script>

<template>
  <div v-if="!page" class="pt-16">
    <h1 class="mb-2 text-2xl font-semibold tracking-tight">No such page</h1>
    <p class="mb-6 text-muted-foreground">
      There is no <code class="rounded-sm bg-muted px-1.5 py-0.5 font-mono">{{ route.params.slug }}.md</code>
      in the repository's <span class="font-mono">docs/</span>.
    </p>
    <RouterLink to="/docs">Back to the reference →</RouterLink>
  </div>

  <div v-else class="grid grid-cols-1 gap-10 pt-8 lg:grid-cols-[12rem_minmax(0,1fr)] xl:grid-cols-[12rem_minmax(0,1fr)_13rem]">
    <!-- the pages -->
    <nav class="hidden lg:sticky lg:top-20 lg:block lg:self-start" aria-label="Reference pages">
      <div v-for="group in GROUPED" :key="group.group" class="mb-4">
        <div
          class="px-2.5 pb-1 text-[0.68rem] font-semibold tracking-[0.09em] text-muted-foreground uppercase"
        >
          {{ group.group }}
        </div>
        <ul class="flex flex-col gap-0.5">
          <li v-for="entry in group.pages" :key="entry.slug">
            <RouterLink
              :to="`/docs/${entry.slug}`"
              class="block rounded-sm border-l-2 border-transparent px-2.5 py-1 text-[0.85rem] text-muted-foreground no-underline hover:bg-accent/40 hover:text-foreground"
              :class="
                isActive(entry.slug)
                  ? 'border-l-primary bg-accent/35 font-semibold text-foreground'
                  : ''
              "
            >
              {{ entry.title }}
            </RouterLink>
          </li>
        </ul>
      </div>
    </nav>

    <!-- the page -->
    <article class="min-w-0">
      <div class="mb-6">
        <div class="mb-1 flex items-center gap-2 text-[0.68rem] tracking-[0.09em] uppercase">
          <span class="text-muted-foreground">{{ page.group }}</span>
          <span
            v-if="page.note"
            class="rounded-full border px-1.5 text-muted-foreground"
            >{{ page.note }}</span
          >
        </div>
        <h1 class="text-3xl font-semibold tracking-[-0.03em] text-balance">{{ page.title }}</h1>
      </div>

      <MarkdownView :source="page.body" @toc="onToc" />

      <nav class="mt-16 flex flex-wrap justify-between gap-4 border-t pt-5 text-sm">
        <RouterLink v-if="neighbours.prev" :to="`/docs/${neighbours.prev.slug}`">
          ← {{ neighbours.prev.title }}
        </RouterLink>
        <span v-else />
        <RouterLink v-if="neighbours.next" :to="`/docs/${neighbours.next.slug}`">
          {{ neighbours.next.title }} →
        </RouterLink>
      </nav>
    </article>

    <!-- what is on this page -->
    <!-- A reference page can have thirty headings, so this scrolls inside the
         viewport rather than running off the bottom of a sticky box. -->
    <div
      class="hidden xl:sticky xl:top-20 xl:block xl:max-h-[calc(100vh-6rem)] xl:self-start xl:overflow-y-auto"
    >
      <TocNav :entries="toc" />
    </div>
  </div>
</template>
