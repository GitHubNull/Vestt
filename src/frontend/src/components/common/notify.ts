// Toast adapter matching upstream custom.js doNotify (frontend.md §7):
// types info/success/danger, 5000 ms default, sticky option (autoHide=false).
// Initialized from App.vue with the PrimeVue Toast service instance.
import type { ToastServiceMethods } from 'primevue/toastservice'

export type NotifyType = 'info' | 'success' | 'danger'

export const NOTIFY_LIFETIME = 5000

let toast: ToastServiceMethods | null = null

export function initNotify(t: ToastServiceMethods): void {
  toast = t
}

function push(type: NotifyType, message: string, sticky = false): void {
  if (!toast) return
  toast.add({
    severity: type,
    summary: message,
    life: sticky ? 0 : NOTIFY_LIFETIME,
  })
}

export const notify = {
  info: (message: string, sticky = false) => push('info', message, sticky),
  success: (message: string, sticky = false) => push('success', message, sticky),
  danger: (message: string, sticky = false) => push('danger', message, sticky),
}
