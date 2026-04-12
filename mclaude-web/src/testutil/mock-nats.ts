import type { INATSClient, NATSConnectionOptions, NATSMessage, KVEntry } from '@/types'

type SubCallback = (msg: NATSMessage) => void
type KVCallback = (entry: KVEntry) => void

export class MockNATSClient implements INATSClient {
  private _connected = false
  private _subs = new Map<string, SubCallback[]>()
  private _kvStore = new Map<string, Map<string, { value: Uint8Array; revision: number }>>()
  private _kvWatchers = new Map<string, Array<{ pattern: string; callback: KVCallback }>>()
  private _disconnectListeners: Array<() => void> = []
  private _reconnectListeners: Array<() => void> = []
  private _seqCounter = 0

  // Published messages are recorded for assertions
  readonly published: Array<{ subject: string; data: Uint8Array; headers?: Record<string, string> }> = []
  readonly requests: Array<{ subject: string; data: Uint8Array }> = []

  // Request handlers — set in tests to return canned replies
  requestHandlers = new Map<string, (data: Uint8Array) => Uint8Array>()

  async connect(_opts: NATSConnectionOptions): Promise<void> {
    this._connected = true
  }

  async reconnect(_newJwt: string): Promise<void> {
    // Simulate disconnect/reconnect cycle for testing
    this._connected = false
    for (const l of this._disconnectListeners) l()
    this._connected = true
    for (const l of this._reconnectListeners) l()
  }

  subscribe(subject: string, callback: SubCallback): () => void {
    // Support wildcard > at end
    const existing = this._subs.get(subject) ?? []
    existing.push(callback)
    this._subs.set(subject, existing)
    return () => {
      const cbs = this._subs.get(subject) ?? []
      this._subs.set(subject, cbs.filter(c => c !== callback))
    }
  }

  publish(subject: string, data: Uint8Array, headers?: Record<string, string>): void {
    this.published.push({ subject, data, headers })
    // Deliver to matching subscribers
    for (const [pattern, cbs] of this._subs) {
      if (this._matchSubject(pattern, subject)) {
        const msg: NATSMessage = { subject, data, headers, seq: ++this._seqCounter }
        for (const cb of cbs) cb(msg)
      }
    }
  }

  // Simulate receiving a message from the server (e.g., from session agent)
  simulateReceive(subject: string, data: unknown, seq?: number): void {
    const encoded = new TextEncoder().encode(JSON.stringify(data))
    const msg: NATSMessage = { subject, data: encoded, seq: seq ?? ++this._seqCounter }
    for (const [pattern, cbs] of this._subs) {
      if (this._matchSubject(pattern, subject)) {
        for (const cb of cbs) cb(msg)
      }
    }
  }

  async request(subject: string, data: Uint8Array, _timeoutMs?: number): Promise<NATSMessage> {
    this.requests.push({ subject, data })
    const handler = this.requestHandlers.get(subject)
    if (handler) {
      return { subject, data: handler(data), seq: ++this._seqCounter }
    }
    return { subject, data: new TextEncoder().encode('{}'), seq: ++this._seqCounter }
  }

  kvWatch(bucket: string, key: string, callback: KVCallback): () => void {
    const watchersForBucket = this._kvWatchers.get(bucket) ?? []
    watchersForBucket.push({ pattern: key, callback })
    this._kvWatchers.set(bucket, watchersForBucket)

    // Immediately deliver existing values matching the pattern
    const bucketStore = this._kvStore.get(bucket)
    if (bucketStore) {
      for (const [k, v] of bucketStore) {
        if (this._matchKVKey(key, k)) {
          callback({ key: k, value: v.value, revision: v.revision })
        }
      }
    }

    return () => {
      const watchers = this._kvWatchers.get(bucket) ?? []
      this._kvWatchers.set(bucket, watchers.filter(w => w.callback !== callback))
    }
  }

  async kvGet(bucket: string, key: string): Promise<KVEntry | null> {
    const bucketStore = this._kvStore.get(bucket)
    if (!bucketStore) return null
    const entry = bucketStore.get(key)
    if (!entry) return null
    return { key, value: entry.value, revision: entry.revision }
  }

  // Test helper: set a KV value and notify watchers
  kvSet(bucket: string, key: string, value: unknown): void {
    if (!this._kvStore.has(bucket)) this._kvStore.set(bucket, new Map())
    const bucketStore = this._kvStore.get(bucket)!
    const existing = bucketStore.get(key)
    const revision = (existing?.revision ?? 0) + 1
    const encoded = new TextEncoder().encode(JSON.stringify(value))
    bucketStore.set(key, { value: encoded, revision })

    const watchers = this._kvWatchers.get(bucket) ?? []
    const entry: KVEntry = { key, value: encoded, revision }
    for (const w of watchers) {
      if (this._matchKVKey(w.pattern, key)) {
        w.callback(entry)
      }
    }
  }

  // Test helper: simulate NATS disconnect
  simulateDisconnect(): void {
    this._connected = false
    for (const l of this._disconnectListeners) l()
  }

  // Test helper: simulate NATS reconnect
  simulateReconnect(): void {
    this._connected = true
    for (const l of this._reconnectListeners) l()
  }

  onDisconnect(callback: () => void): () => void {
    this._disconnectListeners.push(callback)
    return () => { this._disconnectListeners = this._disconnectListeners.filter(l => l !== callback) }
  }

  onReconnect(callback: () => void): () => void {
    this._reconnectListeners.push(callback)
    return () => { this._reconnectListeners = this._reconnectListeners.filter(l => l !== callback) }
  }

  isConnected(): boolean {
    return this._connected
  }

  async close(): Promise<void> {
    this._connected = false
  }

  // Clear recorded calls (for assertions between test phases)
  clearRecorded(): void {
    this.published.length = 0
    this.requests.length = 0
  }

  private _matchSubject(pattern: string, subject: string): boolean {
    if (pattern === subject) return true
    if (pattern.endsWith('>')) {
      const prefix = pattern.slice(0, -1)
      return subject.startsWith(prefix)
    }
    if (pattern.endsWith('*')) {
      const parts = pattern.split('.')
      const subParts = subject.split('.')
      if (parts.length !== subParts.length) return false
      return parts.every((p, i) => p === '*' || p === subParts[i])
    }
    return false
  }

  private _matchKVKey(pattern: string, key: string): boolean {
    if (pattern === key) return true
    if (pattern.endsWith('/>')) {
      const prefix = pattern.slice(0, -1) // Remove '>'
      return key.startsWith(prefix)
    }
    if (pattern.endsWith('>')) {
      const prefix = pattern.slice(0, -1)
      return key.startsWith(prefix)
    }
    return false
  }
}
