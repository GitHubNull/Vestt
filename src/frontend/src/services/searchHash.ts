// Search deep link (SPEC-13 §3.1.1): `#search=<q>&tracker=<id|all>&category=<cat>&filter=<t>`
// (frontend.md §2 keeps upstream's location.hash semantics). `search` present
// → open the dialog and execute. `filter` resolves to a tracker subset via the
// upstream availableFilters id space (custom.js:219-237: type:/tag:/test:).

import type { IndexerDTO } from './types'

export interface SearchDeepLink {
  query: string
  /** raw tracker param; 'all'/empty → every configured indexer. */
  trackers: string[] | 'all'
  category: number | null
  filter: string | null
}

export function parseSearchHash(hash: string): SearchDeepLink | null {
  // vue-router's createWebHashHistory normalizes a bare `#search=…` into
  // `#/search=…` at router creation (which import hoisting runs before
  // main.ts) — tolerate the injected leading slash.
  const body = (hash.startsWith('#') ? hash.slice(1) : hash).replace(/^\/+/, '')
  if (body === '') return null
  // route hashes carry their query in-fragment — those are not search deep
  // links. Keep in sync with the real route table.
  if (/^(home|login|indexers|settings|dashboard)([/?]|$)/.test(body)) return null
  const params = new URLSearchParams(body)
  if (!params.has('search')) return null
  const trackerParam = params.get('tracker')
  let trackers: string[] | 'all' = 'all'
  if (trackerParam && trackerParam.trim() !== '' && trackerParam !== 'all') {
    trackers = trackerParam
      .split(',')
      .map((t) => t.trim())
      .filter(Boolean)
  }
  const catParam = params.get('category')
  const category = catParam !== null && catParam !== '' ? Number(catParam) : null
  return {
    query: params.get('search') ?? '',
    trackers,
    category: category !== null && !isNaN(category) ? category : null,
    filter: params.get('filter'),
  }
}

export function buildSearchHash(link: SearchDeepLink): string {
  const params = new URLSearchParams()
  params.set('search', link.query)
  if (link.trackers === 'all') {
    params.set('tracker', 'all')
  } else if (link.trackers.length > 0) {
    params.set('tracker', link.trackers.join(','))
  } else {
    params.set('tracker', 'all')
  }
  if (link.category !== null) params.set('category', String(link.category))
  if (link.filter) params.set('filter', link.filter)
  return `#${params.toString()}`
}

/** Resolves an upstream-style filter id to configured indexer ids.
 *  Id space mirrors custom.js configureFilters: type:<t>, tag:<t>,
 *  test:passed / test:failed. Unknown ids yield every configured indexer. */
export function resolveFilterTrackers(filter: string | null, indexers: IndexerDTO[]): string[] {
  const configured = indexers.filter((i) => i.configured)
  if (!filter) return configured.map((i) => i.id)
  const [kind, ...rest] = filter.split(':')
  const value = rest.join(':')
  switch (kind) {
    case 'type':
      return configured.filter((i) => i.type === value).map((i) => i.id)
    case 'tag':
      return configured
        .filter((i) => i.tags.some((t) => t.toLowerCase() === value.toLowerCase()))
        .map((i) => i.id)
    case 'test':
      return configured
        .filter((i) => (value === 'failed' ? !!i.last_error : !i.last_error))
        .map((i) => i.id)
    default:
      return configured.map((i) => i.id)
  }
}

// --- pending deep link across the router redirect ---------------------------
// The app runs on vue-router hash history; the bare `#search=…` fragment is
// not a route, so the router's catch-all redirects to /dashboard and rewrites
// the hash before any component mounts. main.ts stashes the raw fragment in
// sessionStorage first; DashboardView consumes it and restores the URL.

export const PENDING_SEARCH_HASH_KEY = 'vestt.pendingSearchHash'

export function capturePendingSearchHash(hash: string): void {
  if (typeof window === 'undefined') return
  if (parseSearchHash(hash)) {
    window.sessionStorage.setItem(PENDING_SEARCH_HASH_KEY, hash)
  }
}

export function consumePendingSearchHash(): string | null {
  if (typeof window === 'undefined') return null
  const raw = window.sessionStorage.getItem(PENDING_SEARCH_HASH_KEY)
  window.sessionStorage.removeItem(PENDING_SEARCH_HASH_KEY)
  return raw
}

/** Restores the deep link into the URL without triggering the router
 *  (replaceState fires no hashchange/popstate), keeping refreshes working. */
export function restoreSearchHash(hash: string): void {
  if (typeof window === 'undefined') return
  const withHash = hash.startsWith('#') ? hash : `#${hash}`
  window.history.replaceState(null, '', withHash)
}

// --- indexer filter deep link (upstream #indexers=1&filter=<json>) -----------
// The bare fragment is not a route, so the router's catch-all rewrites the
// hash before any component mounts. main.ts stashes it in sessionStorage;
// the router guard consumes it and navigates to /indexers with the filter in
// the route query (the same shape in-route URLs already use).

export interface IndexersDeepLink {
  filter: string | null
}

export function parseIndexersHash(hash: string): IndexersDeepLink | null {
  const body = (hash.startsWith('#') ? hash.slice(1) : hash).replace(/^\/+/, '')
  if (body === '') return null
  // in-route `#/indexers?indexers=1…` is handled natively by the route query.
  if (/^indexers([/?]|$)/.test(body)) return null
  const params = new URLSearchParams(body)
  if (!params.has('indexers')) return null
  return { filter: params.get('filter') }
}

export const PENDING_INDEXERS_HASH_KEY = 'vestt.pendingIndexersHash'

export function capturePendingIndexersHash(hash: string): void {
  if (typeof window === 'undefined') return
  if (parseIndexersHash(hash)) {
    window.sessionStorage.setItem(PENDING_INDEXERS_HASH_KEY, hash)
  }
}

export function consumePendingIndexersHash(): IndexersDeepLink | null {
  if (typeof window === 'undefined') return null
  const raw = window.sessionStorage.getItem(PENDING_INDEXERS_HASH_KEY)
  window.sessionStorage.removeItem(PENDING_INDEXERS_HASH_KEY)
  return raw ? parseIndexersHash(raw) : null
}
