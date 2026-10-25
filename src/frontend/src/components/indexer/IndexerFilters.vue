<script setup lang="ts">
import Badge from 'primevue/badge'
import Select from 'primevue/select'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IndexerDTO } from '../../services/types'
import type { IndexerFilters } from '../../stores/indexers'

// Filter bar (SPEC-11 §3.5): type / tag / state dropdowns with counts.
// Selection wiring into the tables lands with SPEC-12; this component is
// purely driven by props.
const props = defineProps<{
  modelValue: IndexerFilters
  indexers: IndexerDTO[]
}>()
const emit = defineEmits<{ 'update:modelValue': [IndexerFilters] }>()

const { t } = useI18n()

const TYPE_OPTIONS = computed(() => [
  { label: t('filters.all'), value: 'all' },
  { label: t('filters.public'), value: 'public' },
  { label: t('filters.semiPrivate'), value: 'semi-private' },
  { label: t('filters.private'), value: 'private' },
])

const STATE_OPTIONS = computed(() => [
  { label: t('filters.all'), value: 'all' },
  { label: t('filters.healthy'), value: 'healthy' },
  { label: t('filters.failing'), value: 'failing' },
  { label: t('filters.untested'), value: 'untested' },
])

interface Option {
  label: string
  value: string
  count: number
}

function countBy(pred: (i: IndexerDTO) => boolean): number {
  return props.indexers.filter(pred).length
}

/** distinct tags, case-insensitively merged (SPEC-12 §3.2 tag_filter). */
const mergedTags = computed<string[]>(() => {
  const seen = new Map<string, string>()
  for (const i of props.indexers) {
    for (const t of i.tags) {
      const k = t.toLowerCase()
      if (!seen.has(k)) seen.set(k, t)
    }
  }
  return [...seen.values()].sort((a, b) => a.localeCompare(b))
})

const typeOptions = computed<Option[]>(() =>
  TYPE_OPTIONS.value.map((o) => ({
    ...o,
    count: o.value === 'all' ? props.indexers.length : countBy((i) => i.type === o.value),
  })),
)

const tagOptions = computed<Option[]>(() => [
  { label: t('filters.all'), value: 'all', count: props.indexers.length },
  ...mergedTags.value.map((tag) => ({
    label: tag,
    value: tag,
    count: countBy((i) => i.tags.some((x) => x.toLowerCase() === tag.toLowerCase())),
  })),
])

const stateOptions = computed<Option[]>(() =>
  STATE_OPTIONS.value.map((o) => ({
    ...o,
    count:
      o.value === 'all'
        ? props.indexers.length
        : o.value === 'healthy'
          ? countBy((i) => i.configured && i.last_error === null)
          : o.value === 'failing'
            ? countBy((i) => i.last_error !== null && i.last_error !== '')
            : countBy((i) => !i.configured),
  })),
)

function update(patch: Partial<IndexerFilters>) {
  emit('update:modelValue', { ...props.modelValue, ...patch })
}
</script>

<template>
  <div class="indexer-filters">
    <div class="filter-field">
      <label>{{ $t('filters.type') }}</label>
      <Select
        :model-value="modelValue.type"
        :options="typeOptions"
        option-label="label"
        option-value="value"
        @update:model-value="update({ type: $event as IndexerFilters['type'] })"
      >
        <template #option="{ option }">
          <span>{{ option.label }}</span>
          <Badge :value="option.count" severity="secondary" />
        </template>
      </Select>
    </div>
    <div class="filter-field">
      <label>{{ $t('filters.tag') }}</label>
      <Select
        :model-value="modelValue.tag"
        :options="tagOptions"
        option-label="label"
        option-value="value"
        @update:model-value="update({ tag: $event })"
      >
        <template #option="{ option }">
          <span>{{ option.label }}</span>
          <Badge :value="option.count" severity="secondary" />
        </template>
      </Select>
    </div>
    <div class="filter-field">
      <label>{{ $t('filters.status') }}</label>
      <Select
        :model-value="modelValue.state"
        :options="stateOptions"
        option-label="label"
        option-value="value"
        @update:model-value="update({ state: $event as IndexerFilters['state'] })"
      >
        <template #option="{ option }">
          <span>{{ option.label }}</span>
          <Badge :value="option.count" severity="secondary" />
        </template>
      </Select>
    </div>
  </div>
</template>

<style scoped>
.indexer-filters {
  display: flex;
  gap: 1.5rem;
  padding: 1rem 1.5rem;
  align-items: flex-end;
  flex-wrap: wrap;
}
.filter-field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 12rem;
}
.filter-field label {
  font-weight: 600;
  font-size: 0.85rem;
}
</style>
