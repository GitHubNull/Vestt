import { createI18n } from 'vue-i18n'
import en from './en'
import zh from './zh'

// i18n (UI 改版): Simplified Chinese by default (the team's working
// language), English selectable. The choice persists in localStorage.
export const LOCALE_STORAGE_KEY = 'vestt.locale'
export type AppLocale = 'zh' | 'en'

export function initialLocale(): AppLocale {
  if (typeof localStorage === 'undefined') return 'zh'
  return localStorage.getItem(LOCALE_STORAGE_KEY) === 'en' ? 'en' : 'zh'
}

export function persistLocale(locale: AppLocale): void {
  localStorage.setItem(LOCALE_STORAGE_KEY, locale)
}

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: initialLocale(),
  fallbackLocale: 'en',
  messages: { zh, en },
})
