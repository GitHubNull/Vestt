<script setup lang="ts">
import Skeleton from 'primevue/skeleton'
import Tab from 'primevue/tab'
import TabList from 'primevue/tablist'
import TabPanel from 'primevue/tabpanel'
import TabPanels from 'primevue/tabpanels'
import Tabs from 'primevue/tabs'
import { onMounted, ref } from 'vue'
import AppHeader from '../components/common/AppHeader.vue'
import IndexerFilters from '../components/indexer/IndexerFilters.vue'
import IndexerTable from '../components/indexer/IndexerTable.vue'
import { notify } from '../components/common/notify'
import { useConfigStore } from '../stores/config'
import { useIndexersStore } from '../stores/indexers'

// Dashboard shell (SPEC-11 §3.5): first paint loads server config and the
// indexer list concurrently (upstream loadJackettSettings → reloadIndexers).
const config = useConfigStore()
const indexers = useIndexersStore()
const booting = ref(true)

onMounted(async () => {
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
})
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
            <IndexerTable :indexers="indexers.configured" mode="configured" :loading="indexers.loading" />
          </TabPanel>
          <TabPanel value="unconfigured">
            <IndexerTable :indexers="indexers.unconfigured" mode="unconfigured" :loading="indexers.loading" />
          </TabPanel>
        </TabPanels>
      </Tabs>
    </template>
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
