<script setup lang="ts">
import Badge from 'primevue/badge'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import MultiSelect from 'primevue/multiselect'
import Select from 'primevue/select'
import { computed } from 'vue'
import PresetBar from './PresetBar.vue'
import SearchResultTable from './SearchResultTable.vue'
import { TORZNAB_MAIN_CATEGORIES, categoryLabel } from '../../services/torznabCategories'
import { useIndexersStore } from '../../stores/indexers'
import { useSearchStore } from '../../stores/search'
import type { ManualSearchIndexerState } from '../../services/types'

// Search dialog (SPEC-13 §3.1): query input (enter submits), tracker
// MultiSelect over configured indexers, Torznab main-class category Select
// (constant tree per SPEC §3.1.2), aggregate run, per-indexer status strip,
// preset bar and result table.

const search = useSearchStore()
const indexers = useIndexersStore()

const trackerOptions = computed(() =>
  indexers.configured.map((i) => ({ label: i.name, value: i.id })),
)

const categoryOptions = computed(() =>
  TORZNAB_MAIN_CATEGORIES.map((c) => ({ label: categoryLabel(c), value: c.ID })),
)

const selectedCategory = computed<number | null>({
  get: () => (search.categories.length ? (search.categories[0] ?? null) : null),
  set: (v: number | null) => {
    search.categories = v === null ? [] : [v]
  },
})

// Per-indexer status strip (SPEC §3.1.4): one line per targeted indexer —
// OK n results x ms / Error (text as tooltip) / Unknown while running.
const statusLine = computed(() => {
  if (search.running) return null
  if (!search.ran) return null
  return search.indexerStates
})

function stateBadge(s: ManualSearchIndexerState) {
  if (s.Status === 2) return { label: `OK ${s.Results} [${s.ElapsedTime}ms]`, severity: 'success' as const }
  if (s.Status === 1) return { label: 'Error', severity: 'danger' as const }
  return { label: 'Unknown', severity: 'secondary' as const }
}

function onClose(): void {
  search.visible = false
}
</script>

<template>
  <Dialog
    :visible="search.visible"
    modal
    :style="{ width: '90vw', maxWidth: '90rem' }"
    :draggable="false"
    :header="search.query ? `Search: ${search.query}` : 'Manual search'"
    @update:visible="(v: boolean) => (search.visible = v)"
    @hide="onClose"
  >
    <p class="hint">You can search all configured indexers from this screen.</p>

    <div class="search-form">
      <label for="searchquery">Query</label>
      <InputText
        id="searchquery"
        v-model="search.query"
        class="query-input"
        placeholder="Search query"
        @keyup.enter="search.run()"
      />

      <label for="searchTracker">Tracker</label>
      <MultiSelect
        id="searchTracker"
        v-model="search.trackers"
        :options="trackerOptions"
        option-label="label"
        option-value="value"
        placeholder="All indexers"
        :max-selected-labels="3"
        class="tracker-select"
      />

      <label for="searchCategory">Category</label>
      <Select
        id="searchCategory"
        v-model="selectedCategory"
        :options="categoryOptions"
        option-label="label"
        option-value="value"
        placeholder="All"
        show-clear
        class="category-select"
      />

      <Button
        id="jackett-search-perform"
        icon="pi pi-search"
        label="Search"
        severity="success"
        :loading="search.running"
        :disabled="search.running"
        @click="search.run()"
      />
    </div>

    <PresetBar class="preset-row" />

    <Message v-if="search.status === 'error'" severity="error" :closable="false" class="error-banner">
      {{ search.error }}
    </Message>

    <div v-if="statusLine" class="status-strip">
      <span class="status-title">Your search was done using:</span>
      <span
        v-for="s in statusLine"
        :key="s.ID"
        :title="s.Error ?? ''"
        class="status-item"
      >
        <Badge :value="stateBadge(s).label" :severity="stateBadge(s).severity" />
        <span class="status-name">{{ s.Name }}</span>
      </span>
    </div>

    <div v-if="search.running" class="skeleton-pane" aria-busy="true">
      <div class="skeleton-row" v-for="i in 5" :key="i" />
    </div>

    <SearchResultTable
      v-else-if="search.ran"
      :results="search.results"
      :loading="false"
    />
  </Dialog>
</template>

<style scoped>
.hint {
  margin: 0 0 0.75rem;
  color: var(--p-text-muted-color);
}
.search-form {
  display: grid;
  grid-template-columns: max-content 1fr max-content 16rem max-content 12rem max-content;
  gap: 0.5rem 0.75rem;
  align-items: center;
  margin-bottom: 0.75rem;
}
.query-input {
  width: 100%;
}
.tracker-select {
  min-width: 16rem;
}
.category-select {
  min-width: 12rem;
}
.preset-row {
  margin-bottom: 0.75rem;
}
.error-banner {
  margin-bottom: 0.75rem;
}
.status-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 1rem;
  align-items: center;
  margin-bottom: 0.75rem;
  font-size: 0.85rem;
}
.status-title {
  font-weight: 600;
}
.status-item {
  display: inline-flex;
  gap: 0.35rem;
  align-items: center;
}
.status-item.error {
  color: var(--p-red-500);
}
.skeleton-pane {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 1rem 0;
}
.skeleton-row {
  height: 2rem;
  border-radius: 4px;
  background: var(--p-surface-200);
  animation: pulse 1.2s ease-in-out infinite;
}
@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}
</style>
