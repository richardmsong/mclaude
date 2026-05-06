/**
 * ADR-0091 / ADR-0092: User info store.
 *
 * Watches the `mclaude-user-info-{uslug}` KV bucket for the user's Anthropic
 * token and other per-user settings. The bucket is client-only (only SPA/CLI
 * can read/write it).
 *
 * On login hydration: call startWatching(). The store fires onTokenChanged()
 * whenever tokens.anthropic changes (including first appearance).
 *
 * The token is written to KV by ConnectSheet after OAuth/paste completes.
 */

import type { INATSClient } from '@/types'
import { logger } from '@/logger'

const log = logger.child({ component: 'user-info-store' })

type TokenChangedCallback = (token: string | null) => void

export class UserInfoStore {
  private _token: string | null = null
  private _callbacks: TokenChangedCallback[] = []
  private _stopWatch: (() => void) | null = null

  constructor(
    private readonly nc: INATSClient,
    private readonly uslug: string,
  ) {}

  get token(): string | null { return this._token }

  get bucketName(): string { return `mclaude-user-info-${this.uslug}` }

  /** Start watching the user-info KV bucket for token changes. */
  startWatching(): void {
    if (this._stopWatch) return // already watching

    const stop = this.nc.kvWatch(
      this.bucketName,
      'tokens.anthropic',
      (entry) => {
        if (entry.operation === 'DEL' || entry.operation === 'PURGE') {
          this._setToken(null)
        } else {
          const value = new TextDecoder().decode(entry.value).trim()
          this._setToken(value || null)
        }
      },
    )
    this._stopWatch = stop
    log.info({ uslug: this.uslug }, 'watching user-info KV')
  }

  stopWatching(): void {
    this._stopWatch?.()
    this._stopWatch = null
  }

  /** Write the Anthropic token to KV (used by ConnectSheet after OAuth/paste). */
  async writeToken(token: string): Promise<void> {
    // KV put: publish to $KV.{bucket}.{key}
    const subject = `$KV.${this.bucketName}.tokens.anthropic`
    const encoded = new TextEncoder().encode(token)
    this.nc.publish(subject, encoded)
    // Optimistically update local state — the KV watch will confirm
    this._setToken(token)
  }

  /** Delete the Anthropic token from KV (used by Disconnect). */
  deleteToken(): void {
    // KV delete: publish empty to $KV.{bucket}.{key} with KV-Operation: DEL header
    const subject = `$KV.${this.bucketName}.tokens.anthropic`
    this.nc.publish(subject, new Uint8Array(0), { 'KV-Operation': 'DEL' })
    this._setToken(null)
  }

  /** Subscribe to token changes. Returns unsubscribe function. */
  onTokenChanged(cb: TokenChangedCallback): () => void {
    this._callbacks.push(cb)
    return () => {
      const idx = this._callbacks.indexOf(cb)
      if (idx >= 0) this._callbacks.splice(idx, 1)
    }
  }

  private _setToken(token: string | null): void {
    if (this._token === token) return
    this._token = token
    log.info({ uslug: this.uslug, hasToken: token !== null }, 'anthropic token changed')
    for (const cb of this._callbacks) cb(token)
  }
}
