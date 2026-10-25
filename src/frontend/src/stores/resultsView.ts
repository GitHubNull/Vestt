import { defineStore } from 'pinia'

// Search results view mode (UI 改版): DataView-style card list by default,
// switchable back to the DataTable. Persisted in localStorage following the
// vestt.* preference convention (theme.ts / searchPresets.ts); invalid values
// fall back to the 'list' default. Home search only — the cached releases
// view never renders the toggle and always uses the table.
export const RESULTS_VIEW_KEY = 'vestt.results.view'
export type ResultsView = 'list' | 'table'

function loadView(): ResultsView {
  if (typeof window === 'undefined') return 'list'
  try {
    return window.localStorage.getItem(RESULTS_VIEW_KEY) === 'table' ? 'table' : 'list'
  } catch {
    return 'list'
  }
}

export const useResultsViewStore = defineStore('resultsView', {
  state: () => ({
    view: loadView() as ResultsView,
  }),
  actions: {
    set(view: ResultsView): void {
      this.view = view
      try {
        window.localStorage.setItem(RESULTS_VIEW_KEY, view)
      } catch {
        // storage unavailable (private mode / SSR): keep in-memory only
      }
    },
  },
})
