<script setup lang="ts">
// The one place the subject speaks for itself: a transcript in wisp's own glyph
// colours, in terminal chrome.
//
// The slot has to hold the `<pre>` itself, not just its contents: Vue's
// template compiler condenses runs of whitespace everywhere except lexically
// inside a `<pre>` tag, and condensed whitespace is a picker whose columns no
// longer line up.
//
// No glow under it. A coloured bloom behind a screenshot is the most reliable
// tell of a page trying to sell you something.
defineProps<{ title: string }>()
</script>

<template>
  <!-- w-fit, so the chrome ends where the 144th column does. A terminal tiles
       its width exactly; a transcript adrift in a box half again as wide is
       the one thing a picture of one must not do. -->
  <div
    class="w-fit max-w-full overflow-hidden rounded-xl border bg-card"
  >
    <div
      class="flex items-center gap-2 border-b bg-muted/55 px-3.5 py-[0.45rem] font-mono text-[0.7rem] text-muted-foreground"
    >
      <span class="size-[0.55rem] rounded-full bg-border" />
      <span class="size-[0.55rem] rounded-full bg-border" />
      <span class="size-[0.55rem] rounded-full bg-border" />
      <span class="ml-1.5">{{ title }}</span>
    </div>
    <div class="overflow-x-auto">
      <slot />
    </div>
  </div>
</template>
