<script setup lang="ts">
import { reactive, watch } from 'vue'
import type { ConfigFieldDef, ConfigItem } from '../../services/types'
import DynamicField from './DynamicField.vue'
import { buildConfigItems, initialValues } from '../../stores/indexers'

// DynamicForm (frontend.md §5): renders a ConfigFieldDef[] and assembles
// the POST Config payload. The parent owns submission (dialog) or calls
// buildItems() directly (batch add).

const props = defineProps<{
  fields: ConfigFieldDef[]
}>()

const values = reactive<Record<string, unknown>>({})

function reset(fields: ConfigFieldDef[]): void {
  const init = initialValues(fields)
  for (const key of Object.keys(values)) delete values[key]
  Object.assign(values, init)
}

watch(
  () => props.fields,
  (fields) => reset(fields),
  { immediate: true, deep: true },
)

/** Assembles the POST payload from the current values. */
function buildItems(): ConfigItem[] {
  return buildConfigItems(props.fields, values)
}

/** Replaces the value of one field (alternativesitelinks → sitelink). */
function setValue(id: string, value: unknown): void {
  values[id] = value
}

defineExpose({ buildItems, setValue, values })
</script>

<template>
  <form class="dynamic-form" @submit.prevent>
    <DynamicField
      v-for="field in fields"
      :key="field.id"
      :field="field"
      :model-value="values[field.id]"
      @update:model-value="values[field.id] = $event"
    />
  </form>
</template>

<style scoped>
.dynamic-form {
  display: flex;
  flex-direction: column;
}
</style>
