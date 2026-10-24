import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { _resetForTests, initBasePath } from '../services/api'
import type { ServerConfig } from '../services/types'
import { useConfigStore } from './config'

const SERVER_CONFIG: ServerConfig = {
  notices: [],
  port: 9117,
  external: false,
  local_bind_address: '127.0.0.1',
  cors: false,
  api_key: 'k3y',
  blackholedir: '',
  updatedisabled: false,
  prerelease: false,
  password: '********',
  logging: false,
  basepathoverride: '/jackett',
  baseurloverride: '',
  cache_enabled: true,
  cache_ttl: 2100,
  cache_max_results_per_indexer: 1000,
  flaresolverrurl: '',
  flaresolverr_maxtimeout: 60000,
  omdbkey: '',
  omdburl: '',
  app_version: '0.1.0-test',
  can_run_netcore: true,
  proxy_type: '-1',
  proxy_url: '',
  proxy_port: null,
  proxy_username: '',
  proxy_password: '',
}

describe('config store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    _resetForTests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('load() populates state and pins the base path override', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(SERVER_CONFIG), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    initBasePath('/jackett/UI/Dashboard')

    const store = useConfigStore()
    const cfg = await store.load()

    expect(cfg.app_version).toBe('0.1.0-test')
    expect(store.appVersion).toBe('0.1.0-test')
    expect(store.apiKey).toBe('k3y')
    expect(store.loaded).toBe(true)
    expect(fetchMock.mock.calls[0][0]).toBe('/jackett/api/v2.0/server/config')
  })

  it('load() caches: a second call does not refetch', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(SERVER_CONFIG), { status: 200 }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const store = useConfigStore()
    await store.load()
    await store.load()
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('save() posts the patch and merges the response', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ ...SERVER_CONFIG, port: 9118, restart_required: true }),
        { status: 200 },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    const store = useConfigStore()
    const res = await store.save({ port: 9118 })
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/v2.0/server/config')
    expect(JSON.parse(String(init.body))).toEqual({ port: 9118 })
    expect(res.restart_required).toBe(true)
    expect(store.serverConfig?.port).toBe(9118)
  })
})
