import { describe, expect, it } from 'vitest'
import { absoluteDate, jacketSize, jacketTimespan } from './format'

// Ports of the upstream handlebars helpers (libs/handlebarsmoment.js).

describe('jacketSize', () => {
  it('formats binary units with up to 2 decimals', () => {
    expect(jacketSize(500 * 1024 * 1024)).toBe('500 MB')
    expect(jacketSize(1024)).toBe('1 KB')
    expect(jacketSize(1234567)).toBe('1.18 MB')
    expect(jacketSize(0)).toBe('0 B')
  })

  it('returns empty for missing values', () => {
    expect(jacketSize(null)).toBe('')
    expect(jacketSize(undefined)).toBe('')
  })
})

describe('jacketTimespan', () => {
  const now = new Date('2026-10-24T12:00:00Z')

  it('uses minutes below 120m', () => {
    expect(jacketTimespan('2026-10-24T11:30:00Z', now)).toBe('30m ago')
  })

  it('uses one-decimal hours below 48h', () => {
    expect(jacketTimespan('2026-10-23T12:00:00Z', now)).toBe('24.0h ago')
  })

  it('uses days below a year', () => {
    expect(jacketTimespan('2026-10-14T12:00:00Z', now)).toBe('10d ago')
  })

  it('uses years beyond 365d', () => {
    expect(jacketTimespan('2023-10-24T12:00:00Z', now)).toBe('3y ago')
  })
})

describe('absoluteDate', () => {
  it('renders YYYY-MM-DD HH:mm:ss Z in local time', () => {
    const out = absoluteDate('2026-10-24T04:00:00Z')
    expect(out).toMatch(/^2026-10-24 \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}$/)
  })

  it('returns empty for missing/invalid values', () => {
    expect(absoluteDate(null)).toBe('')
    expect(absoluteDate('not-a-date')).toBe('')
  })
})
