// fetch wrapper + named API functions (frontend.md §3 contract layer).
// Base path resolution: before the first server/config response the base path
// is inferred from location.pathname (the SPA is served at
// {basePath}/UI/Dashboard); afterwards basepathoverride from the config
// response wins.
import type {
  CachedLog,
  ConfigFieldDef,
  ConfigItem,
  IndexerDTO,
  ManualSearchResult,
  ServerConfig,
  ServerConfigResponse,
  TrackerCacheResult,
} from './types'

export interface ApiError {
  status: number
  message: string
}

let basePath = ''
let basePathLocked = false
let apiKey = ''

/** Pins the API key from the server config (upstream api.js appends
 *  `?apikey=` to the manual-search request, custom.js getResults). */
export function lockApiKey(key: string): void {
  apiKey = key
}

/** Returns the pinned API key (empty before the first config load). */
export function getApiKey(): string {
  return apiKey
}

/** Infers the mount base path from the SPA location (…/UI/Dashboard → prefix). */
export function inferBasePath(pathname: string): string {
  const stripped = pathname.replace(/\/+$/, '')
  const m = stripped.match(/^(.*)\/UI\/(?:Dashboard|Login)$/i)
  return m ? m[1] : ''
}

/** Seeds the base path before the first request; called once at startup. */
export function initBasePath(pathname: string): void {
  if (!basePathLocked) basePath = inferBasePath(pathname)
}

/** Pins the base path from the server config (basepathoverride). */
export function lockBasePath(override: string): void {
  basePath = override || basePath
  basePathLocked = true
}

export function getBasePath(): string {
  return basePath
}

/** Test hook: restores the uninitialized base-path state. */
export function _resetForTests(): void {
  basePath = ''
  basePathLocked = false
  apiKey = ''
}

// 401 handling is injectable so unit tests can observe it without a DOM.
let unauthorizedHandler: () => void = () => {
  if (typeof window !== 'undefined') window.location.hash = '/login'
}
export function setUnauthorizedHandler(fn: () => void): void {
  unauthorizedHandler = fn
}

async function request<T>(
  method: string,
  url: string,
  body?: unknown,
  formEncoded = false,
): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined && !formEncoded) headers['Content-Type'] = 'application/json'
  const init: RequestInit = { method, headers }
  if (body !== undefined) {
    init.body = formEncoded ? (body as string) : JSON.stringify(body)
  }
  const res = await fetch(basePath + url, init)
  if (res.status === 401) {
    unauthorizedHandler()
    throw { status: 401, message: 'unauthorized' } satisfies ApiError
  }
  if (!res.ok) {
    throw { status: res.status, message: await res.text() } satisfies ApiError
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

// --- §1 server ------------------------------------------------------------

export function getServerConfig(): Promise<ServerConfig> {
  return request<ServerConfig>('GET', '/api/v2.0/server/config')
}

export function postServerConfig(patch: Partial<ServerConfig>): Promise<ServerConfigResponse> {
  return request<ServerConfigResponse>('POST', '/api/v2.0/server/config', patch)
}

export function setAdminPassword(password: string): Promise<void> {
  return request<void>('POST', '/api/v2.0/server/adminpassword', password)
}

export function getLogs(): Promise<CachedLog[]> {
  return request<CachedLog[]>('GET', '/api/v2.0/server/logs')
}

export function shutdown(apiKey: string): Promise<{ ShuttingDown: boolean }> {
  return request('POST', `/api/v2.0/server/shutdown?apikey=${encodeURIComponent(apiKey)}`)
}

/** Triggers the update check (backend-api.md §1.5; upstream
 *  api.updateServer → "Updater triggered see log for details.."). */
export function checkForUpdates(): Promise<void> {
  return request<void>('POST', '/api/v2.0/server/update')
}

// --- §2 indexers ------------------------------------------------------------

export function getIndexers(configured?: boolean): Promise<IndexerDTO[]> {
  const q = configured === undefined ? '' : `?configured=${configured}`
  return request<IndexerDTO[]>('GET', `/api/v2.0/indexers${q}`)
}

export function getIndexerConfig(indexerId: string): Promise<ConfigFieldDef[]> {
  return request<ConfigFieldDef[]>('GET', `/api/v2.0/indexers/${encodeURIComponent(indexerId)}/Config`)
}

export function postIndexerConfig(indexerId: string, items: ConfigItem[]): Promise<void> {
  return request<void>(
    'POST',
    `/api/v2.0/indexers/${encodeURIComponent(indexerId)}/Config`,
    items,
  )
}

export function testIndexer(indexerId: string): Promise<void> {
  return request<void>('POST', `/api/v2.0/indexers/${encodeURIComponent(indexerId)}/Test`)
}

export function deleteIndexer(indexerId: string): Promise<void> {
  return request<void>('DELETE', `/api/v2.0/indexers/${encodeURIComponent(indexerId)}`)
}

export function getCache(): Promise<TrackerCacheResult[]> {
  return request<TrackerCacheResult[]>('GET', '/api/v2.0/indexers/Cache')
}

// --- §3 results -------------------------------------------------------------

export interface ManualSearchParams {
  query?: string
  trackers?: string[]
  categories?: number[]
  imdbid?: string
  tmdbid?: string
  tvdbid?: string
  tvmazeid?: string
}

export function manualSearch(indexerId: string, params: ManualSearchParams): Promise<ManualSearchResult> {
  const q = new URLSearchParams()
  // upstream: indexers/{id}/results?apikey=<key> (libs/api.js:51)
  if (apiKey) q.set('apikey', apiKey)
  if (params.query) q.set('query', params.query)
  if (params.trackers?.length) q.set('Tracker[]', params.trackers.join(','))
  if (params.categories?.length) q.set('Category[]', params.categories.join(','))
  if (params.imdbid) q.set('imdbid', params.imdbid)
  if (params.tmdbid) q.set('tmdbid', params.tmdbid)
  if (params.tvdbid) q.set('tvdbid', params.tvdbid)
  if (params.tvmazeid) q.set('tvmazeid', params.tvmazeid)
  const qs = q.toString()
  return request<ManualSearchResult>(
    'GET',
    `/api/v2.0/indexers/${encodeURIComponent(indexerId)}/results${qs ? `?${qs}` : ''}`,
  )
}

// --- §5 UI session ----------------------------------------------------------

export function logout(): Promise<void> {
  return request<void>('GET', '/UI/Logout')
}

/** Submits the admin password form (upstream POST /UI/Dashboard form-encoded).
 *  Upstream decides success by whether the redirect chain lands back on the
 *  dashboard; Vestt serves the SPA there unconditionally (SPEC-11 §3.4), so
 *  the session is verified with a config probe instead: GET server/config
 *  200 → logged in, 401 → wrong password (cookie simply absent). */
export async function loginForm(password: string): Promise<boolean> {
  const body = new URLSearchParams({ password }).toString()
  await fetch(`${basePath}/UI/Dashboard`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
    redirect: 'follow',
  })
  const probe = await fetch(`${basePath}/api/v2.0/server/config`)
  return probe.status === 200
}
