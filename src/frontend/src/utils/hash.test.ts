import { describe, expect, it } from 'vitest'
import { parseHash, serializeHash } from './hash'

describe('parseHash', () => {
  it('parses key=value pairs', () => {
    expect(parseHash('#indexers=1&filter=private')).toEqual({
      indexers: '1',
      filter: 'private',
    })
  })

  it('accepts the fragment without a leading hash', () => {
    expect(parseHash('search=test&tracker=all')).toEqual({
      search: 'test',
      tracker: 'all',
    })
  })

  it('decodes percent-encoded values and keys', () => {
    expect(parseHash('#q=hello%20world&Category%5B%5D=2000')).toEqual({
      q: 'hello world',
      'Category[]': '2000',
    })
  })

  it('returns an empty object for an empty fragment', () => {
    expect(parseHash('')).toEqual({})
    expect(parseHash('#')).toEqual({})
  })

  it('treats a key without "=" as an empty value', () => {
    expect(parseHash('#flag')).toEqual({ flag: '' })
  })
})

describe('serializeHash', () => {
  it('round-trips with parseHash', () => {
    const params = { search: 'a b', tracker: 'all', category: '2000' }
    expect(parseHash(serializeHash(params))).toEqual(params)
  })

  it('produces an empty string for empty input', () => {
    expect(serializeHash({})).toBe('')
  })

  it('encodes reserved characters', () => {
    expect(serializeHash({ 'Tracker[]': 'a,b' })).toBe('#Tracker%5B%5D=a%2Cb')
  })
})
