<script setup lang="ts">
import Checkbox from 'primevue/checkbox'
import InputChips from 'primevue/inputchips'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import MultiSelect from 'primevue/multiselect'
import Password from 'primevue/password'
import Select from 'primevue/select'
import { computed } from 'vue'
import type { ConfigFieldDef } from '../../services/types'

// One dynamic config field (frontend.md §5 mapping table): renders the
// PrimeVue component matching the backend ConfigFieldType and keeps the
// value in the parent form's values record.

const props = defineProps<{
  field: ConfigFieldDef
  modelValue: unknown
}>()
const emit = defineEmits<{ 'update:modelValue': [unknown] }>()

function set(value: unknown): void {
  emit('update:modelValue', value)
}

const selectOptions = computed(() =>
  Object.entries(props.field.options ?? {}).map(([value, label]) => ({ label, value })),
)

const isInfo = computed(() => props.field.type === 'displayinfo')
const isImage = computed(() => props.field.type === 'displayimage')
</script>

<template>
  <div v-if="field.type !== 'hiddendata'" class="dynamic-field" :data-field-id="field.id" :data-field-type="field.type">
    <label v-if="field.type !== 'displayinfo' && field.type !== 'displayimage'" class="field-label">
      {{ field.name }}
    </label>

    <Message v-if="isInfo" severity="info" :closable="false">
      <!-- upstream renders displayinfo raw ({{{value}}}); the content comes
           from local GPL definition files, not user input. -->
      <span v-html="field.value" />
    </Message>

    <template v-else-if="isImage">
      <img
        class="field-image"
        :src="String(field.value ?? '')"
        alt="No image available"
        data-role="displayimage"
      />
    </template>

    <InputText
      v-else-if="field.type === 'inputstring'"
      :model-value="(modelValue as string)"
      class="field-input"
      :data-role="'inputstring'"
      @update:model-value="set($event)"
    />

    <Password
      v-else-if="field.type === 'password'"
      :model-value="(modelValue as string)"
      :feedback="false"
      toggle-mask
      class="field-input"
      :input-style="{ width: '100%' }"
      :data-role="'password'"
      @update:model-value="set($event)"
    />

    <div v-else-if="field.type === 'inputbool'" class="field-checkbox">
      <Checkbox
        :model-value="(modelValue as boolean)"
        :binary="true"
        :input-id="`cfg-${field.id}`"
        :data-role="'inputbool'"
        @update:model-value="set($event)"
      />
    </div>

    <MultiSelect
      v-else-if="field.type === 'inputcheckbox'"
      :model-value="(modelValue as string[])"
      :options="selectOptions"
      option-label="label"
      option-value="value"
      class="field-input"
      :data-role="'inputcheckbox'"
      @update:model-value="set($event)"
    />

    <Select
      v-else-if="field.type === 'inputselect'"
      :model-value="(modelValue as string)"
      :options="selectOptions"
      option-label="label"
      option-value="value"
      class="field-input"
      :data-role="'inputselect'"
      @update:model-value="set($event)"
    />

    <InputChips
      v-else-if="field.type === 'inputtags'"
      :model-value="(modelValue as string[])"
      class="field-input"
      :data-role="'inputtags'"
      @update:model-value="set($event)"
    />
  </div>
</template>

<style scoped>
.dynamic-field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  margin-bottom: 0.9rem;
}
.field-label {
  font-weight: 600;
  font-size: 0.85rem;
}
.field-input {
  width: 100%;
}
.field-image {
  max-width: 100%;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--p-content-border-radius);
}
.field-checkbox {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
</style>
