<script setup lang="ts">
import ConfirmDialog from 'primevue/confirmdialog'
import Skeleton from 'primevue/skeleton'
import Tab from 'primevue/tab'
import TabList from 'primevue/tablist'
import TabPanel from 'primevue/tabpanel'
import TabPanels from 'primevue/tabpanels'
import Tabs from 'primevue/tabs'
import { useConfirm } from 'primevue/useconfirm'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppHeader from '../components/common/AppHeader.vue'
import { notify } from '../components/common/notify'
import IndexerConfigDialog from '../components/config/IndexerConfigDialog.vue'
import IndexerFilters from '../components/indexer/IndexerFilters.vue'
import IndexerTable from '../components/indexer/IndexerTable.vue'
import SearchDialog from '../components/search/SearchDialog.vue'
import { useConfigStore } from '../stores/config'
import { serializeFilters, useIndexersStore } from '../stores/indexers'
import { useSearchStore } from '../stores/search'
import {
  consumePendingSearchHash,
  parseSearchHash,
  resolveFilterTrackers,
  restoreSearchHash,
} from '../services/searchHash'
import type { IndexerDTO } from '../services/types'

// Dashboard (SPEC-11 shell + SPEC-12 wiring + SPEC-13 search): filter
// deep-link sync on the route query, tables with row actions, config dialog,
// delete confirm, search dialog with hash deep-link restore-and-execute.

const config = useConfigStore()
const indexers = useIndexersStore()
const search = useSearchStore()
const route = useRoute()
const router = useRouter()
const confirm = useConfirm()

const booting = ref(true)
const configDialogVisible = ref(false)
const configTarget = ref<IndexerDTO | null>(null)

onMounted(async () => {
  // restore filter deep-link before the first paint of the tables
  indexers.applyFilterDeepLink(route.query.filter)
  try {
    await Promise.all([config.load(), indexers.load()])
  } catch (err) {
    notify.danger(
      err instanceof Error ? err.message : 'Error loading Jackett settings, request to Jackett server failed, is server running ?',
      true,
    )
  } finally {
    booting.value = false
  }
  // SPEC-13 §3.1.1: `#search=<q>&tracker=<id|all>&category=<cat>&filter=<t>`
  // opens the dialog and executes. The pending fragment survived the router
  // redirect via sessionStorage (searchHash.ts).
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
  search.open({
    query: link.query,
    trackers,
    categories: link.category !== null ? [link.category] : undefined,
  })
  void search.run()
  restoreSearchHash(raw)
}

// filter → route query deep link (#/dashboard?indexers=1&filter=<json>).
watch(
  () => indexers.filters,
  (f) => {
    const filter = serializeFilters(f)
    const query = { ...route.query }
    if (filter === null) {
      delete query.indexers
      delete query.filter
    } else {
      query.indexers = '1'
      query.filter = filter
    }
    void router.replace({ query })
  },
  { deep: true },
)

// route query → store (e.g. a manually edited / shared URL).
watch(
  () => route.query.filter,
  (raw) => indexers.applyFilterDeepLink(raw),
)

function openConfig(indexer: IndexerDTO): void {
  configTarget.value = indexer
  configDialogVisible.value = true
}

async function onConfigured(indexerId: string, wasConfigured: boolean): Promise<void> {
  await indexers.load(true)
  // a newly added indexer gets an explicit test run so its badge reflects
  // the result (SPEC-12 §3.3.6 自动触发一次 Test; the backend already
  // auto-tests when the engine requires it — this refreshes the badge).
  if (!wasConfigured) await indexers.test(indexerId)
}

function askDelete(indexer: IndexerDTO): void {
  confirm.require({
    header: 'Delete indexer',
    message: `Are you sure you want to delete the indexer ${indexer.name}?`,
    icon: 'pi pi-exclamation-triangle',
    acceptLabel: 'Delete',
    rejectLabel: 'Cancel',
    acceptClass: 'p-button-danger',
    accept: () => void indexers.remove(indexer.id),
  })
}

function gotoSearch(indexer: IndexerDTO): void {
  // SPEC-13 §3.1.1: the row search button opens the dialog with the tracker
  // preselected. Unlike upstream (which writes location.hash and would
  // auto-run on refresh), no hash is written — see SPEC-13 澄清记录.
  search.reset()
  search.open({ trackers: [indexer.id] })
}
</script>

<template>
  <main class="dashboard">
    <AppHeader />
    <div v-if="booting" class="skeleton-pane" aria-busy="true">
      <Skeleton height="3rem" class="mb-4" />
      <Skeleton height="2rem" width="30%" class="mb-4" />
      <Skeleton height="16rem" />
    </div>
    <template v-else>
      <IndexerFilters v-model="indexers.filters" :indexers="indexers.configured" />
      <Tabs value="configured" class="indexer-tabs">
        <TabList>
          <Tab value="configured">Configured</Tab>
          <Tab value="unconfigured">Unconfigured</Tab>
        </TabList>
        <TabPanels>
          <TabPanel value="configured">
            <IndexerTable
              :indexers="indexers.filteredConfigured"
              :total="indexers.configured.length"
              mode="configured"
              :loading="indexers.loading"
              :test-states="indexers.testStates"
              @configure="openConfig"
              @delete="askDelete"
              @search="gotoSearch"
              @test="(i) => indexers.test(i.id)"
              @test-all="indexers.testAll()"
            />
          </TabPanel>
          <TabPanel value="unconfigured">
            <IndexerTable
              :indexers="indexers.unconfigured"
              :total="indexers.unconfigured.length"
              mode="unconfigured"
              :loading="indexers.loading"
              :test-states="indexers.testStates"
              @configure="openConfig"
              @add-selected="(ids) => indexers.addIndexers(ids)"
            />
          </TabPanel>
        </TabPanels>
      </Tabs>
    </template>

    <IndexerConfigDialog
      v-model="configDialogVisible"
      :indexer="configTarget"
      @saved="onConfigured"
    />
    <SearchDialog />
    <ConfirmDialog />
  </main>
</template>

<style scoped>
.dashboard {
  min-height: 100vh;
  background: var(--p-surface-50);
}
.skeleton-pane {
  padding: 1.5rem;
}
.mb-4 {
  margin-bottom: 1rem;
}
.indexer-tabs {
  padding: 0 1.5rem 2rem;
}
</style>
