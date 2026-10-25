<script setup lang="ts">
import Button from 'primevue/button'
import Menu from 'primevue/menu'
import ToggleSwitch from 'primevue/toggleswitch'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { useConfigStore } from '../../stores/config'
import { useThemeStore } from '../../stores/theme'
import { persistLocale, type AppLocale } from '../../i18n'

// App shell (UI 改版, replaces the SPEC-11 AppHeader): glass top bar with
// brand + version, primary nav, a settings menu (indexer management and the
// server sub-pages all live behind the cog — indexers are configuration,
// not a top-level destination), theme + language switches, and logout
// (upstream only shows it when an admin password is configured), plus the
// external-access security warning.
const config = useConfigStore()
const auth = useAuthStore()
const theme = useThemeStore()
const router = useRouter()
const { t, locale } = useI18n()

const settingsMenu = ref<InstanceType<typeof Menu>>()

const settingsItems = computed(() => [
  { label: t('nav.indexers'), icon: 'pi pi-list', command: () => router.push('/indexers') },
  { separator: true },
  { label: t('server.title'), icon: 'pi pi-server', command: () => router.push('/settings/server') },
  { label: t('cache.title'), icon: 'pi pi-database', command: () => router.push('/settings/cache') },
  { label: t('logs.title'), icon: 'pi pi-file', command: () => router.push('/settings/logs') },
])

// Two languages only — a left/right switch instead of a dropdown.
const isEnglish = computed({
  get: () => locale.value === 'en',
  set: (v: boolean) => switchLocale(v ? 'en' : 'zh'),
})

// Same switch pattern for the theme: off = dark (moon), on = light (sun).
const isLight = computed({
  get: () => !theme.dark,
  set: (v: boolean) => theme.set(!v),
})

function switchLocale(next: AppLocale): void {
  locale.value = next
  persistLocale(next)
}

function toggleSettingsMenu(event: Event): void {
  settingsMenu.value?.toggle(event)
}

const EXTERNAL_WARNING_KEY = 'vestt.externalAccessWarningHidden'
const warningDismissed = ref(
  typeof localStorage !== 'undefined' && localStorage.getItem(EXTERNAL_WARNING_KEY) === 'true',
)
const warningHiddenNow = ref(false)
const showWarning = computed(
  () =>
    config.loaded &&
    config.external &&
    config.passwordUnset &&
    !warningDismissed.value &&
    !warningHiddenNow.value,
)

async function doLogout() {
  await auth.logout()
  router.push({ name: 'login' })
}

function neverShowAgain() {
  localStorage.setItem(EXTERNAL_WARNING_KEY, 'true')
  warningDismissed.value = true
}

onMounted(() => {
  // keep the store in sync with the pre-mount class application in main.ts
  theme.init()
})
</script>

<template>
  <div class="app-shell">
    <header class="app-nav glass-nav">
      <div v-if="showWarning" class="external-warning" role="alert">
        <i class="pi pi-exclamation-triangle" />
        <strong>{{ $t('warning.text') }}</strong>
        <Button
          icon="pi pi-hourglass"
          :title="$t('warning.remindLater')"
          severity="success"
          size="small"
          @click="warningHiddenNow = true"
        />
        <Button
          icon="pi pi-eye-slash"
          :title="$t('warning.neverShow')"
          severity="danger"
          size="small"
          @click="neverShowAgain"
        />
      </div>
      <div class="header-row">
        <router-link to="/" class="brand">
          <img src="/favicon.svg" alt="Logo" class="logo" />
          <span class="title">Vestt</span>
          <span v-if="config.appVersion" class="version">Version {{ config.appVersion }}</span>
        </router-link>
        <nav v-if="auth.authenticated" class="header-actions">
          <router-link to="/" custom v-slot="{ navigate, isActive }">
            <Button
              icon="pi pi-search"
              :label="$t('nav.search')"
              :severity="isActive ? 'primary' : 'secondary'"
              @click="navigate"
            />
          </router-link>
          <Button
            icon="pi pi-cog"
            :title="$t('nav.settings')"
            severity="secondary"
            aria-haspopup="true"
            aria-controls="settings-menu"
            @click="toggleSettingsMenu"
          />
          <Menu
            id="settings-menu"
            ref="settingsMenu"
            :model="settingsItems"
            popup
          />
          <span
            class="lang-switch"
            :title="$t('nav.language')"
            role="group"
            :aria-label="$t('nav.language')"
          >
            <button
              type="button"
              class="lang-option"
              :class="{ active: locale === 'zh' }"
              @click="switchLocale('zh')"
            >中</button>
            <ToggleSwitch v-model="isEnglish" :aria-label="$t('nav.language')" />
            <button
              type="button"
              class="lang-option"
              :class="{ active: locale === 'en' }"
              @click="switchLocale('en')"
            >EN</button>
          </span>
          <span
            class="lang-switch"
            :title="$t('nav.theme')"
            role="group"
            :aria-label="$t('nav.theme')"
          >
            <button
              type="button"
              class="lang-option theme-option"
              :class="{ active: theme.dark }"
              :aria-label="$t('nav.theme')"
              @click="theme.set(true)"
            ><i class="pi pi-moon" /></button>
            <ToggleSwitch v-model="isLight" :aria-label="$t('nav.theme')" />
            <button
              type="button"
              class="lang-option theme-option"
              :class="{ active: !theme.dark }"
              :aria-label="$t('nav.theme')"
              @click="theme.set(false)"
            ><i class="pi pi-sun" /></button>
          </span>
          <Button
            v-if="auth.passwordSet"
            icon="pi pi-sign-out"
            :label="$t('nav.logout')"
            severity="danger"
            @click="doLogout"
          />
        </nav>
      </div>
    </header>
    <main class="app-content">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.external-warning {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 0.5rem 1rem;
  background: color-mix(in srgb, var(--p-red-500) 18%, transparent);
  color: var(--p-red-300);
  border-bottom: 1px solid color-mix(in srgb, var(--p-red-500) 35%, transparent);
}
:root:not(.vestt-dark) .external-warning {
  color: var(--p-red-700);
}
.header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.6rem 1.5rem;
  flex-wrap: wrap;
}
.brand {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  text-decoration: none;
  color: inherit;
}
.logo {
  height: 2rem;
}
.title {
  font-size: 1.5rem;
  font-weight: 700;
}
.version {
  color: var(--p-text-muted-color);
}
.header-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.lang-switch {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}
.lang-option {
  background: none;
  border: none;
  padding: 0.1rem 0.15rem;
  cursor: pointer;
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
  border-radius: 4px;
}
.lang-option.active {
  color: var(--p-primary-color);
  font-weight: 700;
}
.theme-option i {
  font-size: 0.85rem;
}
.app-content {
  padding: 1.5rem;
}
</style>
