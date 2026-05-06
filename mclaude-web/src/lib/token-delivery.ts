/**
 * ADR-0092 / ADR-0091: Token delivery helpers.
 *
 * Wraps a plaintext token with the user-agent's X25519 public key using
 * libsodium's crypto_box_seal (NaCl anonymous sealed box). The wrapped value
 * is base64-encoded for safe transmission over NATS.
 *
 * Also provides helpers for:
 *  - Computing tokenVersion (first 8 hex chars of sha256(token))
 *  - Managing pending-delivery localStorage state
 */

import _sodium from 'libsodium-wrappers'

// ─── Sealed-box wrapping ─────────────────────────────────────────────────────

/**
 * Wrap a plaintext token using the user-agent's X25519 public key.
 * Returns a base64-encoded sealed box.
 *
 * @param token     Plaintext token string (e.g. "sk-ant-oat01-...")
 * @param pubkeyB64 Base64-encoded 32-byte X25519 public key from CP API
 */
export async function sealToken(token: string, pubkeyB64: string): Promise<string> {
  await _sodium.ready
  const sodium = _sodium

  const pubkeyBytes = sodium.from_base64(pubkeyB64, sodium.base64_variants.ORIGINAL)
  const tokenBytes = sodium.from_string(token)
  const sealed = sodium.crypto_box_seal(tokenBytes, pubkeyBytes)
  return sodium.to_base64(sealed, sodium.base64_variants.ORIGINAL)
}

// ─── Token version ───────────────────────────────────────────────────────────

/**
 * Compute the tokenVersion: first 8 hex chars of SHA-256(token).
 * Used to detect stale tokens on hosts without transmitting the token itself.
 */
export async function computeTokenVersion(token: string): Promise<string> {
  const encoded = new TextEncoder().encode(token)
  const hashBuffer = await crypto.subtle.digest('SHA-256', encoded)
  const hashArray = Array.from(new Uint8Array(hashBuffer))
  const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('')
  return hashHex.slice(0, 8)
}

// ─── Pending deliveries (localStorage) ──────────────────────────────────────

const PENDING_DELIVERIES_KEY = 'mclaude.pendingDeliveries'

export interface PendingDelivery {
  hslug: string
  provider: string
}

/** Read pending deliveries from localStorage. Returns empty array on failure. */
export function getPendingDeliveries(): PendingDelivery[] {
  try {
    const raw = localStorage.getItem(PENDING_DELIVERIES_KEY)
    if (!raw) return []
    return JSON.parse(raw) as PendingDelivery[]
  } catch {
    return []
  }
}

/** Add a pending delivery entry to localStorage. */
export function addPendingDelivery(hslug: string, provider: string): void {
  try {
    const current = getPendingDeliveries()
    // Avoid duplicates
    if (!current.some(d => d.hslug === hslug && d.provider === provider)) {
      current.push({ hslug, provider })
      localStorage.setItem(PENDING_DELIVERIES_KEY, JSON.stringify(current))
    }
  } catch {
    // localStorage unavailable — ignore
  }
}

/** Remove a pending delivery entry on success. */
export function removePendingDelivery(hslug: string, provider: string): void {
  try {
    const current = getPendingDeliveries()
    const updated = current.filter(d => !(d.hslug === hslug && d.provider === provider))
    localStorage.setItem(PENDING_DELIVERIES_KEY, JSON.stringify(updated))
  } catch {
    // ignore
  }
}

/** Clear all pending deliveries (called on logout). */
export function clearPendingDeliveries(): void {
  try {
    localStorage.removeItem(PENDING_DELIVERIES_KEY)
  } catch {
    // ignore
  }
}
