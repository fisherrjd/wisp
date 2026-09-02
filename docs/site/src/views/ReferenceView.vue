<script setup lang="ts">
import PageGrid from '@/components/PageGrid.vue'
</script>

<template>
  <div class="rise-in pt-10">
    <h1 class="mb-2 text-3xl font-semibold tracking-[-0.03em] text-balance">Reference</h1>
    <p class="mb-8 max-w-[68ch] text-pretty text-muted-foreground">
      Every command, every key, every error message. Each page here is a file in
      <span class="font-mono text-[0.9em]">docs/</span> in the repository, rendered as it is: the
      site and the markdown cannot disagree because there is only one of them.
    </p>
    <PageGrid />
  </div>
</template>
