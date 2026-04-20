/**
 * Typed-slug helpers for mclaude-web.
 * Mirrors semantics of mclaude-common/pkg/slug in TypeScript.
 * ADR-0024: typed slug scheme for subjects, URLs, and KV keys.
 */

// ── Branded types ─────────────────────────────────────────────────────────────

declare const _brand: unique symbol

/** A validated slug for a user identity. */
export type UserSlug = string & { readonly [_brand]: 'UserSlug' }
/** A validated slug for a project. */
export type ProjectSlug = string & { readonly [_brand]: 'ProjectSlug' }
/** A validated slug for a session. */
export type SessionSlug = string & { readonly [_brand]: 'SessionSlug' }
/** A validated slug for a host (BYOH, ADR-0004). */
export type HostSlug = string & { readonly [_brand]: 'HostSlug' }
/** A validated slug for a cluster. */
export type ClusterSlug = string & { readonly [_brand]: 'ClusterSlug' }

// ── Reserved words ────────────────────────────────────────────────────────────

/**
 * Reserved literals that may never appear as a slug value.
 * This list is append-only — removing a word could allow subject shadowing.
 */
export const RESERVED_WORDS = Object.freeze([
  'users',
  'hosts',
  'projects',
  'sessions',
  'clusters',
  'api',
  'events',
  'lifecycle',
  'quota',
  'terminal',
] as const)

// ── Charset ───────────────────────────────────────────────────────────────────

/**
 * Slug charset regex: starts with [a-z0-9], followed by up to 62 chars of [a-z0-9-].
 * Max length 63 (DNS label compatible).
 * No leading underscore (reserved for future internal use).
 */
const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,62}$/

// ── validate ──────────────────────────────────────────────────────────────────

/**
 * Validate a slug string.
 * Returns null on success, or an Error describing the failure.
 */
export function validate(s: string): Error | null {
  if (!s) return new Error('slug is empty')
  if (s.startsWith('_')) return new Error('slug may not start with underscore')
  if (!SLUG_RE.test(s)) return new Error(`slug "${s}" violates charset: must match /^[a-z0-9][a-z0-9-]{0,62}$/`)
  if ((RESERVED_WORDS as readonly string[]).includes(s)) {
    return new Error(`slug "${s}" is a reserved word`)
  }
  return null
}

// ── slugify ───────────────────────────────────────────────────────────────────

/**
 * Convert an arbitrary display name to a valid slug:
 * 1. Lowercase
 * 2. NFD Unicode normalization + strip combining marks
 * 3. Replace runs of non-[a-z0-9] chars with a single hyphen
 * 4. Trim leading/trailing hyphens
 * 5. Truncate to 63 chars
 *
 * Returns an empty string if the result is empty (caller must apply fallback).
 */
export function slugify(displayName: string): string {
  let s = displayName
    .toLowerCase()
    .normalize('NFD')
    .replace(/\p{M}/gu, '')   // strip combining marks
    .replace(/[^a-z0-9]+/g, '-')  // non-slug chars → hyphen
    .replace(/^-+|-+$/g, '')  // trim leading/trailing hyphens
    .slice(0, 63)

  // A truncated string might end with a hyphen after slicing
  s = s.replace(/-+$/, '')

  return s
}

// ── deriveUserSlug ────────────────────────────────────────────────────────────

/**
 * Derive the display-form user slug from a display name and email.
 * Algorithm: `{slugify(name or local-part)}-{domain.split('.')[0]}`
 *
 * No collision handling here — that is server-side.
 *
 * Examples:
 *   deriveUserSlug("Richard Song", "richard@rbc.com")  → "richard-song-rbc"
 *   deriveUserSlug("Alice", "alice@gmail.com")          → "alice-gmail"
 *   deriveUserSlug("", "bob@company.co.uk")             → "bob-company"
 */
export function deriveUserSlug(displayName: string, email: string): string {
  const atIdx = email.indexOf('@')
  const localPart = atIdx >= 0 ? email.slice(0, atIdx) : email
  const domain = atIdx >= 0 ? email.slice(atIdx + 1) : ''
  const domainFirst = domain.split('.')[0] ?? ''

  const nameBase = displayName.trim() ? slugify(displayName) : ''
  const localBase = slugify(localPart)
  const domainSlug = slugify(domainFirst)

  const namepart = nameBase || localBase || 'u'

  if (!domainSlug) return namepart

  // Combine, ensuring total length ≤ 63
  const combined = `${namepart}-${domainSlug}`.slice(0, 63).replace(/-+$/, '')
  return combined
}

// ── Brand constructors (unsafe casts) ─────────────────────────────────────────
// Use these only after validating or trusting the source (e.g., JWT claim, KV key).

/** Cast a validated string to UserSlug. Throws in dev if invalid. */
export function userSlug(s: string): UserSlug {
  if (typeof import.meta !== 'undefined' && import.meta.env?.DEV) {
    const err = validate(s)
    if (err) throw new Error(`userSlug: ${err.message}`)
  }
  return s as UserSlug
}

/** Cast a validated string to ProjectSlug. Throws in dev if invalid. */
export function projectSlug(s: string): ProjectSlug {
  if (typeof import.meta !== 'undefined' && import.meta.env?.DEV) {
    const err = validate(s)
    if (err) throw new Error(`projectSlug: ${err.message}`)
  }
  return s as ProjectSlug
}

/** Cast a validated string to SessionSlug. Throws in dev if invalid. */
export function sessionSlug(s: string): SessionSlug {
  if (typeof import.meta !== 'undefined' && import.meta.env?.DEV) {
    const err = validate(s)
    if (err) throw new Error(`sessionSlug: ${err.message}`)
  }
  return s as SessionSlug
}

/** Cast a validated string to HostSlug. Throws in dev if invalid. */
export function hostSlug(s: string): HostSlug {
  if (typeof import.meta !== 'undefined' && import.meta.env?.DEV) {
    const err = validate(s)
    if (err) throw new Error(`hostSlug: ${err.message}`)
  }
  return s as HostSlug
}

/** Cast a validated string to ClusterSlug. Throws in dev if invalid. */
export function clusterSlug(s: string): ClusterSlug {
  if (typeof import.meta !== 'undefined' && import.meta.env?.DEV) {
    const err = validate(s)
    if (err) throw new Error(`clusterSlug: ${err.message}`)
  }
  return s as ClusterSlug
}
