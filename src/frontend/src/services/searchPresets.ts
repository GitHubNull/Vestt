// Search presets (SPEC-13 §3.1.5). Deviation from upstream: Jackett 0.24.2900
// savedPresets are plain name strings naming DataTable filter texts
// (custom.js:1238-1254); Vestt stores structured presets
// {name, query, trackers, cats} per SPEC. Same-name save overwrites
// (aligned with upstream setSavedPresets dedup-by-name).

export interface SearchPreset {
  name: string
  query: string
  trackers: string[]
  cats: number[]
}

export const PRESETS_STORAGE_KEY = 'vestt.presets'

function storage(): Storage | null {
  return typeof window !== 'undefined' ? window.localStorage : null
}

export function loadPresets(store: Storage | null = storage()): SearchPreset[] {
  if (!store) return []
  try {
    const raw = store.getItem(PRESETS_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    const out: SearchPreset[] = []
    for (const p of parsed) {
      if (typeof p !== 'object' || p === null) continue
      const candidate = p as Partial<SearchPreset>
      if (typeof candidate.name !== 'string' || candidate.name === '') continue
      out.push({
        name: candidate.name,
        query: typeof candidate.query === 'string' ? candidate.query : '',
        trackers: Array.isArray(candidate.trackers)
          ? candidate.trackers.filter((t): t is string => typeof t === 'string')
          : [],
        cats: Array.isArray(candidate.cats)
          ? candidate.cats.filter((c): c is number => typeof c === 'number')
          : [],
      })
    }
    return out
  } catch {
    return []
  }
}

/** Save-or-overwrite by name; returns the resulting list. */
export function savePreset(
  presets: SearchPreset[],
  preset: SearchPreset,
  store: Storage | null = storage(),
): SearchPreset[] {
  const next = [...presets.filter((p) => p.name !== preset.name), { ...preset }]
  store?.setItem(PRESETS_STORAGE_KEY, JSON.stringify(next))
  return next
}

export function deletePreset(
  presets: SearchPreset[],
  name: string,
  store: Storage | null = storage(),
): SearchPreset[] {
  const next = presets.filter((p) => p.name !== name)
  store?.setItem(PRESETS_STORAGE_KEY, JSON.stringify(next))
  return next
}
