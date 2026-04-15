import { connect, jwtAuthenticator, headers as natsHeaders, Events, consumerOpts } from 'nats.ws'
import type { NatsConnection, KvEntry as NatsKvEntry } from 'nats.ws'
import type { INATSClient, NATSConnectionOptions, NATSMessage, KVEntry } from '@/types'

// Real NATSClient using nats.ws (WebSocket transport for browser).
// For testing, use MockNATSClient from testutil/mock-nats.ts instead.
export class NATSClient implements INATSClient {
  private _nc: NatsConnection | null = null
  private _opts: NATSConnectionOptions | null = null
  private _connected = false
  private _reconnecting = false
  private _disconnectListeners: Array<() => void> = []
  private _reconnectListeners: Array<() => void> = []

  async connect(opts: NATSConnectionOptions): Promise<void> {
    this._opts = opts
    const seed = new TextEncoder().encode(opts.nkeySeed)
    const nc = await connect({
      servers: [opts.url],
      authenticator: jwtAuthenticator(opts.jwt, seed),
    })
    this._nc = nc
    this._connected = true
    this._watchStatus(nc)
  }

  private _watchStatus(nc: NatsConnection): void {
    ;(async () => {
      for await (const s of nc.status()) {
        if (this._reconnecting) continue
        if (s.type === Events.Disconnect) {
          this._connected = false
          for (const l of this._disconnectListeners) l()
        } else if (s.type === Events.Reconnect) {
          this._connected = true
          for (const l of this._reconnectListeners) l()
        }
      }
    })()
  }

  async reconnect(newJwt: string): Promise<void> {
    if (!this._opts) throw new Error('NATSClient: not connected')
    this._reconnecting = true
    try {
      this._connected = false
      for (const l of this._disconnectListeners) l()
      const old = this._nc
      this._nc = null
      await old?.close()
      await this.connect({ ...this._opts, jwt: newJwt })
      for (const l of this._reconnectListeners) l()
    } finally {
      this._reconnecting = false
    }
  }

  subscribe(subject: string, callback: (msg: NATSMessage) => void): () => void {
    const nc = this._nc
    if (!nc) throw new Error('NATSClient: not connected')
    const sub = nc.subscribe(subject)
    ;(async () => {
      for await (const msg of sub) {
        const hdrs: Record<string, string> = {}
        if (msg.headers) {
          for (const [k, v] of msg.headers) {
            hdrs[k] = v.join(',')
          }
        }
        callback({
          subject: msg.subject,
          data: msg.data,
          headers: Object.keys(hdrs).length > 0 ? hdrs : undefined,
          reply: msg.reply,
        })
      }
    })()
    return () => sub.unsubscribe()
  }

  async jsSubscribe(
    _stream: string,
    subject: string,
    startSeq: number,
    callback: (msg: NATSMessage) => void,
  ): Promise<() => void> {
    const nc = this._nc
    if (!nc) throw new Error('NATSClient: not connected')
    const js = nc.jetstream()
    const opts = consumerOpts()
    opts.orderedConsumer()
    opts.filterSubject(subject)
    if (startSeq > 0) {
      opts.startSequence(startSeq)
    } else {
      opts.deliverAll()
    }
    const sub = await js.subscribe(subject, opts)
    let stopped = false
    ;(async () => {
      try {
        for await (const m of sub) {
          if (stopped) break
          callback({ subject: m.subject, data: m.data, seq: m.seq })
          m.ack()
        }
      } catch (_err) {
        // Expected when subscription is stopped or connection closes
      }
    })()
    return () => {
      stopped = true
      sub.unsubscribe()
    }
  }

  publish(subject: string, data: Uint8Array, headerMap?: Record<string, string>): void {
    const nc = this._nc
    if (!nc) throw new Error('NATSClient: not connected')
    if (headerMap && Object.keys(headerMap).length > 0) {
      const h = natsHeaders()
      for (const [k, v] of Object.entries(headerMap)) {
        h.set(k, v)
      }
      nc.publish(subject, data, { headers: h })
    } else {
      nc.publish(subject, data)
    }
  }

  async request(subject: string, data: Uint8Array, timeoutMs?: number): Promise<NATSMessage> {
    const nc = this._nc
    if (!nc) throw new Error('NATSClient: not connected')
    const msg = await nc.request(subject, data, { timeout: timeoutMs ?? 5000 })
    return { subject: msg.subject, data: msg.data }
  }

  kvWatch(bucket: string, key: string, callback: (entry: KVEntry) => void): () => void {
    let watcher: NatsKvEntry | null = null
    let stopped = false

    ;(async () => {
      try {
        const nc = this._nc
        if (!nc) return
        const js = nc.jetstream()
        const kv = await js.views.kv(bucket)
        const iter = await kv.watch({ key })
        // Store stop reference via duck typing since QueuedIterator has stop()
        const stoppable = iter as unknown as { stop: () => void }
        if (stopped) {
          stoppable.stop()
          return
        }
        watcher = stoppable as unknown as NatsKvEntry
        for await (const entry of iter) {
          if (stopped) break
          callback({
            key: entry.key,
            value: entry.value,
            revision: entry.revision,
            operation: entry.operation as 'PUT' | 'DEL' | 'PURGE',
          })
        }
      } catch (_err) {
        // Expected when connection closes or watcher is stopped
      }
    })()

    return () => {
      stopped = true
      if (watcher) {
        (watcher as unknown as { stop: () => void }).stop()
      }
    }
  }

  async kvGet(bucket: string, key: string): Promise<KVEntry | null> {
    const nc = this._nc
    if (!nc) throw new Error('NATSClient: not connected')
    const js = nc.jetstream()
    const kv = await js.views.kv(bucket)
    const entry = await kv.get(key)
    if (!entry) return null
    return {
      key: entry.key,
      value: entry.value,
      revision: entry.revision,
    }
  }

  onDisconnect(callback: () => void): () => void {
    this._disconnectListeners.push(callback)
    return () => {
      this._disconnectListeners = this._disconnectListeners.filter(l => l !== callback)
    }
  }

  onReconnect(callback: () => void): () => void {
    this._reconnectListeners.push(callback)
    return () => {
      this._reconnectListeners = this._reconnectListeners.filter(l => l !== callback)
    }
  }

  isConnected(): boolean {
    return this._connected
  }

  async close(): Promise<void> {
    this._connected = false
    const nc = this._nc
    this._nc = null
    await nc?.close()
  }
}
