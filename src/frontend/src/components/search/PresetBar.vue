<script setup lang="ts">
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import { computed, onMounted, ref } from 'vue'
import { notify } from '../common/notify'
import { useSearchStore } from '../../stores/search'

// Preset bar (SPEC-13 §3.1.5): save the current form under a name, apply or
// delete saved presets. Same-name save overwrites (upstream savedPresets
// dedup semantics). Presets persist in localStorage `vestt.presets`.

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
    notify.info('Enter a preset name first')
    return
  }
  search.savePreset(name)
  presetName.value = ''
  notify.success(`Preset "${name}" saved`)
}

function apply(): void {
  if (!selectedPreset.value) return
  search.applyPreset(selectedPreset.value)
  notify.info(`Preset "${selectedPreset.value}" applied`)
}

function remove(): void {
  if (!selectedPreset.value) return
  search.deletePreset(selectedPreset.value)
  notify.success(`Preset "${selectedPreset.value}" deleted`)
  selectedPreset.value = null
}
</script>

<template>
  <div class="preset-bar">
    <span class="preset-label">Presets:</span>
    <Select
      v-model="selectedPreset"
      :options="presetOptions"
      placeholder="Select preset"
      size="small"
      class="preset-select"
    />
    <Button label="Apply" icon="pi pi-check" size="small" outlined :disabled="!selectedPreset" @click="apply" />
    <Button label="Delete" icon="pi pi-trash" size="small" outlined severity="danger" :disabled="!selectedPreset" @click="remove" />
    <InputText v-model="presetName" placeholder="Preset name" size="small" class="preset-name" @keyup.enter="save" />
    <Button label="Save" icon="pi pi-save" size="small" :disabled="!presetName.trim()" @click="save" />
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
