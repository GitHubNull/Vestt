import { describe, expect, it } from 'vitest'
import type { IndexerDTO } from './types'
import {
  buildSearchHash,
  parseIndexersHash,
  parseSearchHash,
  resolveFilterTrackers,
} from './searchHash'

// SPEC-13 §3.1.1: `#search=<q>&tracker=<id|all>&category=<cat>&filter=<t>`.

describe('parseSearchHash', () => {
  it('parses a full deep link', () => {
    expect(parseSearchHash('#search=matrix&tracker=0magnet,acgrip&category=2000&filter=type:public')).toEqual({
      query: 'matrix',
      trackers: ['0magnet', 'acgrip'],
      category: 2000,
      filter: 'type:public',
    })
  })

  it('treats tracker=all (or absent) as every configured indexer', () => {
    expect(parseSearchHash('#search=matrix&tracker=all')?.trackers).toBe('all')
    expect(parseSearchHash('#search=matrix')?.trackers).toBe('all')
  })

  it('decodes URL-encoded queries', () => {
    expect(parseSearchHash('#search=big+buck+bunny')?.query).toBe('big buck bunny')
    expect(parseSearchHash('#search=%E7%94%B5%E5%BD%B1')?.query).toBe('电影')
  })

  it('tolerates the leading slash vue-router injects at boot', () => {
    // createWebHashHistory normalizes `#search=…` → `#/search=…` before
    // main.ts runs (import hoisting); the bare-fragment deep link must
    // still parse.
    expect(parseSearchHash('#/search=matrix&tracker=all')).toEqual({
      query: 'matrix',
      trackers: 'all',
      category: null,
      filter: null,
    })
  })

  it('returns null without a search param (dashboard filter hashes, plain routes)', () => {
    expect(parseSearchHash('#/dashboard?indexers=1&filter=%7B%7D')).toBeNull()
    expect(parseSearchHash('')).toBeNull()
    expect(parseSearchHash('#indexers=1')).toBeNull()
    expect(parseSearchHash('#/dashboard')).toBeNull()
  })

  it('round-trips through buildSearchHash', () => {
    const link = {
      query: 'matrix',
      trackers: ['0magnet'] as string[] | 'all',
      category: 2000,
      filter: 'type:public',
    }
    expect(parseSearchHash(buildSearchHash(link))).toEqual(link)
  })
})

const INDEXERS: IndexerDTO[] = [
  { id: 'pub1', name: 'Pub1', description: '', type: 'public', configured: true, site_link: '', alternativesitelinks: [], language: '', tags: ['movies'], last_error: null, potatoenabled: false, caps: [] },
  { id: 'pub2', name: 'Pub2', description: '', type: 'public', configured: true, site_link: '', alternativesitelinks: [], language: '', tags: ['Movies'], last_error: 'boom', potatoenabled: false, caps: [] },
  { id: 'priv1', name: 'Priv1', description: '', type: 'private', configured: true, site_link: '', alternativesitelinks: [], language: '', tags: [], last_error: null, potatoenabled: false, caps: [] },
  { id: 'unconf', name: 'Unconf', description: '', type: 'public', configured: false, site_link: '', alternativesitelinks: [], language: '', tags: [], last_error: null, potatoenabled: false, caps: [] },
]

describe('parseIndexersHash', () => {
  it('parses a bare upstream filter deep link', () => {
    expect(parseIndexersHash('#indexers=1&filter=%7B%22type%22%3A%22private%22%7D')).toEqual({
      filter: '{"type":"private"}',
    })
  })

  it('tolerates the leading slash vue-router injects at boot', () => {
    expect(parseIndexersHash('#/indexers=1&filter=%7B%7D')).toEqual({ filter: '{}' })
  })

  it('accepts a missing filter', () => {
    expect(parseIndexersHash('#indexers=1')).toEqual({ filter: null })
  })

  it('returns null for in-route hashes and non-indexer fragments', () => {
    expect(parseIndexersHash('#/indexers?indexers=1&filter=%7B%7D')).toBeNull()
    expect(parseIndexersHash('#/indexers')).toBeNull()
    expect(parseIndexersHash('#search=matrix')).toBeNull()
    expect(parseIndexersHash('')).toBeNull()
  })
})

describe('resolveFilterTrackers', () => {
  it('null filter resolves to every configured indexer', () => {
    expect(resolveFilterTrackers(null, INDEXERS)).toEqual(['pub1', 'pub2', 'priv1'])
  })

  it('type:<t> filters by indexer type', () => {
    expect(resolveFilterTrackers('type:public', INDEXERS)).toEqual(['pub1', 'pub2'])
    expect(resolveFilterTrackers('type:private', INDEXERS)).toEqual(['priv1'])
  })

  it('tag:<t> matches case-insensitively over configured indexers only', () => {
    expect(resolveFilterTrackers('tag:movies', INDEXERS)).toEqual(['pub1', 'pub2'])
  })

  it('test:passed / test:failed split on last_error', () => {
    expect(resolveFilterTrackers('test:passed', INDEXERS)).toEqual(['pub1', 'priv1'])
    expect(resolveFilterTrackers('test:failed', INDEXERS)).toEqual(['pub2'])
  })

  it('unknown filters fall back to every configured indexer', () => {
    expect(resolveFilterTrackers('bogus:x', INDEXERS)).toEqual(['pub1', 'pub2', 'priv1'])
  })
})
