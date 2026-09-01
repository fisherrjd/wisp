<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { GROUPED } from '@/lib/pages'
</script>

<template>
  <div>
    <div v-for="group in GROUPED" :key="group.group" class="mb-6">
      <div
        class="mb-2 text-[0.68rem] font-semibold tracking-[0.09em] text-muted-foreground uppercase"
      >
        {{ group.group }}
      </div>
      <div class="grid gap-3.5 sm:grid-cols-2">
        <RouterLink
          v-for="page in group.pages"
          :key="page.slug"
          :to="`/docs/${page.slug}`"
          class="rounded-lg border bg-card px-4.5 py-4 no-underline transition-colors hover:border-ring/70 hover:bg-accent/30"
        >
          <div class="mb-1 flex items-baseline gap-2">
            <h3 class="font-semibold tracking-tight">{{ page.title }}</h3>
            <span
              v-if="page.note"
              class="rounded-full border px-1.5 text-[0.65rem] tracking-wide text-muted-foreground uppercase"
            >
              {{ page.note }}
            </span>
          </div>
          <p class="line-clamp-3 text-sm text-muted-foreground">{{ page.blurb }}</p>
        </RouterLink>
      </div>
    </div>
  </div>
</template>
