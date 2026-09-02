<script setup lang="ts">
// The one place the subject speaks for itself: a transcript in wisp's own glyph
// colours, in terminal chrome.
//
// The slot has to hold the `<pre>` itself, not just its contents: Vue's
// template compiler condenses runs of whitespace everywhere except lexically
// inside a `<pre>` tag, and condensed whitespace is a picker whose columns no
// longer line up.
defineProps<{ title: string; glow?: boolean }>()
</script>

<template>
  <!-- w-fit, so the chrome ends where the 120th column does. A terminal tiles
       its width exactly; a transcript adrift in a box half again as wide is
       the one thing a picture of one must not do. -->
  <div
    class="w-fit max-w-full overflow-hidden rounded-xl border bg-card"
    :class="
      glow
        ? 'shadow-[0_12px_40px_-12px_hsl(var(--primary)/0.22),0_2px_8px_-4px_rgb(0_0_0/0.18)]'
        : ''
    "
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
