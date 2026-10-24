// Display formatting ports of the upstream handlebars helpers
// (Content/libs/handlebarsmoment.js): jacketTimespan / jacketSize /
// dateFormat("YYYY-MM-DD HH:mm:ss Z"). Behavior matches the upstream
// implementations verbatim, including the unit thresholds.

const SIZE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

/** filesize(bytes, { round: 2 }) — binary (1024) units, up to 2 decimals. */
export function jacketSize(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || isNaN(bytes)) return ''
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < SIZE_UNITS.length - 1) {
    value /= 1024
    unit++
  }
  const rounded = Math.round(value * 100) / 100
  return `${rounded} ${SIZE_UNITS[unit]}`
}

/** jacketTimespan: "<n>m ago" < 120m, "<n.n>h ago" < 48h,
 *  "<n>d ago" < 365d, else "<n>y ago" (upstream thresholds verbatim). */
export function jacketTimespan(publishDate: string | null | undefined, now = new Date()): string {
  if (!publishDate) return ''
  const from = new Date(publishDate)
  if (isNaN(from.getTime())) return ''
  const diffMs = now.getTime() - from.getTime()
  const minutes = diffMs / 60000
  if (minutes < 120) return `${Math.round(minutes)}m ago`
  const hours = minutes / 60
  if (hours < 48) return `${hours.toFixed(1)}h ago`
  const days = hours / 24
  if (days < 365) return `${Math.round(days)}d ago`
  const years = days / 365
  return `${Math.round(years)}y ago`
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : `${n}`
}

/** dateFormat "YYYY-MM-DD HH:mm:ss Z" in local time, e.g. 2026-10-24 15:04:05 +08:00. */
export function absoluteDate(publishDate: string | null | undefined): string {
  if (!publishDate) return ''
  const d = new Date(publishDate)
  if (isNaN(d.getTime())) return ''
  const offsetMin = -d.getTimezoneOffset()
  const sign = offsetMin >= 0 ? '+' : '-'
  const abs = Math.abs(offsetMin)
  const z = `${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())} ${z}`
}
