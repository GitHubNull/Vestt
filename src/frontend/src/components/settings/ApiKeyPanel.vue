<script setup lang="ts">
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import { computed, ref } from 'vue'
import CopyButton from '../common/CopyButton.vue'
import { getBasePath } from '../../services/api'
import { useConfigStore } from '../../stores/config'

// API key panel (SPEC-13 §3.5): masked key with reveal toggle and copy
// button (upstream header parity, moved here in the UI rework) plus the
// Torznab base URL example. Lives on the server config page — the API key
// is server configuration, not a standalone destination.

const config = useConfigStore()

const keyVisible = ref(false)
const maskedKey = computed(() =>
  keyVisible.value ? config.apiKey : '•'.repeat(Math.max(config.apiKey.length, 8)),
)

const torznabBase = computed(
  () => `${location.origin}${getBasePath()}/api/v2.0/indexers/`,
)
</script>

<template>
  <div class="api-key-panel">
    <div class="field">
      <span class="label">{{ $t('apiKey.label') }}</span>
      <InputText
        :model-value="maskedKey"
        :type="keyVisible ? 'text' : 'password'"
        readonly
        class="key-input"
        @click="keyVisible = !keyVisible"
      />
      <Button
        :icon="keyVisible ? 'pi pi-eye-slash' : 'pi pi-eye'"
        :title="keyVisible ? $t('apiKey.hide') : $t('apiKey.show')"
        severity="secondary"
        size="small"
        @click="keyVisible = !keyVisible"
      />
      <CopyButton :text="config.apiKey" :title="$t('apiKey.copy')" />
    </div>
    <div class="field">
      <span class="label">{{ $t('apiKey.torznabBase') }}</span>
      <InputText :model-value="torznabBase" readonly class="key-input" />
      <CopyButton :text="torznabBase" :title="$t('apiKey.copyTorznab')" />
    </div>
    <p class="hint">
      {{ $t('apiKey.hint') }}
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
