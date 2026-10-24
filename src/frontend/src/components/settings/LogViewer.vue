<script setup lang="ts">
import Button from 'primevue/button'
import Select from 'primevue/select'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { LOG_PAGE_SIZE, useLogsStore } from '../../stores/logs'
import type { CachedLog } from '../../services/types'

// Log viewer (SPEC-13 §3.4): level badges, monospaced messages, manual
// refresh + 10 s polling toggle, "load earlier" client-side pagination
// (the backend ring buffer returns the full buffer newest-first).

const logs = useLogsStore()

const POLL_INTERVAL_MS = 10000
const polling = ref(false)
const options = [
  { label: 'Manual refresh', value: false },
  { label: 'Refresh every 10 seconds', value: true },
]
const pollingMode = ref({ label: 'Manual refresh', value: false })

let timer: ReturnType<typeof setInterval> | null = null

function setTimer(on: boolean): void {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  if (on) {
    timer = setInterval(() => void logs.poll(), POLL_INTERVAL_MS)
  }
}

function onPollToggle(): void {
  polling.value = pollingMode.value.value
  setTimer(polling.value)
}

function refresh(): void {
  void logs.poll()
}

function levelSeverity(level: string): 'info' | 'secondary' | 'warn' | 'danger' {
  switch (level.toUpperCase()) {
    case 'DEBUG':
      return 'secondary'
    case 'WARN':
    case 'WARNING':
      return 'warn'
    case 'ERROR':
    case 'FATAL':
      return 'danger'
    default:
      return 'info'
  }
}

function formatWhen(entry: CachedLog): string {
  const d = new Date(entry.when)
  return isNaN(d.getTime()) ? entry.when : d.toLocaleString()
}

onMounted(() => {
  void logs.load()
})

onBeforeUnmount(() => {
  setTimer(false)
})

defineExpose({ refresh })
</script>

<template>
  <div class="log-viewer">
    <div class="log-toolbar">
      <Select
        v-model="pollingMode"
        :options="options"
        option-label="label"
        class="poll-select"
        @change="onPollToggle"
      />
      <Button
        icon="pi pi-refresh"
        label="Refresh"
        size="small"
        outlined
        :loading="logs.loading"
        @click="refresh"
      />
    </div>

    <div v-if="!logs.loading && logs.entries.length === 0" class="log-empty">
      No log entries.
    </div>

    <table v-else class="log-table">
      <tbody>
        <tr v-for="(entry, i) in logs.visibleEntries" :key="`${entry.when}-${i}`">
          <td class="log-when">{{ formatWhen(entry) }}</td>
          <td class="log-level">
            <span class="level-badge" :class="`level-${levelSeverity(entry.level)}`">{{ entry.level.toUpperCase() }}</span>
          </td>
          <td class="log-message mono">{{ entry.message }}</td>
        </tr>
      </tbody>
    </table>

    <Button
      v-if="logs.hasMore"
      label="Load earlier entries"
      icon="pi pi-angle-up"
      size="small"
      text
      class="load-earlier"
      @click="logs.showMore()"
    />
    <p v-else-if="logs.entries.length > LOG_PAGE_SIZE" class="log-end">
      All {{ logs.entries.length }} entries shown.
    </p>
  </div>
</template>

<style scoped>
.log-toolbar {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}
.poll-select {
  min-width: 14rem;
}
.log-empty {
  padding: 2rem;
  text-align: center;
  color: var(--p-text-muted-color);
}
.log-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.85rem;
}
.log-table td {
  padding: 0.2rem 0.5rem;
  border-bottom: 1px solid var(--p-surface-200);
  vertical-align: top;
}
.log-when {
  white-space: nowrap;
  color: var(--p-text-muted-color);
}
.log-level {
  white-space: nowrap;
}
.level-badge {
  display: inline-block;
  border-radius: 0.2em;
  padding: 0 0.4em;
  font-size: 75%;
  font-weight: 700;
  color: #fff;
  background: #777;
}
.level-info {
  background: #5bc0de;
}
.level-secondary {
  background: #777;
}
.level-warn {
  background: #f0ad4e;
}
.level-danger {
  background: #d9534f;
}
.mono {
  font-family: monospace;
  word-break: break-all;
}
.load-earlier {
  margin-top: 0.5rem;
}
.log-end {
  margin-top: 0.5rem;
  color: var(--p-text-muted-color);
  font-size: 0.85rem;
}
</style>
