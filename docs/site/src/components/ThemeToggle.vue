<script setup lang="ts">
import { useTheme } from '@/composables/useTheme'

// One control, and it looks like what it is: a knob that slides between the two
// halves of the one palette. Not a dropdown, because there is nothing else to
// pick.
const { mode, toggleMode } = useTheme()
</script>

<template>
  <button
    type="button"
    role="switch"
    class="relative h-[1.65rem] w-[3.25rem] shrink-0 cursor-pointer rounded-full border bg-secondary p-0 transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus-visible:outline-none"
    :aria-checked="mode === 'dark'"
    :aria-label="mode === 'dark' ? 'Dark mode' : 'Light mode'"
    title="Light / dark"
    @click="toggleMode"
  >
    <span
      class="absolute top-[0.15rem] left-[0.15rem] grid size-5 place-items-center rounded-full bg-card text-[0.72rem] leading-none text-foreground shadow-md transition-transform duration-200 ease-[cubic-bezier(0.34,1.3,0.64,1)]"
      :class="mode === 'dark' ? 'translate-x-6' : 'translate-x-0'"
      aria-hidden="true"
    >
      {{ mode === 'dark' ? '☾' : '☀' }}
    </span>
  </button>
</template>
