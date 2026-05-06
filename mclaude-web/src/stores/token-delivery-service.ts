/**
 * ADR-0092: Token delivery service.
 *
 * Handles:
 *  - Delivering a token to a specific host's user-agent via NATS request/reply
 *  - Re-delivery to all accepted-grant hosts (on reconnect or token refresh)
 *  - Stale token detection on login hydration
 *  - Pending delivery retry from localStorage
 *  - Token revoke (per-host and all-hosts)
 *
 * The actual KV read/write is owned by the caller (App.tsx), which passes
 * the token string. This service only handles the NATS delivery mechanism
 * and localStorage pending-delivery bookkeeping.
 */

import type { INATSClient } from '@/types'
import type { AuthClient } from '@/transport/auth-client'
import { sealToken, computeTokenVersion, addPendingDelivery, removePendingDelivery, getPendingDeliveries } from '@/lib/token-delivery'

const TOKEN_DELIVERY_TIMEOUT_MS = 30_000   // first-run spinner timeout
const TOKEN_REVOKE_TIMEOUT_MS   = 10_000   // token-revoke request/reply
const AGENT_STATUS_TIMEOUT_MS   =  5_000   // agent.status request/reply

export type DeliveryResult =
  | { ok: true }
  | { ok: false; error: string }

export type MultiDeliveryResult = {
  succeeded: number
  total: number
  /** hslug values that failed (user-agent offline, timeout, etc.) */
  failed: string[]
}

/** Deliver a sealed token to a single host's user-agent. */
export async function deliverToken(
  nc: INATSClient,
  authClient: AuthClient,
  uslug: string,
  hslug: string,
  token: string,
  provider = 'anthropic',
  timeoutMs = TOKEN_DELIVERY_TIMEOUT_MS,
): Promise<DeliveryResult> {
  try {
    const pubkeyB64 = await authClient.getUserAgentPubkey(uslug, hslug)
    const tokenWrapped = await sealToken(token, pubkeyB64)
    const payload = JSON.stringify({ provider, token_wrapped: tokenWrapped })
    const subject = `mclaude.users.${uslug}.hosts.${hslug}.agent.token-delivery`
    await nc.request(subject, new TextEncoder().encode(payload), timeoutMs)
    removePendingDelivery(hslug, provider)
    return { ok: true }
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) }
  }
}

/**
 * Deliver token to all accepted-grant hosts.
 * Hosts that fail are recorded in localStorage as pending deliveries.
 */
export async function deliverTokenToAllHosts(
  nc: INATSClient,
  authClient: AuthClient,
  uslug: string,
  token: string,
  provider = 'anthropic',
): Promise<MultiDeliveryResult> {
  // Fetch all grants; filter to accepted client-side
  const grants = await authClient.getGrants(uslug)
  const accepted = grants.filter(g => g.status === 'accepted')

  const results = await Promise.allSettled(
    accepted.map(async g => {
      const result = await deliverToken(nc, authClient, uslug, g.hostSlug, token, provider)
      if (!result.ok) {
        addPendingDelivery(g.hostSlug, provider)
        return { hslug: g.hostSlug, ok: false }
      }
      return { hslug: g.hostSlug, ok: true }
    })
  )

  let succeeded = 0
  const failed: string[] = []
  for (const r of results) {
    if (r.status === 'fulfilled' && r.value.ok) {
      succeeded++
    } else {
      const hslug = r.status === 'fulfilled' ? r.value.hslug : 'unknown'
      failed.push(hslug)
    }
  }

  return { succeeded, total: accepted.length, failed }
}

/**
 * Revoke token on a single host's user-agent.
 * Subject: mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke
 */
export async function revokeTokenOnHost(
  nc: INATSClient,
  uslug: string,
  hslug: string,
  provider = 'anthropic',
): Promise<DeliveryResult> {
  try {
    const subject = `mclaude.users.${uslug}.hosts.${hslug}.agent.token-revoke`
    const payload = JSON.stringify({ provider })
    const reply = await nc.request(subject, new TextEncoder().encode(payload), TOKEN_REVOKE_TIMEOUT_MS)
    const data = JSON.parse(new TextDecoder().decode(reply.data)) as { ok: boolean; error?: string }
    if (data.ok) return { ok: true }
    return { ok: false, error: data.error ?? 'Revoke failed' }
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) }
  }
}

/**
 * Revoke token on all accepted-grant hosts.
 */
export async function revokeTokenOnAllHosts(
  nc: INATSClient,
  authClient: AuthClient,
  uslug: string,
  provider = 'anthropic',
): Promise<MultiDeliveryResult> {
  const grants = await authClient.getGrants(uslug)
  const accepted = grants.filter(g => g.status === 'accepted')

  const results = await Promise.allSettled(
    accepted.map(async g => {
      const result = await revokeTokenOnHost(nc, uslug, g.hostSlug, provider)
      return { hslug: g.hostSlug, ok: result.ok }
    })
  )

  let succeeded = 0
  const failed: string[] = []
  for (const r of results) {
    if (r.status === 'fulfilled' && r.value.ok) {
      succeeded++
    } else {
      const hslug = r.status === 'fulfilled' ? r.value.hslug : 'unknown'
      failed.push(hslug)
    }
  }

  return { succeeded, total: accepted.length, failed }
}

/**
 * Query a host's user-agent for its token status.
 * Subject: mclaude.users.{uslug}.hosts.{hslug}.agent.status
 * Returns null if the host is offline or times out.
 */
export async function getAgentTokenStatus(
  nc: INATSClient,
  uslug: string,
  hslug: string,
): Promise<{ hasToken: boolean; tokenVersion: string } | null> {
  try {
    const subject = `mclaude.users.${uslug}.hosts.${hslug}.agent.status`
    const reply = await nc.request(subject, new TextEncoder().encode('{}'), AGENT_STATUS_TIMEOUT_MS)
    return JSON.parse(new TextDecoder().decode(reply.data)) as { hasToken: boolean; tokenVersion: string }
  } catch {
    return null
  }
}

/**
 * On login hydration: check each accepted-grant host for stale tokens.
 * If a host's tokenVersion doesn't match the current KV token, re-deliver.
 * Called from App.tsx after login.
 */
export async function checkAndRedeliverStaleTokens(
  nc: INATSClient,
  authClient: AuthClient,
  uslug: string,
  token: string,
  provider = 'anthropic',
): Promise<void> {
  const grants = await authClient.getGrants(uslug)
  const accepted = grants.filter(g => g.status === 'accepted')
  if (accepted.length === 0) return

  const currentVersion = await computeTokenVersion(token)

  await Promise.allSettled(
    accepted.map(async g => {
      const status = await getAgentTokenStatus(nc, uslug, g.hostSlug)
      if (!status) return // offline — will be picked up by pending delivery
      if (status.tokenVersion === currentVersion) return // up to date
      // Stale — re-deliver
      await deliverToken(nc, authClient, uslug, g.hostSlug, token, provider)
    })
  )
}

/**
 * Retry pending deliveries from localStorage.
 * Called on login and on NATS reconnect.
 */
export async function retryPendingDeliveries(
  nc: INATSClient,
  authClient: AuthClient,
  uslug: string,
  token: string,
): Promise<void> {
  const pending = getPendingDeliveries()
  if (pending.length === 0) return

  await Promise.allSettled(
    pending.map(d => deliverToken(nc, authClient, uslug, d.hslug, token, d.provider))
  )
}
