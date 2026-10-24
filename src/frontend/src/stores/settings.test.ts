import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { _resetForTests, initBasePath } from '../services/api'
import type { ServerConfig } from '../services/types'
import { useConfigStore } from './config'
import {
  diffConfig,
  draftFromConfig,
  isProxyWarningVisible,
  sectionForError,
  sectionsOfDiff,
  useSettingsStore,
} from './settings'

// SPEC-13 §5.3: settings form merge logic — draft copy, PATCH diff with type
// normalization, section ownership of diffs and 400 error mapping,
// restart_required banner, proxyWarning visibility.

const BASE: ServerConfig = {
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

describe('settings merge logic (SPEC-13 §3.3)', () => {
  it('draftFromConfig returns an independent deep copy', () => {
    const draft = draftFromConfig(BASE)
    draft.port = 9118
    draft.cache_enabled = false
    expect(BASE.port).toBe(9117)
    expect(BASE.cache_enabled).toBe(true)
  })

  it('diffConfig is empty for an untouched draft', () => {
    expect(diffConfig(BASE, draftFromConfig(BASE))).toEqual({})
  })

  it('diffConfig reports only the changed fields', () => {
    const draft = draftFromConfig(BASE)
    draft.port = 9118
    draft.basepathoverride = '/vestt'
    draft.omdbkey = 'abc'
    expect(diffConfig(BASE, draft)).toEqual({
      port: 9118,
      basepathoverride: '/vestt',
      omdbkey: 'abc',
    })
  })

  it('diffConfig normalizes form strings to numbers and booleans', () => {
    const draft = draftFromConfig(BASE)
    // form inputs hand back strings/'on'-style values
    ;(draft as unknown as Record<string, unknown>).port = '9119'
    ;(draft as unknown as Record<string, unknown>).flaresolverr_maxtimeout = '55000'
    ;(draft as unknown as Record<string, unknown>).external = 'true'
    const diff = diffConfig(BASE, draft)
    expect(diff.port).toBe(9119)
    expect(diff.flaresolverr_maxtimeout).toBe(55000)
    expect(diff.external).toBe(true)
  })

  it('diffConfig keeps proxy_port nullable (empty → null when it had a value)', () => {
    const base = { ...BASE, proxy_port: 8080 }
    const draft = draftFromConfig(base)
    draft.proxy_type = '0'
    draft.proxy_url = 'http://127.0.0.1'
    ;(draft as unknown as Record<string, unknown>).proxy_port = ''
    const diff = diffConfig(base, draft)
    expect(diff).toEqual({ proxy_type: '0', proxy_url: 'http://127.0.0.1', proxy_port: null })

    // clearing an already-empty port is not a change
    const emptyDraft = draftFromConfig(BASE)
    ;(emptyDraft as unknown as Record<string, unknown>).proxy_port = ''
    expect(diffConfig(BASE, emptyDraft)).toEqual({})
  })

  it('diffConfig never ships the api key or derived fields', () => {
    const draft = draftFromConfig(BASE)
    ;(draft as unknown as Record<string, unknown>).api_key = 'tampered'
    ;(draft as unknown as Record<string, unknown>).app_version = '9.9.9'
    ;(draft as unknown as Record<string, unknown>).password = 's3cret'
    expect(diffConfig(BASE, draft)).toEqual({})
  })

  it('sectionsOfDiff maps fields to their owning section', () => {
    expect(sectionsOfDiff({ port: 9118 })).toEqual(['server'])
    expect(sectionsOfDiff({ cache_ttl: 100 })).toEqual(['cache'])
    expect(sectionsOfDiff({ proxy_type: '2', omdbkey: 'x' })).toEqual(['proxy', 'omdb'])
  })

  it('sectionForError matches the server text to the owning section', () => {
    expect(sectionForError('Invalid port: 70000', ['server'])).toEqual(['server'])
    expect(sectionForError('flaresolverr max timeout must be >= 5000ms', ['flaresolverr'])).toEqual([
      'flaresolverr',
    ])
    expect(sectionForError('something opaque', ['server', 'cache'])).toEqual(['server', 'cache'])
  })

  it('proxyWarning shows only when a proxy type is set without a URL (SPEC §3.3)', () => {
    expect(isProxyWarningVisible({ proxy_type: '-1', proxy_url: '' })).toBe(false)
    expect(isProxyWarningVisible({ proxy_type: '2', proxy_url: '' })).toBe(true)
    expect(isProxyWarningVisible({ proxy_type: '2', proxy_url: 'http://proxy' })).toBe(false)
  })
})

describe('settings store save (SPEC-13 §3.3.1/3.3.2)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    _resetForTests()
    initBasePath('/UI/Dashboard')
  })

  function primeConfig() {
    const config = useConfigStore()
    config.serverConfig = { ...BASE }
    config.loaded = true
    return config
  }

  it('save() posts only the diff and raises the restart banner', async () => {
    const config = primeConfig()
    const saveMock = vi.spyOn(config, 'save').mockResolvedValue({ ...BASE, port: 9118, restart_required: true })
    const settings = useSettingsStore()
    settings.open()
    settings.draft!.port = 9118
    const ok = await settings.save()
    expect(ok).toBe(true)
    expect(saveMock).toHaveBeenCalledWith({ port: 9118 })
    expect(settings.restartRequired).toBe(true)
  })

  it('save() maps a 400 to the owning section Alert', async () => {
    primeConfig()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('Invalid port: 70000', { status: 400 })),
    )
    const settings = useSettingsStore()
    settings.open()
    settings.draft!.port = 70000
    const ok = await settings.save()
    expect(ok).toBe(false)
    expect(settings.sectionErrors.server).toBe('Invalid port: 70000')
  })

  it('save() with no changes is a no-op success', async () => {
    const config = primeConfig()
    const saveMock = vi.spyOn(config, 'save')
    const settings = useSettingsStore()
    settings.open()
    expect(await settings.save()).toBe(true)
    expect(saveMock).not.toHaveBeenCalled()
  })
})
