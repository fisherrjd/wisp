<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { GROUPED } from '@/lib/pages'
</script>

<template>
  <div>
    <div v-for="group in GROUPED" :key="group.group" class="mb-6">
      <div
        class="eyebrow mb-2"
      >
        {{ group.group }}
      </div>
      <div class="grid gap-3.5 sm:grid-cols-2">
        <RouterLink
          v-for="page in group.pages"
          :key="page.slug"
          :to="`/docs/${page.slug}`"
          class="blob"
        >
          <div class="mb-1 flex items-baseline gap-2">
            <h3 class="font-semibold tracking-tight">{{ page.title }}</h3>
            <span
              v-if="page.note"
              class="rounded-full bg-muted px-2.5 py-0.5 text-[0.75rem] text-muted-foreground"
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
