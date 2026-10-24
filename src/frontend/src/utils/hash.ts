// location.hash deep-link helpers (frontend.md §2): upstream-style
// "#indexers=1&filter=private" fragments, consumed by SPEC-13's search dialog.
// Values are URI-decoded on parse and encoded on serialize; key order is
// preserved on round-trip.

export type HashParams = Record<string, string>

/** Parses a raw hash fragment ("#a=1&b=two" or "a=1&b=two") into key/value pairs. */
export function parseHash(hash: string): HashParams {
  const out: HashParams = {}
  const frag = hash.replace(/^#/, '')
  if (frag === '') return out
  for (const pair of frag.split('&')) {
    if (pair === '') continue
    const eq = pair.indexOf('=')
    if (eq < 0) {
      out[decodeURIComponent(pair)] = ''
    } else {
      out[decodeURIComponent(pair.slice(0, eq))] = decodeURIComponent(
        pair.slice(eq + 1).replace(/\+/g, ' '),
      )
    }
  }
  return out
}

/** Serializes key/value pairs into a hash fragment ("#a=1&b=two"). */
export function serializeHash(params: HashParams): string {
  const pairs = Object.entries(params)
    .filter(([, v]) => v !== undefined)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
  return pairs.length ? `#${pairs.join('&')}` : ''
}

/** Reads the current location.hash (usable in any history mode). */
export function readLocationHash(): HashParams {
  if (typeof window === 'undefined') return {}
  return parseHash(window.location.hash)
}

/** Replaces the current hash without adding a history entry. */
export function writeLocationHash(params: HashParams): void {
  if (typeof window === 'undefined') return
  const target = serializeHash(params)
  if (window.location.hash !== target) {
    history.replaceState(null, '', window.location.pathname + window.location.search + target)
  }
}
