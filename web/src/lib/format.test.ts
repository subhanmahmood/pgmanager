import { describe, expect, it } from 'vitest'
import {
  cellText,
  envSegment,
  expiringSoon,
  fmtBytes,
  inputValue,
  isExpired,
  parseEnvSegment,
  primaryKeyColumns,
  relativeExpiry,
  truncate,
} from './format'

describe('envSegment', () => {
  // This segment feeds every database API path and every database route, so a
  // mistake here breaks the whole subtree at once.
  it('uses the bare env for non-PR databases', () => {
    expect(envSegment({ env: 'dev' })).toBe('dev')
    expect(envSegment({ env: 'prod' })).toBe('prod')
  })

  it('encodes the PR number', () => {
    expect(envSegment({ env: 'pr', pr_number: 42 })).toBe('pr_42')
  })

  it('round-trips through parseEnvSegment', () => {
    expect(parseEnvSegment('pr_42')).toEqual({ env: 'pr', prNumber: 42 })
    expect(parseEnvSegment('staging')).toEqual({ env: 'staging' })
    // "pr" alone is the env, not a malformed PR segment.
    expect(parseEnvSegment('pr')).toEqual({ env: 'pr' })
  })
})

describe('cellText / inputValue', () => {
  // null, the string "null", and "" are all legitimate column values. Confusing
  // any two of them corrupts a row on save.
  it('keeps null distinguishable from its string form and from empty', () => {
    expect(cellText(null)).toBe('NULL')
    expect(cellText(undefined)).toBe('NULL')
    expect(cellText('null')).toBe('null')
    expect(cellText('')).toBe('')

    // The editor expresses NULL with a checkbox, so the text is empty for both
    // null and "" — which is exactly why the checkbox has to exist.
    expect(inputValue(null)).toBe('')
    expect(inputValue(undefined)).toBe('')
    expect(inputValue('')).toBe('')
    expect(inputValue('null')).toBe('null')
  })

  it('serialises objects the same way in both', () => {
    expect(cellText({ a: 1 })).toBe('{"a":1}')
    expect(inputValue({ a: 1 })).toBe('{"a":1}')
    expect(inputValue([1, 2])).toBe('[1,2]')
  })

  it('preserves falsy scalars', () => {
    expect(cellText(0)).toBe('0')
    expect(cellText(false)).toBe('false')
    expect(inputValue(0)).toBe('0')
    expect(inputValue(false)).toBe('false')
  })
})

describe('relativeExpiry', () => {
  it('reports never for an absent expiry', () => {
    expect(relativeExpiry(undefined)).toBe('never')
    expect(relativeExpiry(null)).toBe('never')
  })

  it('reports expired for a past timestamp', () => {
    expect(relativeExpiry(new Date(Date.now() - 60_000).toISOString())).toBe('expired')
  })

  it('rounds down to days, then hours, then <1h', () => {
    const inMs = (ms: number) => new Date(Date.now() + ms).toISOString()
    expect(relativeExpiry(inMs(3 * 86_400_000 + 5_000))).toBe('in 3d')
    expect(relativeExpiry(inMs(5 * 3_600_000))).toBe('in 5h')
    expect(relativeExpiry(inMs(120_000))).toBe('in <1h')
  })
})

describe('isExpired / expiringSoon', () => {
  it('treats an absent expiry as neither', () => {
    expect(isExpired(undefined)).toBe(false)
    expect(expiringSoon(undefined)).toBe(false)
  })

  it('does not call an already-expired database "expiring soon"', () => {
    const past = new Date(Date.now() - 1000).toISOString()
    expect(isExpired(past)).toBe(true)
    expect(expiringSoon(past)).toBe(false)
  })

  it('flags an expiry inside the window', () => {
    expect(expiringSoon(new Date(Date.now() + 3_600_000).toISOString())).toBe(true)
    expect(expiringSoon(new Date(Date.now() + 48 * 3_600_000).toISOString())).toBe(false)
  })
})

describe('primaryKeyColumns', () => {
  it('returns the key columns in order, empty when there is no key', () => {
    expect(
      primaryKeyColumns([
        { name: 'id', primary_key: true },
        { name: 'email', primary_key: false },
      ]),
    ).toEqual(['id'])

    expect(primaryKeyColumns([{ name: 'a', primary_key: false }])).toEqual([])

    expect(
      primaryKeyColumns([
        { name: 'tenant', primary_key: true },
        { name: 'id', primary_key: true },
      ]),
    ).toEqual(['tenant', 'id'])
  })
})

describe('fmtBytes', () => {
  // Mirrors cmd/pgmanager's humanBytes — same base unit, same rounding — so
  // a size shown in the CLI and the admin UI never disagree.
  it('keeps sub-KiB sizes as whole bytes', () => {
    expect(fmtBytes(0)).toBe('0 B')
    expect(fmtBytes(1023)).toBe('1023 B')
  })

  it('renders larger sizes with one decimal and the right unit', () => {
    expect(fmtBytes(1024)).toBe('1.0 KiB')
    expect(fmtBytes(1536)).toBe('1.5 KiB')
    expect(fmtBytes(5 * 1024 * 1024)).toBe('5.0 MiB')
    expect(fmtBytes(2 * 1024 * 1024 * 1024)).toBe('2.0 GiB')
  })
})

describe('truncate', () => {
  it('leaves short strings alone and ellipsises long ones', () => {
    expect(truncate('short', 10)).toBe('short')
    expect(truncate('abcdefghijk', 5)).toBe('abcd…')
  })
})
