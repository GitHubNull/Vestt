import { defineStore } from 'pinia'
import { getIndexers } from '../services/api'
import type { IndexerDTO } from '../services/types'

// SPEC-11 delivers the state + load() skeleton; filter wiring lands in SPEC-12
// (frontend.md §3).

export type IndexerTypeFilter = 'public' | 'semi-private' | 'private' | 'all'

export interface IndexerFilters {
  /** 'all' = no filtering (PrimeVue Select treats '' as empty/placeholder). */
  type: IndexerTypeFilter
  tag: string
  /** healthy | failing | untested | 'all' */
  state: string
}

export const useIndexersStore = defineStore('indexers', {
  state: () => ({
    all: [] as IndexerDTO[],
    loading: false,
    loaded: false,
    filters: { type: 'all', tag: 'all', state: 'all' } as IndexerFilters,
  }),
  getters: {
    configured: (s): IndexerDTO[] => s.all.filter((i) => i.configured),
    unconfigured: (s): IndexerDTO[] => s.all.filter((i) => !i.configured),
    /** distinct tags across all indexers (upstream tag filter source). */
    tags: (s): string[] => [...new Set(s.all.flatMap((i) => i.tags))].sort(),
  },
  actions: {
    async load(force = false): Promise<IndexerDTO[]> {
      if (this.loaded && !force) return this.all
      this.loading = true
      try {
        this.all = await getIndexers()
        this.loaded = true
        return this.all
      } finally {
        this.loading = false
      }
    },
  },
})
