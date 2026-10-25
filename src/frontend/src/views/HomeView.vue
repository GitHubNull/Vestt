<script setup lang="ts">
import Badge from 'primevue/badge'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import MultiSelect from 'primevue/multiselect'
import Select from 'primevue/select'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PresetBar from '../components/search/PresetBar.vue'
import SearchResultTable from '../components/search/SearchResultTable.vue'
import { notify } from '../components/common/notify'
import {
  consumePendingSearchHash,
  parseSearchHash,
  resolveFilterTrackers,
  restoreSearchHash,
} from '../services/searchHash'
import { TORZNAB_MAIN_CATEGORIES, categoryLabel } from '../services/torznabCategories'
import type { ManualSearchIndexerState } from '../services/types'
import { useConfigStore } from '../stores/config'
import { useIndexersStore } from '../stores/indexers'
import { useSearchStore } from '../stores/search'

// Search home (UI 改版): Google-style centered query box; results render
// inline below once a search runs. Owns the SPEC-13 §3.1.1 `#search=…`
// deep link (restore-and-execute) and the preset bar. The box's leading
// icon is a magnifier while empty and flips to a one-click clear button
// once at least one character is entered.

const { t } = useI18n()
const config = useConfigStore()
const indexers = useIndexersStore()
const search = useSearchStore()

const booting = ref(true)
const advancedOpen = ref(false)
const queryInput = ref<{ $el?: unknown } | null>(null)

// layout: centered hero before the first run, compact top bar afterwards.
const hasResults = computed(() => search.ran || search.running)

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
  if (s.Status === 2)
    return { label: t('home.statusOk', { results: s.Results, elapsed: s.ElapsedTime }), severity: 'success' as const }
  if (s.Status === 1) return { label: t('home.statusError'), severity: 'danger' as const }
  return { label: t('home.statusUnknown'), severity: 'secondary' as const }
}

function clearQuery(): void {
  search.query = ''
  const root = queryInput.value?.$el as HTMLElement | undefined
  root?.querySelector('input')?.focus()
}

onMounted(async () => {
  try {
    await Promise.all([config.load(), indexers.load()])
  } catch (err) {
    notify.danger(
      err instanceof Error ? err.message : t('errors.loadFailed'),
      true,
    )
  } finally {
    booting.value = false
  }
  // SPEC-13 §3.1.1: `#search=<q>&tracker=<id|all>&category=<cat>&filter=<t>`
  // opens the home page with the query executed. The pending fragment
  // survived the router redirect via sessionStorage (searchHash.ts).
  applySearchDeepLink(consumePendingSearchHash())
  window.addEventListener('hashchange', onHashChange)
})

onBeforeUnmount(() => {
  window.removeEventListener('hashchange', onHashChange)
})

function onHashChange(): void {
  applySearchDeepLink(window.location.hash)
}

function applySearchDeepLink(raw: string | null): void {
  if (!raw) return
  const link = parseSearchHash(raw)
  if (!link) return
  const trackers =
    link.trackers === 'all'
      ? resolveFilterTrackers(link.filter, indexers.all)
      : link.trackers
  search.applyParams({
    query: link.query,
    trackers,
    categories: link.category !== null ? [link.category] : undefined,
  })
  void search.run()
  restoreSearchHash(raw)
}
</script>

<template>
  <div class="home" :class="{ 'has-results': hasResults }">
    <div class="hero">
      <img src="/favicon.svg" alt="Logo" class="hero-logo" />
      <h1 class="hero-title">Vestt</h1>
      <div class="search-box glass-card">
        <Button
          v-if="search.query"
          icon="pi pi-times"
          class="clear-btn"
          severity="secondary"
          text
          rounded
          :aria-label="$t('home.clear')"
          :title="$t('home.clear')"
          @click="clearQuery"
        />
        <i v-else class="pi pi-search search-icon" />
        <InputText
          ref="queryInput"
          v-model="search.query"
          class="query-input"
          :placeholder="$t('home.placeholder')"
          :aria-label="$t('home.queryLabel')"
          @keyup.enter="search.run()"
        />
        <Button
          icon="pi pi-sliders-h"
          :severity="advancedOpen ? 'primary' : 'secondary'"
          text
          rounded
          :title="$t('home.advanced')"
          :aria-expanded="advancedOpen"
          @click="advancedOpen = !advancedOpen"
        />
        <Button
          id="jackett-search-perform"
          icon="pi pi-search"
          :label="$t('nav.search')"
          severity="success"
          :loading="search.running"
          :disabled="search.running"
          @click="search.run()"
        />
      </div>
      <div v-if="advancedOpen" class="advanced-row">
        <MultiSelect
          v-model="search.trackers"
          :options="trackerOptions"
          option-label="label"
          option-value="value"
          :placeholder="$t('home.allIndexers')"
          :max-selected-labels="3"
          class="tracker-select"
        />
        <Select
          v-model="selectedCategory"
          :options="categoryOptions"
          option-label="label"
          option-value="value"
          :placeholder="$t('home.allCategories')"
          show-clear
          class="category-select"
        />
      </div>
    </div>

    <PresetBar v-if="hasResults" class="preset-row" />

    <Message v-if="search.status === 'error'" severity="error" :closable="false" class="error-banner">
      {{ search.error }}
    </Message>

    <div v-if="statusLine" class="status-strip">
      <span class="status-title">{{ $t('home.doneUsing') }}</span>
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

    <div v-else-if="search.ran" class="glass-card results-pane">
      <SearchResultTable :results="search.results" :loading="false" />
    </div>

    <p v-else-if="!booting" class="home-hint">
      {{ $t('home.hint') }}
    </p>
  </div>
</template>

<style scoped>
.home {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-height: calc(100vh - 8rem);
  transition: justify-content 0.2s ease;
}
.home:not(.has-results) {
  justify-content: center;
}
/* 结果态锁定页面高度：滚动只发生在 DataTable 表体内部，玻璃面板本身不滚 */
.home.has-results {
  height: calc(100vh - 7rem);
  min-height: 0;
}
.hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.75rem;
  width: 100%;
  max-width: 46rem;
  margin-bottom: 1rem;
}
.hero-logo {
  height: 4.5rem;
}
.hero-title {
  margin: 0;
  font-size: 2.5rem;
  font-weight: 700;
}
.has-results .hero-logo,
.has-results .hero-title {
  display: none;
}
.search-box {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.5rem 0.5rem 0.75rem;
}
.search-icon {
  color: var(--p-text-muted-color);
  padding-left: 0.5rem;
}
.clear-btn {
  flex-shrink: 0;
}
.query-input {
  flex: 1;
  border: none;
  background: transparent;
  box-shadow: none;
  font-size: 1.1rem;
}
.query-input:focus {
  outline: none;
}
.advanced-row {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
  justify-content: center;
}
.tracker-select {
  min-width: 18rem;
}
.category-select {
  min-width: 14rem;
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
.skeleton-pane {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  width: 100%;
  max-width: 72rem;
  padding: 1rem 0;
}
.skeleton-row {
  height: 2rem;
  border-radius: 4px;
  background: var(--p-content-hover-background);
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
.results-pane {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  width: 100%;
  max-width: 72rem;
  padding: 1rem;
}
.home-hint {
  color: var(--p-text-muted-color);
}
</style>
