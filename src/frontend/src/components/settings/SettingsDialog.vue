<script setup lang="ts">
import Dialog from 'primevue/dialog'
import Tab from 'primevue/tab'
import TabList from 'primevue/tablist'
import TabPanel from 'primevue/tabpanel'
import TabPanels from 'primevue/tabpanels'
import Tabs from 'primevue/tabs'
import { watch } from 'vue'
import ApiKeyPanel from './ApiKeyPanel.vue'
import CacheReleases from './CacheReleases.vue'
import LogViewer from './LogViewer.vue'
import ServerConfigForm from './ServerConfigForm.vue'
import { useSettingsStore } from '../../stores/settings'

// Settings dialog (SPEC-13 §3.3/3.5): Tabs — Server Config / Cached
// Releases / Logs / API Key. Opening initializes the draft from the
// loaded server config (upstream loadJackettSettings field loading).

const settings = useSettingsStore()

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ 'update:visible': [value: boolean] }>()

watch(
  () => props.visible,
  (v) => {
    if (v) settings.open()
    else emit('update:visible', false)
  },
)
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    header="Settings"
    :style="{ width: '70vw', maxWidth: '70rem' }"
    :draggable="false"
    @update:visible="(v: boolean) => emit('update:visible', v)"
  >
    <Tabs value="server">
      <TabList>
        <Tab value="server">Server Config</Tab>
        <Tab value="cache">Cached Releases</Tab>
        <Tab value="logs">Logs</Tab>
        <Tab value="apikey">API Key</Tab>
      </TabList>
      <TabPanels>
        <TabPanel value="server">
          <ServerConfigForm />
        </TabPanel>
        <TabPanel value="cache">
          <CacheReleases />
        </TabPanel>
        <TabPanel value="logs">
          <LogViewer />
        </TabPanel>
        <TabPanel value="apikey">
          <ApiKeyPanel />
        </TabPanel>
      </TabPanels>
    </Tabs>
  </Dialog>
</template>
