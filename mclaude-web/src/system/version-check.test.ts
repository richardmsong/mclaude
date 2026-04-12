import { describe, it, expect } from 'vitest'
import { compareVersions, checkClientVersion } from './version-check'

describe('compareVersions', () => {
  it('returns 0 for equal versions', () => {
    expect(compareVersions('1.0.0', '1.0.0')).toBe(0)
    expect(compareVersions('2.5.3', '2.5.3')).toBe(0)
  })

  it('returns negative when a < b (major)', () => {
    expect(compareVersions('1.0.0', '2.0.0')).toBeLessThan(0)
  })

  it('returns positive when a > b (major)', () => {
    expect(compareVersions('2.0.0', '1.0.0')).toBeGreaterThan(0)
  })

  it('handles semantic minor comparison correctly (not lexicographic)', () => {
    // 1.9.0 < 1.10.0 — would fail with lexicographic comparison
    expect(compareVersions('1.9.0', '1.10.0')).toBeLessThan(0)
    expect(compareVersions('1.10.0', '1.9.0')).toBeGreaterThan(0)
  })

  it('patch difference: 1.0.1 > 1.0.0', () => {
    expect(compareVersions('1.0.1', '1.0.0')).toBeGreaterThan(0)
    expect(compareVersions('1.0.0', '1.0.1')).toBeLessThan(0)
  })

  it('minor difference: 1.1.0 > 1.0.9', () => {
    expect(compareVersions('1.1.0', '1.0.9')).toBeGreaterThan(0)
  })

  it('handles missing patch segment (treats as 0)', () => {
    expect(compareVersions('1.0', '1.0.0')).toBe(0)
    expect(compareVersions('1.1', '1.0.9')).toBeGreaterThan(0)
  })
})

describe('checkClientVersion', () => {
  it('returns ok when currentVersion >= minClientVersion (equal)', () => {
    const result = checkClientVersion('1.5.0', '1.5.0')
    expect(result.blocked).toBe(false)
    expect(result.reason).toBe('ok')
  })

  it('returns ok when currentVersion > minClientVersion', () => {
    const result = checkClientVersion('2.0.0', '1.9.9')
    expect(result.blocked).toBe(false)
    expect(result.reason).toBe('ok')
    expect(result.message).toBeUndefined()
  })

  it('returns below_minimum when currentVersion < minClientVersion (first check)', () => {
    const result = checkClientVersion('1.0.0', '1.2.0', 0)
    expect(result.blocked).toBe(true)
    expect(result.reason).toBe('below_minimum')
    expect(result.message).toBeDefined()
    expect(result.message).toContain('Updating')
  })

  it('returns reload_pending when below minimum after already reloading', () => {
    const result = checkClientVersion('1.0.0', '1.2.0', 1)
    expect(result.blocked).toBe(true)
    expect(result.reason).toBe('reload_pending')
    expect(result.message).toBeDefined()
    expect(result.message?.toLowerCase()).toContain('wait')
  })

  it('reload_pending message mentions server is updating', () => {
    const result = checkClientVersion('0.9.0', '1.0.0', 2)
    expect(result.blocked).toBe(true)
    expect(result.reason).toBe('reload_pending')
    expect(result.message).toContain('updating')
  })

  it('exposes currentVersion and minVersion in result', () => {
    const result = checkClientVersion('1.0.0', '2.0.0')
    expect(result.currentVersion).toBe('1.0.0')
    expect(result.minVersion).toBe('2.0.0')
  })

  it('semantic version: 1.9.0 correctly identified as below 1.10.0', () => {
    const result = checkClientVersion('1.9.0', '1.10.0')
    expect(result.blocked).toBe(true)
    expect(result.reason).toBe('below_minimum')
  })
})
