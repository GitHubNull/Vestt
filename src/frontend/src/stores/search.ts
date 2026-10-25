import { defineStore } from 'pinia'
import { manualSearch } from '../services/api'
import { errMessage } from './indexers'
import {
  deletePreset,
  loadPresets,
  savePreset,
  type SearchPreset,
} from '../services/searchPresets'
import type { ManualSearchIndexerState, TrackerCacheResult } from '../services/types'

// SPEC-13 §3.1: manual search state machine (idle/loading/done/error) +
// structured presets (SPEC §3.1.5, localStorage `vestt.presets`).
// The store is visibility-agnostic: HomeView owns the form and renders the
// results inline (UI 改版: the search dialog was retired).

export type SearchRunStatus = 'idle' | 'loading' | 'done' | 'error'

export const useSearchStore = defineStore('search', {
  state: () => ({
    query: '',
    categories: [] as number[],
    trackers: [] as string[],
    results: [] as TrackerCacheResult[],
    indexerStates: [] as ManualSearchIndexerState[],
    running: false,
    ran: false,
    error: null as string | null,
    presets: [] as SearchPreset[],
  }),
  getters: {
    /** idle/loading/done/error (SPEC §5.3 状态机). */
    status(s): SearchRunStatus {
      if (s.running) return 'loading'
      if (s.error) return 'error'
      if (s.ran) return 'done'
      return 'idle'
    },
  },
  actions: {
    /** Applies deep-link / handoff params (query/trackers/categories)
     *  without touching any UI state — the home page renders results. */
    applyParams(preset?: { query?: string; trackers?: string[]; categories?: number[] }): void {
      if (preset?.query !== undefined) this.query = preset.query
      if (preset?.trackers) this.setTrackers(preset.trackers)
      if (preset?.categories) this.categories = [...preset.categories]
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
      this.error = null
    },

    /** Executes the aggregate search. Empty tracker list = every configured
     *  indexer (upstream "all"); the backend meta indexer applies the
     *  Tracker[] subset and reports per-indexer states. */
    async run(): Promise<void> {
      if (this.running) return
      this.running = true
      this.error = null
      try {
        const res = await manualSearch('all', {
          query: this.query,
          trackers: this.trackers.length ? this.trackers : undefined,
          categories: this.categories.length ? this.categories : undefined,
        })
        this.results = res.results
        this.indexerStates = res.indexers
        this.ran = true
      } catch (err) {
        this.results = []
        this.indexerStates = []
        this.error = errMessage(err)
        this.ran = true
      } finally {
        this.running = false
      }
    },

    // --- presets (SPEC §3.1.5) ----------------------------------------------

    loadPresets(): SearchPreset[] {
      this.presets = loadPresets()
      return this.presets
    },

    savePreset(name: string): SearchPreset[] {
      const preset: SearchPreset = {
        name,
        query: this.query,
        trackers: [...this.trackers],
        cats: [...this.categories],
      }
      this.presets = savePreset(this.presets, preset)
      return this.presets
    },

    applyPreset(name: string): void {
      const preset = this.presets.find((p) => p.name === name)
      if (!preset) return
      this.query = preset.query
      this.trackers = [...preset.trackers]
      this.categories = [...preset.cats]
    },

    deletePreset(name: string): SearchPreset[] {
      this.presets = deletePreset(this.presets, name)
      return this.presets
    },
  },
})
