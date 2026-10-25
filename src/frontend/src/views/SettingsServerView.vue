<script setup lang="ts">
import { onMounted } from 'vue'
import ApiKeyPanel from '../components/settings/ApiKeyPanel.vue'
import ServerConfigForm from '../components/settings/ServerConfigForm.vue'
import { useSettingsStore } from '../stores/settings'

// Server config page (UI 改版, moved out of the settings dialog): entering
// initializes the draft from the loaded server config (upstream
// loadJackettSettings field loading). The API key panel lives here too —
// it's part of the server config, not a standalone page.
const settings = useSettingsStore()

onMounted(() => {
  settings.open()
})
</script>

<template>
  <div class="settings-page">
    <h1 class="page-title">{{ $t('server.title') }}</h1>
    <div class="glass-card settings-card">
      <ServerConfigForm />
    </div>
    <h2 class="section-title">{{ $t('apiKey.title') }}</h2>
    <div class="glass-card settings-card">
      <ApiKeyPanel />
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  max-width: 72rem;
  margin: 0 auto;
}
.page-title {
  margin: 0 0 1rem;
  font-size: 1.5rem;
}
.section-title {
  margin: 1.5rem 0 1rem;
  font-size: 1.15rem;
}
.settings-card {
  padding: 1.25rem;
}
</style>
