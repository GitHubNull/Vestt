// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { RESULTS_VIEW_KEY, useResultsViewStore } from './resultsView'

// UI 改版: results view mode persists in localStorage (vestt.results.view),
// defaults to the DataView-style 'list', invalid values fall back to 'list'.

describe('resultsView store', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('defaults to list when no stored value exists', () => {
    expect(useResultsViewStore().view).toBe('list')
  })

  it('round-trips the table choice through localStorage', () => {
    const store = useResultsViewStore()
    store.set('table')
    expect(store.view).toBe('table')
    expect(localStorage.getItem(RESULTS_VIEW_KEY)).toBe('table')

    setActivePinia(createPinia())
    expect(useResultsViewStore().view).toBe('table')
  })

  it('falls back to list for invalid stored values', () => {
    localStorage.setItem(RESULTS_VIEW_KEY, 'grid')
    setActivePinia(createPinia())
    expect(useResultsViewStore().view).toBe('list')
  })
})
