<script setup lang="ts">
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import SettingsDialog from '../settings/SettingsDialog.vue'
import { useAuthStore } from '../../stores/auth'
import { useConfigStore } from '../../stores/config'
import { useSearchStore } from '../../stores/search'
import CopyButton from './CopyButton.vue'

// Top bar (SPEC-11 §3.5 + SPEC-13 §3.5): title + version, global search
// button, API key panel with mask toggle and copy, settings dialog, logout
// (upstream only shows it when an admin password is configured),
// external-access security warning.
const config = useConfigStore()
const auth = useAuthStore()
const search = useSearchStore()
const router = useRouter()

const keyVisible = ref(false)
const settingsOpen = ref(false)

const maskedKey = computed(() =>
  keyVisible.value ? config.apiKey : '•'.repeat(Math.max(config.apiKey.length, 8)),
)

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

// Upstream text (index.html): "Security Risk: Your instance has external
// access enabled without using an admin password."
const warningText =
  'Security Risk: Your instance has external access enabled without using an admin password.'

function remindLater() {
  warningHiddenNow.value = true
}

function neverShowAgain() {
  localStorage.setItem(EXTERNAL_WARNING_KEY, 'true')
  warningDismissed.value = true
}

async function doLogout() {
  await auth.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <header class="app-header">
    <div v-if="showWarning" class="external-warning" role="alert">
      <i class="pi pi-exclamation-triangle" />
      <strong>{{ warningText }}</strong>
      <Button
        icon="pi pi-hourglass"
        title="Remind me again later"
        severity="success"
        size="small"
        @click="remindLater"
      />
      <Button
        icon="pi pi-eye-slash"
        title="Do not show again"
        severity="danger"
        size="small"
        @click="neverShowAgain"
      />
    </div>
    <div class="header-row">
      <div class="brand">
        <img src="/favicon.svg" alt="Logo" class="logo" />
        <span class="title">Vestt</span>
        <span v-if="config.appVersion" class="version">Version {{ config.appVersion }}</span>
      </div>
      <div class="header-actions">
        <Button
          icon="pi pi-search"
          label="Search"
          severity="success"
          @click="search.visible = true"
        />
        <div class="apikey-panel">
          <span class="input-header">API Key:</span>
          <InputText
            class="apikey-input"
            :model-value="maskedKey"
            :type="keyVisible ? 'text' : 'password'"
            readonly
            placeholder="API Key"
            @click="keyVisible = !keyVisible"
          />
          <Button
            :icon="keyVisible ? 'pi pi-eye-slash' : 'pi pi-eye'"
            :title="keyVisible ? 'Hide API Key' : 'Show API Key'"
            severity="secondary"
            size="small"
            @click="keyVisible = !keyVisible"
          />
          <CopyButton :text="config.apiKey" title="Copy API Key to clipboard" />
        </div>
        <Button
          icon="pi pi-cog"
          title="Settings"
          severity="secondary"
          @click="settingsOpen = true"
        />
        <Button
          v-if="auth.passwordSet"
          icon="pi pi-sign-out"
          label="Logout"
          severity="danger"
          @click="doLogout"
        />
      </div>
    </div>
    <SettingsDialog v-model:visible="settingsOpen" />
  </header>
</template>

<style scoped>
.external-warning {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 0.5rem 1rem;
  background: var(--p-red-50);
  color: var(--p-red-700);
  border-bottom: 1px solid var(--p-red-200);
}
.header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.75rem 1.5rem;
  border-bottom: 1px solid var(--p-surface-200);
  flex-wrap: wrap;
}
.brand {
  display: flex;
  align-items: center;
  gap: 0.75rem;
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
.apikey-panel {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}
.input-header {
  font-weight: 600;
}
.apikey-input {
  width: 22rem;
  font-family: monospace;
}
</style>
