<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="pt-20">
    <h1 class="mb-3 text-3xl font-bold">Nothing here</h1>
    <p class="mb-6 max-w-[60ch] text-muted-foreground">
      This site is the overview and the reference pages, and that is all of it.
    </p>
    <div class="flex gap-4 text-sm">
      <RouterLink to="/">Overview</RouterLink>
      <RouterLink to="/docs">Reference</RouterLink>
    </div>
  </div>
</template>
