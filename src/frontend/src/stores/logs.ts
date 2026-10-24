import { defineStore } from 'pinia'
import { getLogs } from '../services/api'
import type { CachedLog } from '../services/types'

// SPEC-13 §3.4: log viewer store. The backend ring buffer returns the whole
// buffer newest-first, so pagination is client-side slicing ("load earlier").

export const LOG_PAGE_SIZE = 200

export const useLogsStore = defineStore('logs', {
  state: () => ({
    entries: [] as CachedLog[],
    loading: false,
    loaded: false,
    /** client-side pagination cursor: entries[0..shownCount) are visible. */
    shownCount: LOG_PAGE_SIZE,
  }),
  getters: {
    visibleEntries(s): CachedLog[] {
      return s.entries.slice(0, s.shownCount)
    },
    hasMore(s): boolean {
      return s.shownCount < s.entries.length
    },
  },
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
    /** 10 s polling refresh (SPEC §3.4); keeps the pagination cursor. */
    async poll(): Promise<CachedLog[]> {
      return this.load(true)
    },
    showMore(): void {
      this.shownCount += LOG_PAGE_SIZE
    },
    resetPagination(): void {
      this.shownCount = LOG_PAGE_SIZE
    },
  },
})
