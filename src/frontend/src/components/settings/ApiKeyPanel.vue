<script setup lang="ts">
import InputText from 'primevue/inputtext'
import { computed } from 'vue'
import CopyButton from '../common/CopyButton.vue'
import { getBasePath } from '../../services/api'
import { useConfigStore } from '../../stores/config'

// API key tab (SPEC-13 §3.5): full key with copy button plus the Torznab
// base URL example.

const config = useConfigStore()

const torznabBase = computed(
  () => `${location.origin}${getBasePath()}/api/v2.0/indexers/`,
)
</script>

<template>
  <div class="api-key-panel">
    <div class="field">
      <span class="label">API Key:</span>
      <InputText :model-value="config.apiKey" readonly class="key-input" />
      <CopyButton :text="config.apiKey" title="Copy API Key to clipboard" />
    </div>
    <div class="field">
      <span class="label">Torznab base URL:</span>
      <InputText :model-value="torznabBase" readonly class="key-input" />
      <CopyButton :text="torznabBase" title="Copy Torznab base URL to clipboard" />
    </div>
    <p class="hint">
      Use the API key as the password when adding an indexer to your applications.
    </p>
  </div>
</template>

<style scoped>
.api-key-panel {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.field {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}
.label {
  font-weight: 600;
  min-width: 10rem;
}
.key-input {
  flex: 1;
  font-family: monospace;
}
.hint {
  color: var(--p-text-muted-color);
  margin: 0;
}
</style>
