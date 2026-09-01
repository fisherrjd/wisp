<script setup lang="ts">
import { computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { render, type TocEntry } from '@/lib/markdown'

const props = defineProps<{ source: string }>()
const emit = defineEmits<{ toc: [TocEntry[]] }>()

const router = useRouter()
const rendered = computed(() => render(props.source))

watch(rendered, (r) => emit('toc', r.toc), { immediate: true })

// Links inside v-html are ordinary anchors, so a rewritten `/docs/…` would
// reload the whole app. Intercept the in-app ones and route instead; leave
// hash links to the browser, which already scrolls to them.
function onClick(event: MouseEvent) {
  if (event.defaultPrevented || event.button !== 0) return
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return

  const anchor = (event.target as HTMLElement | null)?.closest('a')
  const href = anchor?.getAttribute('href')
  if (!href || !href.startsWith('/')) return

  event.preventDefault()
  router.push(href)
}
</script>

<template>
  <div class="md-prose" @click="onClick" v-html="rendered.html" />
</template>
