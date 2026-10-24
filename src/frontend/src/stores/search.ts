import { defineStore } from 'pinia'
import type { ManualSearchIndexerState, TrackerCacheResult } from '../services/types'

// SPEC-11 delivers the state skeleton; run()/presets land in SPEC-13
// (frontend.md §3).

export const useSearchStore = defineStore('search', {
  state: () => ({
    visible: false,
    query: '',
    categories: [] as number[],
    trackers: [] as string[],
    results: [] as TrackerCacheResult[],
    indexerStates: [] as ManualSearchIndexerState[],
    running: false,
    ran: false,
  }),
  actions: {
    open(preset?: { query?: string; trackers?: string[]; categories?: number[] }): void {
      if (preset?.query !== undefined) this.query = preset.query
      if (preset?.trackers) this.setTrackers(preset.trackers)
      if (preset?.categories) this.categories = [...preset.categories]
      this.visible = true
    },
    close(): void {
      this.visible = false
    },
    setTrackers(trackers: string[]): void {
      this.trackers = [...trackers]
    },
    reset(): void {
      this.query = ''
      this.categories = []
      this.trackers = []
      this.results = []
      this.indexerStates = []
      this.ran = false
    },
  },
})
