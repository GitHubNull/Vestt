// Torznab top-level category constants (SPEC-13 §3.1.2). The full tree lives
// in the backend (common/models/torznab_category.go, ported verbatim from
// upstream TorznabCatType.cs); the search dialog only offers the main
// classes, matching the upstream UI which lists tracker main caps.

export interface TorznabCategory {
  ID: number
  Name: string
}

export const TORZNAB_MAIN_CATEGORIES: TorznabCategory[] = [
  { ID: 1000, Name: 'Console' },
  { ID: 2000, Name: 'Movies' },
  { ID: 3000, Name: 'Audio' },
  { ID: 4000, Name: 'PC' },
  { ID: 5000, Name: 'TV' },
  { ID: 6000, Name: 'XXX' },
  { ID: 7000, Name: 'Other' },
  { ID: 8000, Name: 'Books' },
]

/** Option label in the upstream format "ID (Name)" (custom.js showSearch). */
export function categoryLabel(cat: TorznabCategory): string {
  return `${cat.ID} (${cat.Name})`
}
