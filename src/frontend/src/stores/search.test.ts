// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { _resetForTests, initBasePath } from '../services/api'
import type { ManualSearchResult } from '../services/types'
import { useSearchStore } from './search'

// SPEC-13 §5.3: search state machine — idle/loading/done/error, aggregate
// request URL (query/trackers/categories), preset save/apply/delete with
// overwrite-by-name persistence. The store is visibility-agnostic (results
// render inline on the home page since the UI rework).

function searchResponse(): ManualSearchResult {
  return {
    results: [
      {
        Title: 'fake 2026 release 720p',
        Tracker: '0Magnet',
        TrackerId: '0magnet',
        Size: 524288000,
        Seeders: 7,
        Peers: 2,
        PublishDate: '2026-10-24T10:00:00Z',
        DownloadVolumeFactor: 0,
        UploadVolumeFactor: 1,
      },
    ],
    indexers: [{ ID: '0magnet', Name: '0Magnet', Status: 2, Results: 1, ElapsedTime: 12, Error: null }],
  }
}

function stubFetch(body: unknown, status = 200) {
  const fetchMock = vi.fn().mockImplementation(() =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('search store (SPEC-13 §3.1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    _resetForTests()
    initBasePath('/UI/Dashboard')
    window.localStorage.clear()
  })

  it('starts idle with an empty form', () => {
    const store = useSearchStore()
    expect(store.status).toBe('idle')
    expect(store.query).toBe('')
    expect(store.results).toEqual([])
  })

  it('applyParams() applies the deep-link fields', () => {
    const store = useSearchStore()
    store.applyParams({ query: 'matrix', trackers: ['0magnet'], categories: [2000] })
    expect(store.query).toBe('matrix')
    expect(store.trackers).toEqual(['0magnet'])
    expect(store.categories).toEqual([2000])
    expect(store.status).toBe('idle')
  })

  it('run() hits the aggregate endpoint and lands in done', async () => {
    const fetchMock = stubFetch(searchResponse())
    const store = useSearchStore()
    store.query = 'fake'
    await store.run()

    expect(store.status).toBe('done')
    expect(store.results).toHaveLength(1)
    expect(store.indexerStates[0].Status).toBe(2)
    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain('/api/v2.0/indexers/all/results?')
    expect(url).toContain('query=fake')
  })

  it('run() encodes tracker subset and category filters in the URL', async () => {
    const fetchMock = stubFetch({ results: [], indexers: [] })
    const store = useSearchStore()
    store.trackers = ['0magnet', 'acgrip']
    store.categories = [2000]
    await store.run()

    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain('Tracker%5B%5D=0magnet%2Cacgrip')
    expect(url).toContain('Category%5B%5D=2000')
  })

  it('run() failure lands in error and clears results', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response('boom', { status: 500 })))
    vi.stubGlobal('fetch', fetchMock)
    const store = useSearchStore()
    await store.run()

    expect(store.status).toBe('error')
    expect(store.error).toBe('boom')
    expect(store.results).toEqual([])
  })

  it('presets persist across a fresh store (save → reload → apply)', async () => {
    const fetchMock = stubFetch({ results: [], indexers: [] })
    const store = useSearchStore()
    store.query = 'matrix'
    store.trackers = ['0magnet']
    store.categories = [2000]
    store.savePreset('movies')
    expect(store.presets).toEqual([
      { name: 'movies', query: 'matrix', trackers: ['0magnet'], cats: [2000] },
    ])

    // simulate the acceptance flow: refresh the page (fresh store) then apply
    const fresh = useSearchStore()
    fresh.loadPresets()
    fresh.reset()
    fresh.applyPreset('movies')
    expect(fresh.query).toBe('matrix')
    expect(fresh.trackers).toEqual(['0magnet'])
    expect(fresh.categories).toEqual([2000])

    // applying re-issues the same request (SPEC §5.1 同请求重发)
    await fresh.run()
    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain('query=matrix')
    expect(url).toContain('Tracker%5B%5D=0magnet')
    expect(url).toContain('Category%5B%5D=2000')
  })

  it('same-name preset overwrite keeps a single entry', () => {
    const store = useSearchStore()
    store.query = 'one'
    store.savePreset('dup')
    store.query = 'two'
    store.savePreset('dup')
    expect(store.presets).toHaveLength(1)
    expect(store.presets[0].query).toBe('two')
  })

  it('deletePreset removes the entry from localStorage', () => {
    const store = useSearchStore()
    store.savePreset('gone')
    store.savePreset('kept')
    store.deletePreset('gone')
    expect(store.presets.map((p) => p.name)).toEqual(['kept'])
    expect(window.localStorage.getItem('vestt.presets')).toContain('kept')
    expect(window.localStorage.getItem('vestt.presets')).not.toContain('gone')
  })
})
