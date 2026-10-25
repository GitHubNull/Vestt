<script setup lang="ts">
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { notify } from '../common/notify'
import { useSearchStore } from '../../stores/search'

// Preset bar (SPEC-13 §3.1.5): save the current form under a name, apply or
// delete saved presets. Same-name save overwrites (upstream savedPresets
// dedup semantics). Presets persist in localStorage `vestt.presets`.

const { t } = useI18n()
const search = useSearchStore()

const presetName = ref('')
const selectedPreset = ref<string | null>(null)

const presetOptions = computed(() => search.presets.map((p) => p.name))

onMounted(() => {
  search.loadPresets()
})

function save(): void {
  const name = presetName.value.trim()
  if (!name) {
    notify.info(t('presets.nameFirst'))
    return
  }
  search.savePreset(name)
  presetName.value = ''
  notify.success(t('presets.saved', { name }))
}

function apply(): void {
  if (!selectedPreset.value) return
  search.applyPreset(selectedPreset.value)
  notify.info(t('presets.applied', { name: selectedPreset.value }))
}

function remove(): void {
  if (!selectedPreset.value) return
  search.deletePreset(selectedPreset.value)
  notify.success(t('presets.deleted', { name: selectedPreset.value }))
  selectedPreset.value = null
}
</script>

<template>
  <div class="preset-bar">
    <span class="preset-label">{{ $t('presets.label') }}</span>
    <Select
      v-model="selectedPreset"
      :options="presetOptions"
      :placeholder="$t('presets.select')"
      size="small"
      class="preset-select"
    />
    <Button :label="$t('presets.apply')" icon="pi pi-check" size="small" outlined :disabled="!selectedPreset" @click="apply" />
    <Button :label="$t('presets.delete')" icon="pi pi-trash" size="small" outlined severity="danger" :disabled="!selectedPreset" @click="remove" />
    <InputText v-model="presetName" :placeholder="$t('presets.namePlaceholder')" size="small" class="preset-name" @keyup.enter="save" />
    <Button :label="$t('presets.save')" icon="pi pi-save" size="small" :disabled="!presetName.trim()" @click="save" />
  </div>
</template>

<style scoped>
.preset-bar {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  flex-wrap: wrap;
}
.preset-label {
  font-weight: 600;
}
.preset-select {
  min-width: 12rem;
}
.preset-name {
  width: 12rem;
}
</style>
