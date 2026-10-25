import { defineStore } from 'pinia'

// Liquid glass theme switch (UI 改版): dark by default, persisted in
// localStorage. The `vestt-dark` class on <html> drives both the PrimeVue
// darkModeSelector and the CSS-variable glass palette in style.css.
export const THEME_STORAGE_KEY = 'vestt.theme'

function applyTheme(dark: boolean): void {
  document.documentElement.classList.toggle('vestt-dark', dark)
  localStorage.setItem(THEME_STORAGE_KEY, dark ? 'dark' : 'light')
}

export const useThemeStore = defineStore('theme', {
  state: () => ({
    dark: true,
  }),
  actions: {
    init(): void {
      this.dark = localStorage.getItem(THEME_STORAGE_KEY) !== 'light'
      applyTheme(this.dark)
    },
    set(dark: boolean): void {
      this.dark = dark
      applyTheme(this.dark)
    },
    toggle(): void {
      this.set(!this.dark)
    },
  },
})
