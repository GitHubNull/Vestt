import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  _resetForTests,
  getBasePath,
  getServerConfig,
  inferBasePath,
  initBasePath,
  lockBasePath,
  loginForm,
  manualSearch,
  setAdminPassword,
  setUnauthorizedHandler,
  type ApiError,
} from './api'
import type { ServerConfig } from './types'

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
  password: '',
  logging: false,
  basepathoverride: '',
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

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('inferBasePath', () => {
  it('strips the /UI/Dashboard mount suffix', () => {
    expect(inferBasePath('/jackett/UI/Dashboard')).toBe('/jackett')
    expect(inferBasePath('/UI/Dashboard')).toBe('')
    expect(inferBasePath('/UI/Dashboard/')).toBe('')
  })

  it('strips the /UI/Login variant too', () => {
    expect(inferBasePath('/jackett/UI/Login')).toBe('/jackett')
  })

  it('keeps deeper prefixes untouched', () => {
    expect(inferBasePath('/a/b/UI/Dashboard')).toBe('/a/b')
  })
})

describe('request wrapper', () => {
  beforeEach(() => {
    _resetForTests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('composes requests against the inferred base path', async () => {
    initBasePath('/jackett/UI/Dashboard')
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(SERVER_CONFIG))
    vi.stubGlobal('fetch', fetchMock)

    await getServerConfig()
    expect(fetchMock).toHaveBeenCalledWith('/jackett/api/v2.0/server/config', expect.anything())
  })

  it('locks onto basepathoverride once the config is fetched', async () => {
    initBasePath('/UI/Dashboard')
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(SERVER_CONFIG))
    vi.stubGlobal('fetch', fetchMock)

    lockBasePath('/override')
    await getServerConfig()
    expect(fetchMock).toHaveBeenCalledWith('/override/api/v2.0/server/config', expect.anything())
    expect(getBasePath()).toBe('/override')
  })

  it('invokes the unauthorized handler and rejects on 401', async () => {
    initBasePath('/UI/Dashboard')
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('nope', { status: 401 })),
    )
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)

    let err: ApiError | undefined
    try {
      await getServerConfig()
    } catch (e) {
      err = e as ApiError
    }
    expect(onUnauthorized).toHaveBeenCalledOnce()
    expect(err?.status).toBe(401)
  })

  it('normalizes non-2xx failures to {status, message} with the body text', async () => {
    initBasePath('/UI/Dashboard')
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('restricted: port 22', { status: 400 })),
    )
    let err: ApiError | undefined
    try {
      await getServerConfig()
    } catch (e) {
      err = e as ApiError
    }
    expect(err?.status).toBe(400)
    expect(err?.message).toBe('restricted: port 22')
  })

  it('posts the admin password as a JSON string literal', async () => {
    initBasePath('/UI/Dashboard')
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await setAdminPassword('s3cret')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/v2.0/server/adminpassword')
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
    expect(init.body).toBe('"s3cret"')
  })

  it('builds manual search URLs with csv Tracker[]/Category[] params', async () => {
    initBasePath('/UI/Dashboard')
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ results: [], indexers: [] }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await manualSearch('all', {
      query: 'test',
      trackers: ['a', 'b'],
      categories: [2000, 2040],
    })
    const [url] = fetchMock.mock.calls[0] as unknown as [string]
    expect(url).toContain('/api/v2.0/indexers/all/results?')
    expect(url).toContain('query=test')
    expect(url).toContain(`Tracker%5B%5D=${encodeURIComponent('a,b')}`)
    expect(url).toContain(`Category%5B%5D=${encodeURIComponent('2000,2040')}`)
  })
})

describe('loginForm', () => {
  beforeEach(() => {
    _resetForTests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('posts form-encoded and probes the config API for success', async () => {
    initBasePath('/UI/Dashboard')
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({ url: 'http://localhost/UI/Dashboard' } as Response)
      .mockResolvedValueOnce({ status: 200 } as Response)
    vi.stubGlobal('fetch', fetchMock)

    await expect(loginForm('pw')).resolves.toBe(true)
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/UI/Dashboard')
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/x-www-form-urlencoded' })
    expect(init.body).toBe('password=pw')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v2.0/server/config')
  })

  it('reports failure when the config probe answers 401', async () => {
    initBasePath('/UI/Dashboard')
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({ url: 'http://localhost/UI/Dashboard' } as Response)
      .mockResolvedValueOnce({ status: 401 } as Response)
    vi.stubGlobal('fetch', fetchMock)

    await expect(loginForm('wrong')).resolves.toBe(false)
  })
})
