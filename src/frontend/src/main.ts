import Aura from '@primeuix/themes/aura'
import { definePreset } from '@primeuix/themes'
import { createPinia } from 'pinia'
import PrimeVue from 'primevue/config'
import ConfirmationService from 'primevue/confirmationservice'
import DialogService from 'primevue/dialogservice'
import ToastService from 'primevue/toastservice'
import { createApp } from 'vue'
import App from './App.vue'
import { notify } from './components/common/notify'
import { router } from './router'
import { initBasePath } from './services/api'
import { capturePendingSearchHash } from './services/searchHash'
import 'primeicons/primeicons.css'
import './style.css'

// Resolve the mount base path before anything issues a request (custom.js
// computes the same prefix from window.location.pathname).
initBasePath(window.location.pathname)

// SPEC-13 §3.1.1: stash a bare `#search=…` deep link before the router's
// catch-all rewrites the hash; DashboardView consumes and restores it.
capturePendingSearchHash(window.location.hash)

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
  },
})

app.use(PrimeVue, { theme: { preset: AuraPreset } })
app.use(ToastService)
app.use(DialogService)
app.use(ConfirmationService)
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
