<script setup lang="ts">
import { onBeforeUnmount, ref, watch, nextTick } from 'vue'
import type { TocEntry } from '@/lib/markdown'

const props = defineProps<{ entries: TocEntry[]; label?: string }>()

const active = ref<string>('')
let observer: IntersectionObserver | null = null
const visible = new Set<string>()

function teardown() {
  observer?.disconnect()
  observer = null
  visible.clear()
}

// Highlight the section currently in view: the topmost one intersecting the
// band between the sticky header and the bottom 60% of the viewport.
watch(
  () => props.entries,
  async (entries) => {
    teardown()
    active.value = entries[0]?.id ?? ''
    if (!entries.length) return
    await nextTick()

    observer = new IntersectionObserver(
      (records) => {
        for (const record of records) {
          if (record.isIntersecting) visible.add(record.target.id)
          else visible.delete(record.target.id)
        }
        const top = entries.find((e) => visible.has(e.id))
        if (top) active.value = top.id
      },
      { rootMargin: '-72px 0px -60% 0px' },
    )

    for (const entry of entries) {
      const el = document.getElementById(entry.id)
      if (el) observer.observe(el)
    }
  },
  { immediate: true },
)

onBeforeUnmount(teardown)
</script>

<template>
  <nav v-if="entries.length" :aria-label="label ?? 'On this page'" class="text-sm">
    <div
      class="px-2.5 pb-1.5 text-[0.68rem] font-semibold tracking-[0.09em] text-muted-foreground uppercase"
    >
      {{ label ?? 'On this page' }}
    </div>
    <ul class="flex flex-col gap-0.5">
      <li v-for="entry in entries" :key="entry.id">
        <a
          :href="`#${entry.id}`"
          class="block truncate rounded-sm border-l-2 border-transparent py-1 text-[0.85rem] text-muted-foreground no-underline hover:bg-accent/40 hover:text-foreground"
          :class="[
            entry.depth >= 3 ? 'pr-2 pl-5' : 'px-2.5',
            active === entry.id
              ? 'border-l-primary bg-accent/35 font-semibold text-foreground'
              : '',
          ]"
        >
          {{ entry.text }}
        </a>
      </li>
    </ul>
  </nav>
</template>
