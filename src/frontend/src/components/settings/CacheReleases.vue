<script setup lang="ts">
import { onMounted, ref } from 'vue'
import SearchResultTable from '../search/SearchResultTable.vue'
import { getCache } from '../../services/api'
import { errMessage } from '../../stores/indexers'
import { useConfigStore } from '../../stores/config'
import type { TrackerCacheResult } from '../../services/types'

// Cached releases view (SPEC-13 §3.2): same result table as the search
// dialog plus the FirstSeen column. Upstream disables the "View cached
// releases" button when cache_enabled is false (custom.js:129-134).

const config = useConfigStore()
const releases = ref<TrackerCacheResult[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

onMounted(async () => {
  if (!config.serverConfig?.cache_enabled) return
  loading.value = true
  try {
    const cache = await getCache()
    releases.value = cache
  } catch (err) {
    error.value = errMessage(err)
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="cache-releases">
    <p v-if="!config.serverConfig?.cache_enabled" class="cache-disabled">
      The result cache is disabled. Enable it under Server Config → Cache to use cached releases.
    </p>
    <template v-else>
      <p v-if="error" class="cache-error">{{ error }}</p>
      <SearchResultTable v-else :results="releases" :loading="loading" show-first-seen />
    </template>
  </div>
</template>

<style scoped>
.cache-disabled {
  color: var(--p-text-muted-color);
}
.cache-error {
  color: var(--p-red-500);
}
</style>
