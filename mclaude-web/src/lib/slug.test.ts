import { describe, it, expect } from 'vitest'
import { slugify, validate, deriveUserSlug, RESERVED_WORDS } from './slug'

describe('slugify', () => {
  it('lowercases ASCII', () => {
    expect(slugify('Hello World')).toBe('hello-world')
  })

  it('strips accents via NFD decomposition', () => {
    expect(slugify('Caf\u00e9')).toBe('cafe')
    expect(slugify('Na\u00efve')).toBe('naive')
    expect(slugify('r\u00e9sum\u00e9')).toBe('resume')
  })

  it('handles emoji-only input (returns empty string)', () => {
    // Emoji get stripped after NFD + combining mark removal, leaving only hyphens
    // which are then trimmed
    const result = slugify('\u{1F600}')
    expect(result).toBe('')
  })

  it('replaces runs of non-alnum with a single hyphen', () => {
    expect(slugify('foo  bar---baz')).toBe('foo-bar-baz')
    expect(slugify('hello!@#world')).toBe('hello-world')
  })

  it('trims leading and trailing hyphens', () => {
    expect(slugify('---hello---')).toBe('hello')
    expect(slugify('   spaces   ')).toBe('spaces')
  })

  it('truncates at 63 characters', () => {
    const long = 'a'.repeat(100)
    expect(slugify(long)).toHaveLength(63)
  })

  it('does not end with a hyphen after truncation', () => {
    // 62 'a' chars + non-alnum run — truncation should not leave trailing hyphen
    const s = 'a'.repeat(62) + '---more'
    const result = slugify(s)
    expect(result).not.toMatch(/-$/)
  })

  it('handles empty string', () => {
    expect(slugify('')).toBe('')
  })

  it('handles numeric-only names', () => {
    expect(slugify('42')).toBe('42')
  })
})

describe('validate', () => {
  it('accepts a valid slug', () => {
    expect(validate('alice-gmail')).toBeNull()
    expect(validate('my-project-123')).toBeNull()
    expect(validate('a')).toBeNull()
  })

  it('rejects empty string', () => {
    expect(validate('')?.message).toContain('empty')
  })

  it('rejects leading underscore', () => {
    expect(validate('_internal')).not.toBeNull()
  })

  it('rejects uppercase', () => {
    expect(validate('Alice')).not.toBeNull()
  })

  it('rejects spaces', () => {
    expect(validate('hello world')).not.toBeNull()
  })

  it('rejects slug longer than 63 chars', () => {
    expect(validate('a'.repeat(64))).not.toBeNull()
  })

  it('accepts slug of exactly 63 chars', () => {
    expect(validate('a'.repeat(63))).toBeNull()
  })

  it('rejects all 10 reserved words', () => {
    for (const word of RESERVED_WORDS) {
      const err = validate(word)
      expect(err, `reserved word "${word}" should be rejected`).not.toBeNull()
    }
  })

  it('rejects slug starting with hyphen', () => {
    expect(validate('-hello')).not.toBeNull()
  })

  it('accepts slug starting with digit', () => {
    expect(validate('1project')).toBeNull()
  })

  it('rejects slug with dot', () => {
    expect(validate('hello.world')).not.toBeNull()
  })

  it('rejects slug with slash', () => {
    expect(validate('hello/world')).not.toBeNull()
  })
})

describe('deriveUserSlug', () => {
  it('combines slugified name with domain first segment', () => {
    expect(deriveUserSlug('Richard', 'richard@rbc.com')).toBe('richard-rbc')
    expect(deriveUserSlug('Alice', 'alice@gmail.com')).toBe('alice-gmail')
  })

  it('ADR-0024 example: richard@rbc.com', () => {
    expect(deriveUserSlug('Richard Song', 'richard@rbc.com')).toBe('richard-song-rbc')
  })

  it('ADR-0024 example: alice@gmail.com', () => {
    expect(deriveUserSlug('Alice', 'alice@gmail.com')).toBe('alice-gmail')
  })

  it('uses only domain first segment for multi-segment domains', () => {
    // bob@company.co.uk → bob-company (not bob-company-co-uk)
    expect(deriveUserSlug('Bob', 'bob@company.co.uk')).toBe('bob-company')
  })

  it('falls back to email local-part when display name is empty', () => {
    expect(deriveUserSlug('', 'alice@gmail.com')).toBe('alice-gmail')
  })

  it('handles email without @ domain part', () => {
    // No domain segment — just use the name/local part
    const result = deriveUserSlug('Alice', 'alice')
    expect(result).toBe('alice')
  })

  it('truncates combined slug to 63 chars', () => {
    const longName = 'a'.repeat(60)
    const result = deriveUserSlug(longName, 'user@example.com')
    expect(result.length).toBeLessThanOrEqual(63)
  })

  it('does not end with hyphen', () => {
    const result = deriveUserSlug('test', 'test@x.com')
    expect(result).not.toMatch(/-$/)
  })
})
