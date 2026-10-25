import Aura from '@primeuix/themes/aura'
import { definePreset } from '@primeuix/themes'
import { createPinia } from 'pinia'
import PrimeVue from 'primevue/config'
import ConfirmationService from 'primevue/confirmationservice'
import DialogService from 'primevue/dialogservice'
import ToastService from 'primevue/toastservice'
import { createApp } from 'vue'
import App from './App.vue'
import { i18n } from './i18n'
import { notify } from './components/common/notify'
import { router } from './router'
import { initBasePath } from './services/api'
import { capturePendingIndexersHash, capturePendingSearchHash } from './services/searchHash'
import { THEME_STORAGE_KEY } from './stores/theme'
import 'primeicons/primeicons.css'
import './style.css'

// Resolve the mount base path before anything issues a request (custom.js
// computes the same prefix from window.location.pathname).
initBasePath(window.location.pathname)

// SPEC-13 §3.1.1: stash bare `#search=…` / `#indexers=1&filter=…` deep links
// before the router's catch-all rewrites the hash; HomeView / the router
// guard consume and restore them.
capturePendingSearchHash(window.location.hash)
capturePendingIndexersHash(window.location.hash)

// Liquid glass theme (UI 改版): dark by default, user-toggleable via the
// header switch (AppShell); the choice persists in localStorage.
if (localStorage.getItem(THEME_STORAGE_KEY) !== 'light') {
  document.documentElement.classList.add('vestt-dark')
}

const app = createApp(App)

const AuraPreset = definePreset(Aura, {
  semantic: {
    primary: {
      50: '{indigo.50}',
      100: '{indigo.100}',
      200: '{indigo.200}',
      300: '{indigo.300}',
      400: '{indigo.400}',
      500: '{indigo.500}',
      600: '{indigo.600}',
      700: '{indigo.700}',
      800: '{indigo.800}',
      900: '{indigo.900}',
      950: '{indigo.950}',
    },
    colorScheme: {
      light: {
        // frosted glass on the light gradient
        content: {
          background: 'rgba(255, 255, 255, 0.65)',
          hoverBackground: 'rgba(255, 255, 255, 0.85)',
          borderColor: 'rgba(15, 23, 42, 0.10)',
        },
      },
      dark: {
        // translucent content chrome so tables/forms/dialogs blend with the
        // glass panels instead of sitting as opaque islands on the gradient
        content: {
          background: 'rgba(255, 255, 255, 0.045)',
          hoverBackground: 'rgba(255, 255, 255, 0.08)',
          borderColor: 'rgba(255, 255, 255, 0.10)',
        },
        surface: {
          0: '#ffffff',
          50: '#f6f7f9',
          100: '#e5e7eb',
          200: '#d3d7dd',
          300: '#b7bfc9',
          400: '#8e99a7',
          500: '#64707f',
          600: '#454f5e',
          700: '#333b48',
          800: '#222834',
          900: 'rgba(10, 12, 18, 0.72)',
          950: '#0a0c12',
        },
      },
    },
  },
})

app.use(PrimeVue, {
  theme: {
    preset: AuraPreset,
    options: { darkModeSelector: '.vestt-dark' },
  },
})
app.use(ToastService)
app.use(DialogService)
app.use(ConfirmationService)
app.use(i18n)
app.use(createPinia())
app.use(router)

// Global error hooks (SPEC-11 §3.1): unhandled errors surface as danger toasts.
app.config.errorHandler = (err) => {
  notify.danger(err instanceof Error ? err.message : String(err), true)
}
window.addEventListener('unhandledrejection', (event) => {
  const reason = event.reason as { message?: string } | undefined
  notify.danger(reason?.message ?? 'Unexpected error', true)
})

app.mount('#app')
