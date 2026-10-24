<script setup lang="ts">
import Badge from 'primevue/badge'
import Button from 'primevue/button'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import { computed, ref } from 'vue'
import CopyButton from '../common/CopyButton.vue'
import { getBasePath } from '../../services/api'
import { badgeOf } from '../../stores/indexers'
import type { IndexerDTO } from '../../services/types'
import type { TestRunState } from '../../stores/indexers'

// Configured / unconfigured indexer tables (SPEC-12 §3.1). Purely
// presentational: every mutation is emitted and owned by the dashboard.

const props = defineProps<{
  indexers: IndexerDTO[]
  total: number
  mode: 'configured' | 'unconfigured'
  loading: boolean
  testStates: Record<string, TestRunState>
}>()
const emit = defineEmits<{
  configure: [indexer: IndexerDTO]
  delete: [indexer: IndexerDTO]
  search: [indexer: IndexerDTO]
  test: [indexer: IndexerDTO]
  testAll: []
  addSelected: [ids: string[]]
}>()

const selected = ref<IndexerDTO[]>([])

const heading = computed(
  () => `Showing ${props.indexers.length} of ${props.total}`,
)

const emptyText = computed(() =>
  props.mode === 'configured'
    ? 'No indexers configured yet.'
    : 'All indexers are configured.',
)

function badge(indexer: IndexerDTO) {
  const state = badgeOf(indexer, props.testStates[indexer.id])
  return {
    healthy: { label: 'Healthy', severity: 'success' as const },
    failing: { label: 'Failing', severity: 'danger' as const },
    untested: { label: 'Untested', severity: 'secondary' as const },
  }[state]
}

function torznabUrl(indexer: IndexerDTO): string {
  return `${getBasePath()}/api/v2.0/indexers/${indexer.id}/results/torznab/`
}

function typeSeverity(type: string): 'success' | 'danger' | 'warn' | 'secondary' {
  if (type === 'public') return 'success'
  if (type === 'private') return 'danger'
  if (type === 'semi-private') return 'warn'
  return 'secondary'
}

function testIcon(indexer: IndexerDTO): string {
  switch (props.testStates[indexer.id]) {
    case 'running':
      return 'pi pi-spin pi-spinner'
    case 'ok':
      return 'pi pi-check-circle'
    case 'fail':
      return 'pi pi-times-circle'
    default:
      return 'pi pi-circle'
  }
}

function addSelected(): void {
  emit(
    'addSelected',
    selected.value.map((i) => i.id),
  )
  selected.value = []
}
</script>

<template>
  <div class="indexer-table">
    <div class="table-toolbar">
      <p class="row-count">{{ heading }}</p>
      <div class="toolbar-actions">
        <Button
          v-if="mode === 'configured' && total > 0"
          label="Test All"
          icon="pi pi-bolt"
          size="small"
          severity="warning"
          outlined
          @click="emit('testAll')"
        />
        <Button
          v-if="mode === 'unconfigured'"
          label="Add Selected"
          icon="pi pi-plus"
          size="small"
          :disabled="selected.length === 0"
          @click="addSelected"
        />
      </div>
    </div>

    <div v-if="!loading && indexers.length === 0" class="empty-state">
      <i class="pi pi-inbox empty-icon" />
      <p>{{ emptyText }}</p>
    </div>

    <DataTable
      v-else
      :value="indexers"
      :loading="loading"
      size="small"
      sort-field="name"
      :sort-order="1"
      data-key="id"
      v-model:selection="selected"
    >
      <Column
        v-if="mode === 'unconfigured'"
        selection-mode="multiple"
        header-style="width: 3rem"
      />

      <Column v-if="mode === 'configured'" header="Status" header-style="width: 6rem">
        <template #body="{ data }">
          <span :title="data.last_error ?? ''">
            <Badge :value="badge(data).label" :severity="badge(data).severity" />
          </span>
        </template>
      </Column>

      <Column field="name" header="Indexer" sortable>
        <template #body="{ data }">
          <a
            :href="data.site_link"
            target="_blank"
            rel="noreferrer"
            :title="data.description"
          >{{ data.name }}</a>
        </template>
      </Column>

      <Column field="type" header="Type" header-style="width: 7rem">
        <template #body="{ data }">
          <Badge
            :value="data.type"
            :severity="typeSeverity(data.type)"
            style="text-transform: capitalize"
          />
        </template>
      </Column>

      <Column field="language" header="Language" header-style="width: 7rem" />

      <Column header="Tags" header-style="width: 10rem">
        <template #body="{ data }">
          <span v-for="tag in data.tags" :key="tag" class="tag-chip" :title="tag">{{ tag }}</span>
        </template>
      </Column>

      <Column
        v-if="mode === 'configured'"
        header="Caps"
        header-style="width: 5rem"
      >
        <template #body="{ data }">{{ data.caps.length }}</template>
      </Column>

      <Column header-style="width: 1%" :header="mode === 'configured' ? 'Actions' : ''">
        <template #body="{ data }">
          <div class="indexer-buttons">
            <CopyButton
              v-if="mode === 'configured'"
              :text="torznabUrl(data)"
              title="Copy Torznab Feed"
            />
            <Button
              v-if="mode === 'configured'"
              icon="pi pi-search"
              title="Search"
              severity="success"
              size="small"
              outlined
              @click="emit('search', data)"
            />
            <Button
              icon="pi pi-cog"
              :title="mode === 'configured' ? 'Configure' : 'Configure'"
              size="small"
              outlined
              @click="emit('configure', data)"
            />
            <Button
              v-if="mode === 'configured'"
              :icon="testIcon(data)"
              :title="data.last_error ?? 'Test'"
              :loading="testStates[data.id] === 'running'"
              severity="warning"
              size="small"
              outlined
              label="Test"
              @click="emit('test', data)"
            />
            <Button
              v-if="mode === 'configured'"
              icon="pi pi-trash"
              title="Delete"
              severity="danger"
              size="small"
              outlined
              @click="emit('delete', data)"
            />
          </div>
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.table-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.5rem;
}
.row-count {
  margin: 0;
  color: var(--p-text-muted-color);
}
.toolbar-actions {
  display: flex;
  gap: 0.5rem;
}
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  padding: 3rem 1rem;
  color: var(--p-text-muted-color);
}
.empty-icon {
  font-size: 3rem;
}
.indexer-buttons {
  display: flex;
  gap: 0.25rem;
  justify-content: flex-end;
  flex-wrap: wrap;
}
.tag-chip {
  display: inline-block;
  background: var(--p-surface-200);
  color: var(--p-text-color);
  border-radius: 999px;
  padding: 0.05rem 0.5rem;
  margin: 0.1rem 0.15rem 0.1rem 0;
  font-size: 0.75rem;
}
</style>
