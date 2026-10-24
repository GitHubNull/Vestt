import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { _resetForTests } from '../services/api'
import type { ConfigFieldDef, IndexerDTO } from '../services/types'
import {
  badgeOf,
  buildConfigItems,
  initialValues,
  parseFilters,
  serializeFilters,
  useIndexersStore,
} from './indexers'

function indexer(partial: Partial<IndexerDTO> & { id: string }): IndexerDTO {
  return {
    name: partial.id,
    description: '',
    type: 'public',
    configured: false,
    site_link: `https://${partial.id}.example.com/`,
    alternativesitelinks: [],
    language: 'en-US',
    tags: [],
    last_error: null,
    potatoenabled: false,
    caps: [],
    ...partial,
  }
}

const FIXTURE_INDEXERS: IndexerDTO[] = [
  indexer({ id: 'alpha', type: 'public', configured: true, tags: ['Movies'] }),
  indexer({
    id: 'bravo',
    type: 'private',
    configured: true,
    tags: ['movies', 'hd'],
    last_error: 'login failed',
  }),
  indexer({ id: 'charlie', type: 'semi-private', configured: true, tags: ['HD'] }),
  indexer({ id: 'delta', type: 'public', configured: true }),
  indexer({ id: 'echo', type: 'public' }), // unconfigured
]

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('indexers store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    _resetForTests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  async function loadFixture(): Promise<ReturnType<typeof useIndexersStore>> {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse(FIXTURE_INDEXERS)),
    )
    const store = useIndexersStore()
    await store.load()
    return store
  }

  describe('filters (AND semantics, SPEC-12 §3.2)', () => {
    it('type filter selects only matching indexers', async () => {
      const store = await loadFixture()
      store.setFilters({ type: 'private' })
      expect(store.filteredConfigured.map((i) => i.id)).toEqual(['bravo'])
    })

    it('tag filter matches case-insensitively and merges tag casing', async () => {
      const store = await loadFixture()
      expect(store.tags).toEqual(['hd', 'Movies'])
      store.setFilters({ tag: 'movies' })
      expect(store.filteredConfigured.map((i) => i.id).sort()).toEqual(['alpha', 'bravo'])
    })

    it('state filter: failing matches last_error', async () => {
      const store = await loadFixture()
      store.setFilters({ state: 'failing' })
      expect(store.filteredConfigured.map((i) => i.id)).toEqual(['bravo'])
    })

    it('state filter: untested excludes tested-ok and failing', async () => {
      const store = await loadFixture()
      store.testStates.alpha = 'ok'
      store.setFilters({ state: 'untested' })
      expect(store.filteredConfigured.map((i) => i.id).sort()).toEqual(['charlie', 'delta'])
    })

    it('combined type+tag+state filters apply with AND', async () => {
      const store = await loadFixture()
      store.setFilters({ type: 'public', tag: 'movies', state: 'failing' })
      expect(store.filteredConfigured.map((i) => i.id)).toEqual([])
      store.setFilters({ type: 'private', tag: 'movies', state: 'failing' })
      expect(store.filteredConfigured.map((i) => i.id)).toEqual(['bravo'])
    })

    it('unconfigured indexers never appear in the configured list', async () => {
      const store = await loadFixture()
      store.setFilters({ type: 'all', tag: 'all', state: 'all' })
      expect(store.filteredConfigured.some((i) => i.id === 'echo')).toBe(false)
      expect(store.unconfigured.map((i) => i.id)).toEqual(['echo'])
    })
  })

  describe('filter deep link (de)serialization', () => {
    it('all-defaults serialize to null (no hash pollution)', () => {
      expect(serializeFilters({ type: 'all', tag: 'all', state: 'all' })).toBeNull()
    })

    it('round-trips a JSON filter payload', () => {
      const f: import('./indexers').IndexerFilters = {
        type: 'private',
        tag: 'Movies',
        state: 'failing',
      }
      expect(parseFilters(serializeFilters(f))).toEqual(f)
    })

    it('rejects malformed JSON and unknown values', () => {
      expect(parseFilters('not json')).toBeNull()
      const parsed = parseFilters('{"type":"nonsense","tag":"x","state":"all"}')
      expect(parsed).toEqual({
        type: 'all',
        tag: 'x',
        state: 'all',
      })
      expect(parseFilters(undefined)).toBeNull()
    })

    it('applyFilterDeepLink installs parsed filters', async () => {
      const store = await loadFixture()
      store.applyFilterDeepLink('{"type":"public"}')
      expect(store.filters.type).toBe('public')
      expect(store.filters.tag).toBe('all')
    })
  })

  describe('config payload assembly (frontend.md §5)', () => {
    const fields: ConfigFieldDef[] = [
      { id: 'sitelink', type: 'inputstring', name: 'Site Link', value: 'https://a.example/' },
      { id: 'internal', type: 'hiddendata', name: 'Internal', value: 'keep-me' },
      { id: 'username', type: 'inputstring', name: 'Username', value: '' },
      { id: 'password', type: 'password', name: 'Password', value: '|||%%PREVJACKPASSWD%%|||' },
      { id: 'freeleech', type: 'inputbool', name: 'Freeleech', value: false },
      { id: 'categories', type: 'inputcheckbox', name: 'Categories', values: ['2000'], options: { '2000': 'Movies' } },
      { id: 'authentication', type: 'inputselect', name: 'Auth', value: 'form', options: { form: 'Form', cookie: 'Cookie' } },
      { id: 'notice', type: 'displayinfo', name: 'Info', value: 'read me' },
      { id: 'CaptchaImage', type: 'displayimage', name: 'Captcha', value: 'data:image/jpeg;base64,xx' },
      { id: 'cookie', type: 'inputstring', name: 'Cookie', value: '' },
    ]

    it('maps the 8 field types onto the right ConfigItem shapes', () => {
      const values = initialValues(fields)
      values.username = 'user1'
      values.password = 'secret'
      values.freeleech = true
      values.categories = ['2000', '3000']
      values.authentication = 'cookie'
      values.cookie = 'uid=1; pass=x'

      const items = buildConfigItems(fields, values)
      expect(items).toEqual([
        { id: 'sitelink', value: 'https://a.example/' },
        { id: 'internal', value: 'keep-me' },
        { id: 'username', value: 'user1' },
        { id: 'password', value: 'secret' },
        { id: 'freeleech', value: 'true' },
        { id: 'categories', values: ['2000', '3000'] },
        { id: 'authentication', value: 'cookie' },
        // displayinfo/displayimage submit nothing
        { id: 'cookie', cookie: 'uid=1; pass=x' },
      ])
    })

    it('skips password fields still holding the mask sentinel', () => {
      const items = buildConfigItems(fields, initialValues(fields))
      expect(items.find((i) => i.id === 'password')).toBeUndefined()
    })

    it('treats the admin-style ******** mask the same way', () => {
      const values = initialValues(fields)
      values.password = '********'
      const items = buildConfigItems(fields, values)
      expect(items.find((i) => i.id === 'password')).toBeUndefined()
    })
  })

  describe('badge derivation', () => {
    it('last_error wins; ok run means healthy; otherwise untested', () => {
      const ok = indexer({ id: 'x', configured: true })
      expect(badgeOf(ok, 'ok')).toBe('healthy')
      expect(badgeOf(ok, undefined)).toBe('untested')
      const failing = indexer({ id: 'y', configured: true, last_error: 'boom' })
      expect(badgeOf(failing, 'ok')).toBe('failing')
      expect(badgeOf(failing, 'fail')).toBe('failing')
    })
  })

  describe('actions', () => {
    it('test() runs the state machine and clears last_error on success', async () => {
      const store = await loadFixture()
      const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
      vi.stubGlobal('fetch', fetchMock)

      await store.test('bravo')
      expect(store.testStates.bravo).toBe('ok')
      expect(store.all.find((i) => i.id === 'bravo')?.last_error).toBeNull()
      const [url, init] = fetchMock.mock.calls.at(-1) as unknown as [string, RequestInit]
      expect(url).toBe('/api/v2.0/indexers/bravo/Test')
      expect(init.method).toBe('POST')
    })

    it('test() failure records the error message', async () => {
      const store = await loadFixture()
      vi.stubGlobal(
        'fetch',
        vi.fn().mockResolvedValue(new Response('login failed', { status: 500 })),
      )
      await store.test('alpha')
      expect(store.testStates.alpha).toBe('fail')
      expect(store.all.find((i) => i.id === 'alpha')?.last_error).toBe('login failed')
    })

    it('remove() deletes and reloads the list', async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(jsonResponse(FIXTURE_INDEXERS)) // initial load
        .mockResolvedValueOnce(new Response(null, { status: 204 })) // DELETE
        .mockResolvedValueOnce(jsonResponse(FIXTURE_INDEXERS.filter((i) => i.id !== 'alpha'))) // reload
      vi.stubGlobal('fetch', fetchMock)
      const store = useIndexersStore()
      await store.load()
      await store.remove('alpha')
      const calls = fetchMock.mock.calls.map((c) => c[0] as string)
      expect(calls[1]).toBe('/api/v2.0/indexers/alpha')
      expect((fetchMock.mock.calls[1] as unknown as [string, RequestInit])[1].method).toBe('DELETE')
      expect(store.all.some((i) => i.id === 'alpha')).toBe(false)
    })

    it('addIndexers() posts the fetched default config per id', async () => {
      const defaultConfig = [
        { id: 'sitelink', type: 'inputstring', name: 'Site Link', value: 'https://echo.example/' },
      ]
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(jsonResponse(FIXTURE_INDEXERS))
        .mockResolvedValueOnce(jsonResponse(defaultConfig)) // GET Config echo
        .mockResolvedValueOnce(new Response(null, { status: 204 })) // POST Config echo
        .mockResolvedValueOnce(jsonResponse(FIXTURE_INDEXERS)) // reload
      vi.stubGlobal('fetch', fetchMock)
      const store = useIndexersStore()
      await store.load()
      await store.addIndexers(['echo'])

      const calls = fetchMock.mock.calls.map((c) => c[0] as string)
      expect(calls[1]).toBe('/api/v2.0/indexers/echo/Config')
      expect(calls[2]).toBe('/api/v2.0/indexers/echo/Config')
      const postInit = fetchMock.mock.calls[2] as unknown as [string, RequestInit]
      expect(postInit[1].method).toBe('POST')
      expect(JSON.parse(String(postInit[1].body))).toEqual([
        { id: 'sitelink', value: 'https://echo.example/' },
      ])
    })
  })
})
