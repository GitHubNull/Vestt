// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { _resetForTests, initBasePath } from '../../services/api'
import type { TrackerCacheResult } from '../../services/types'
import { externalIdLinks, filterByTitle, rowKey, titleLabels } from './releaseUtils'

// Shared result-render helpers (SPEC-13 §3.1.4 ports + UI 改版 local filter).
// blackhole() is exercised through SearchResultList.spec.ts (fetch + notify).

function release(overrides: Partial<TrackerCacheResult> = {}): TrackerCacheResult {
  return { Title: 'Some Release 1080p', ...overrides }
}

describe('titleLabels', () => {
  it('emits FREELEECH for a zero download factor', () => {
    expect(titleLabels(release({ DownloadVolumeFactor: 0 }))).toEqual([
      { text: 'FREELEECH', cls: 'label-success' },
    ])
  })

  it('emits discounted and inflated download factors', () => {
    expect(titleLabels(release({ DownloadVolumeFactor: 0.5 }))[0].text).toBe('50%DL')
    expect(titleLabels(release({ DownloadVolumeFactor: 2 }))[0]).toEqual({
      text: '200%DL',
      cls: 'label-danger',
    })
  })

  it('emits NO UPLOAD and inflated upload factors', () => {
    expect(titleLabels(release({ UploadVolumeFactor: 0 }))[0]).toEqual({
      text: 'NO UPLOAD',
      cls: 'label-warning',
    })
    expect(titleLabels(release({ UploadVolumeFactor: 1.5 }))[0].text).toBe('150%UL')
  })

  it('emits nothing for neutral or missing factors', () => {
    expect(titleLabels(release({ DownloadVolumeFactor: 1, UploadVolumeFactor: 1 }))).toEqual([])
    expect(titleLabels(release())).toEqual([])
    expect(titleLabels(release({ DownloadVolumeFactor: NaN }))).toEqual([])
  })
})

describe('externalIdLinks', () => {
  it('zero-pads short Imdb ids to the tt prefix', () => {
    const links = externalIdLinks(release({ Imdb: 123456 }))
    expect(links).toEqual([{ label: 'IMDB', href: 'https://www.imdb.com/title/tt0123456/' }])
  })

  it('keeps 8-digit Imdb ids unpadded', () => {
    const links = externalIdLinks(release({ Imdb: 12345678 }))
    expect(links[0].href).toBe('https://www.imdb.com/title/tt12345678/')
  })

  it('routes TMDb by category and includes TVDB', () => {
    const movie = externalIdLinks(release({ TMDb: 42, CategoryDesc: 'Movies/HD' }))
    expect(movie[0].href).toBe('https://www.themoviedb.org/movie/42')
    const tv = externalIdLinks(release({ TMDb: 42, CategoryDesc: 'TV/HD', TVDBId: 7 }))
    expect(tv[0].href).toBe('https://www.themoviedb.org/tv/42')
    expect(tv[1]).toEqual({ label: 'TVDB', href: 'https://thetvdb.com/?tab=series&id=7' })
  })

  it('returns no links when all ids are missing', () => {
    expect(externalIdLinks(release())).toEqual([])
  })
})

describe('rowKey', () => {
  it('prefers Guid and falls back through TrackerId/Tracker/Title', () => {
    expect(rowKey(release({ TrackerId: 't1', Guid: 'g1' }))).toBe('t1-g1')
    expect(rowKey(release({ Tracker: 'T', Title: 'x' }))).toBe('T-x')
    expect(rowKey(release({ Title: 'only' }))).toBe('-only')
  })
})

describe('filterByTitle', () => {
  const rows = [
    release({ Title: 'Ubuntu ISO 24.04' }),
    release({ Title: 'ubuntu server pack' }),
    release({ Title: 'Fedora Workstation' }),
  ]

  it('matches case-insensitively', () => {
    expect(filterByTitle(rows, 'UBUNTU')).toHaveLength(2)
    expect(filterByTitle(rows, 'fedora')).toHaveLength(1)
  })

  it('returns everything for blank input', () => {
    expect(filterByTitle(rows, '  ')).toHaveLength(3)
  })

  it('returns nothing when nothing matches', () => {
    expect(filterByTitle(rows, 'debian')).toEqual([])
  })
})

describe('blackhole', () => {
  beforeEach(() => {
    _resetForTests()
    initBasePath('/')
  })

  it('notifies success on ok and danger on failure', async () => {
    const { blackhole } = await import('./releaseUtils')
    const { notify } = await import('../common/notify')
    const success = vi.spyOn(notify, 'success')
    const danger = vi.spyOn(notify, 'danger')
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('ok', { status: 200 })),
    )
    await blackhole(release({ BlackholeLink: '/blackhole/1' }))
    expect(success).toHaveBeenCalledOnce()
    expect(danger).not.toHaveBeenCalled()

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('boom', { status: 500 })),
    )
    await blackhole(release({ BlackholeLink: '/blackhole/2' }))
    expect(danger).toHaveBeenCalledOnce()
    vi.unstubAllGlobals()
  })

  it('no-ops without a BlackholeLink', async () => {
    const { blackhole } = await import('./releaseUtils')
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await blackhole(release())
    expect(fetchMock).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
})
