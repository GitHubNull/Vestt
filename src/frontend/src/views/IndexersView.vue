<script setup lang="ts">
import ConfirmDialog from 'primevue/confirmdialog'
import Skeleton from 'primevue/skeleton'
import Tab from 'primevue/tab'
import TabList from 'primevue/tablist'
import TabPanel from 'primevue/tabpanel'
import TabPanels from 'primevue/tabpanels'
import Tabs from 'primevue/tabs'
import { useConfirm } from 'primevue/useconfirm'
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { notify } from '../components/common/notify'
import IndexerConfigDialog from '../components/config/IndexerConfigDialog.vue'
import IndexerFilters from '../components/indexer/IndexerFilters.vue'
import IndexerTable from '../components/indexer/IndexerTable.vue'
import { serializeFilters, useIndexersStore } from '../stores/indexers'
import { useSearchStore } from '../stores/search'
import { useConfigStore } from '../stores/config'
import type { IndexerDTO } from '../services/types'

// Indexer management (UI 改版, moved off the dashboard): filter deep-link
// sync on the route query, tables with row actions, config dialog and delete
// confirm. Row "Search" hands off to the home page with the tracker
// preselected (SPEC-13 澄清记录: no hash is written).

const { t } = useI18n()
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
    notify.danger(err instanceof Error ? err.message : t('errors.loadFailed'), true)
  } finally {
    booting.value = false
  }
})

// filter → route query deep link (#/indexers?indexers=1&filter=<json>).
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
    header: t('indexersView.deleteTitle'),
    message: t('indexersView.deleteMessage', { name: indexer.name }),
    icon: 'pi pi-exclamation-triangle',
    acceptLabel: t('indexersView.deleteConfirm'),
    rejectLabel: t('indexersView.cancel'),
    acceptClass: 'p-button-danger',
    accept: () => void indexers.remove(indexer.id),
  })
}

function gotoSearch(indexer: IndexerDTO): void {
  // SPEC-13 §3.1.1: the row search button lands on the home page with the
  // tracker preselected. Unlike upstream (which writes location.hash and
  // would auto-run on refresh), no hash is written — see SPEC-13 澄清记录.
  search.reset()
  search.applyParams({ trackers: [indexer.id] })
  void router.push({ name: 'home' })
}
</script>

<template>
  <div class="indexers-page">
    <div v-if="booting" class="skeleton-pane" aria-busy="true">
      <Skeleton height="3rem" class="mb-4" />
      <Skeleton height="2rem" width="30%" class="mb-4" />
      <Skeleton height="16rem" />
    </div>
    <template v-else>
      <IndexerFilters v-model="indexers.filters" :indexers="indexers.configured" />
      <Tabs value="configured" class="indexer-tabs">
        <TabList>
          <Tab value="configured">{{ $t('indexersView.configured') }}</Tab>
          <Tab value="unconfigured">{{ $t('indexersView.unconfigured') }}</Tab>
        </TabList>
        <TabPanels class="glass-card tab-panels">
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
    <ConfirmDialog />
  </div>
</template>

<style scoped>
.skeleton-pane {
  padding: 1.5rem;
}
.mb-4 {
  margin-bottom: 1rem;
}
.indexer-tabs {
  padding: 0 0 2rem;
}
.tab-panels {
  margin: 0 1.5rem;
  padding: 1rem;
}
</style>
