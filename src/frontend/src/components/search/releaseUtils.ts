// Shared pure helpers for search result rendering (SPEC-13 §3.1.4 + UI 改版):
// extracted from SearchResultTable.vue so the DataView-style card list
// (SearchResultList.vue) and the DataTable share one implementation.
// Label/link logic ports upstream updateSearchResultTable / updateReleasesRow
// (custom.js:1307-1457, :969-1045).

import { i18n } from '../../i18n'
import { notify } from '../common/notify'
import { getBasePath } from '../../services/api'
import type { TrackerCacheResult } from '../../services/types'

export interface TitleLabel {
  text: string
  cls: string
}

export interface ExternalIdLink {
  label: string
  href: string
}

export function rowKey(r: TrackerCacheResult): string {
  return `${r.TrackerId ?? r.Tracker ?? ''}-${r.Guid ?? r.Title}`
}

export function titleLabels(r: TrackerCacheResult): TitleLabel[] {
  const labels: TitleLabel[] = []
  const dvf = r.DownloadVolumeFactor
  if (dvf !== undefined && dvf !== null && !isNaN(dvf)) {
    if (dvf === 0) labels.push({ text: 'FREELEECH', cls: 'label-success' })
    else if (dvf < 1) labels.push({ text: `${(dvf * 100).toFixed(0)}%DL`, cls: 'label-primary' })
    else if (dvf > 1) labels.push({ text: `${(dvf * 100).toFixed(0)}%DL`, cls: 'label-danger' })
  }
  const ulf = r.UploadVolumeFactor
  if (ulf !== undefined && ulf !== null && !isNaN(ulf)) {
    if (ulf === 0) labels.push({ text: 'NO UPLOAD', cls: 'label-warning' })
    else if (ulf !== 1) labels.push({ text: `${(ulf * 100).toFixed(0)}%UL`, cls: 'label-info' })
  }
  return labels
}

export function externalIdLinks(r: TrackerCacheResult): ExternalIdLink[] {
  const links: ExternalIdLink[] = []
  if (r.Imdb) {
    const len = String(r.Imdb).length > 7 ? 8 : 7
    const id = 'tt' + ('00000000' + r.Imdb).slice(-len)
    links.push({ label: 'IMDB', href: `https://www.imdb.com/title/${id}/` })
  }
  if (r.TMDb && r.TMDb > 0) {
    const type = (r.CategoryDesc ?? '').includes('Movies') ? 'movie' : 'tv'
    links.push({ label: 'TMDB', href: `https://www.themoviedb.org/${type}/${r.TMDb}` })
  }
  if (r.TVDBId && r.TVDBId > 0) {
    links.push({ label: 'TVDB', href: `https://thetvdb.com/?tab=series&id=${r.TVDBId}` })
  }
  return links
}

/** Case-insensitive substring filter over the already-loaded result set
 * (UI 改版: local secondary filter, no backend round-trip). */
export function filterByTitle(rows: TrackerCacheResult[], q: string): TrackerCacheResult[] {
  const needle = q.trim().toLowerCase()
  if (!needle) return rows
  return rows.filter((r) => r.Title.toLowerCase().includes(needle))
}

export async function blackhole(r: TrackerCacheResult): Promise<void> {
  if (!r.BlackholeLink) return
  const t = i18n.global.t
  try {
    const res = await fetch(getBasePath() + r.BlackholeLink)
    if (!res.ok) throw new Error(await res.text())
    notify.success(t('results.blackholeOk'))
  } catch (err) {
    notify.danger(`Error: ${err instanceof Error ? err.message : String(err)}`)
  }
}
