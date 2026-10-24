import { defineStore } from 'pinia'
import { getLogs } from '../services/api'
import type { CachedLog } from '../services/types'

export const useLogsStore = defineStore('logs', {
  state: () => ({
    entries: [] as CachedLog[],
    loading: false,
    loaded: false,
  }),
  actions: {
    async load(force = false): Promise<CachedLog[]> {
      if (this.loaded && !force) return this.entries
      this.loading = true
      try {
        this.entries = await getLogs()
        this.loaded = true
        return this.entries
      } finally {
        this.loading = false
      }
    },
  },
})
