<script setup lang="ts">
import Badge from 'primevue/badge'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import { computed } from 'vue'
import type { IndexerDTO } from '../../services/types'

// Placeholder tables for the two tabs (SPEC-11 §3.5): row counts plus a
// read-only name/type overview; full row actions land in SPEC-12.
const props = defineProps<{
  indexers: IndexerDTO[]
  mode: 'configured' | 'unconfigured'
  loading: boolean
}>()

const heading = computed(() =>
  props.mode === 'configured'
    ? `${props.indexers.length} configured indexer${props.indexers.length === 1 ? '' : 's'}`
    : `${props.indexers.length} unconfigured indexer${props.indexers.length === 1 ? '' : 's'}`,
)

const emptyText = computed(() =>
  props.mode === 'configured'
    ? 'No indexers configured yet.'
    : 'All indexers are configured.',
)
</script>

<template>
  <div class="indexer-table">
    <p class="row-count">{{ heading }}</p>
    <div v-if="!loading && indexers.length === 0" class="empty-state">
      <i class="pi pi-inbox empty-icon" />
      <p>{{ emptyText }}</p>
      <p class="empty-hint">Indexer management lands in SPEC-12.</p>
    </div>
    <DataTable v-else :value="indexers" :loading="loading" size="small">
      <Column field="name" header="Indexer" />
      <Column field="type" header="Type">
        <template #body="{ data }">
          <Badge :value="data.type" :severity="data.type === 'private' ? 'danger' : 'secondary'" />
        </template>
      </Column>
      <Column field="language" header="Language" />
    </DataTable>
  </div>
</template>

<style scoped>
.row-count {
  margin: 0 0 0.5rem;
  color: var(--p-text-muted-color);
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
.empty-hint {
  font-size: 0.85rem;
}
</style>
