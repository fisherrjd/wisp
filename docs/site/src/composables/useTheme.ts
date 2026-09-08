import { ref, watchEffect } from 'vue'

// One preset (dusk), so mode is the only choice. app-template's useTheme keeps
// a theme ref as well; there is nothing here for it to select between.
const MODE_KEY = 'wisp.docs.mode'

export type Mode = 'light' | 'dark'

function initialMode(): Mode {
  try {
    const saved = localStorage.getItem(MODE_KEY)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // private mode, or storage disabled: the system preference still answers
  }
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

// module-scope singleton: every component shares the same mode
const mode = ref<Mode>(initialMode())

watchEffect(() => {
  document.documentElement.classList.toggle('dark', mode.value === 'dark')
  try {
    localStorage.setItem(MODE_KEY, mode.value)
  } catch {
    // the class is already on the root; persistence is the only thing lost
  }
})

export function useTheme() {
  const toggleMode = () => {
    mode.value = mode.value === 'dark' ? 'light' : 'dark'
  }
  return { mode, toggleMode }
}
