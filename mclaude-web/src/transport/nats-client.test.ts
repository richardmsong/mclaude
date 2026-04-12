import { describe, it, expect, vi, beforeEach } from 'vitest'
import { MockNATSClient } from '@/testutil/mock-nats'
import { NATSClient } from '@/transport/nats-client'
import type { INATSClient } from '@/types'

// ── Type-level assertions ──────────────────────────────────────────────────
// Both classes must satisfy INATSClient at compile time.
const _natsTypeCheck: INATSClient = new NATSClient()
const _mockTypeCheck: INATSClient = new MockNATSClient()
void _natsTypeCheck
void _mockTypeCheck

// ── NATSClient class structure ─────────────────────────────────────────────
describe('NATSClient class structure', () => {
  it('implements every INATSClient method', () => {
    const client = new NATSClient()
    const methods: Array<keyof INATSClient> = [
      'connect', 'reconnect', 'subscribe', 'publish', 'request',
      'kvWatch', 'kvGet', 'onDisconnect', 'onReconnect', 'isConnected', 'close',
    ]
    for (const m of methods) {
      expect(typeof client[m], `method ${m}`).toBe('function')
    }
  })

  it('starts disconnected before connect', () => {
    expect(new NATSClient().isConnected()).toBe(false)
  })

  it('onDisconnect/onReconnect register and unregister without connection', () => {
    const client = new NATSClient()
    const cb = vi.fn()
    const unsub1 = client.onDisconnect(cb)
    const unsub2 = client.onReconnect(cb)
    unsub1()
    unsub2()
    // no throw
  })

  it('close resolves without error when not connected', async () => {
    await expect(new NATSClient().close()).resolves.toBeUndefined()
  })
})

// ── Interface contract via MockNATSClient ──────────────────────────────────
// The real NATSClient requires a running NATS server. All interface-contract
// tests run against MockNATSClient, which must satisfy the same contract.

describe('INATSClient contract (MockNATSClient)', () => {
  const OPTS = { url: 'ws://localhost:4222', jwt: 'test-jwt', nkeySeed: 'test-seed' }
  let client: MockNATSClient

  beforeEach(async () => {
    client = new MockNATSClient()
    await client.connect(OPTS)
  })

  // ── connect / isConnected / close ──────────────────────────────────────

  it('isConnected returns true after connect', () => {
    expect(client.isConnected()).toBe(true)
  })

  it('isConnected returns false after close', async () => {
    await client.close()
    expect(client.isConnected()).toBe(false)
  })

  // ── subscribe / publish ─────────────────────────────────────────────────

  it('subscribe receives published message on exact subject', () => {
    const msgs: string[] = []
    client.subscribe('a.b.c', msg => msgs.push(new TextDecoder().decode(msg.data)))
    client.publish('a.b.c', new TextEncoder().encode('hello'))
    expect(msgs).toEqual(['hello'])
  })

  it('subscribe returns unsubscribe function that stops delivery', () => {
    const msgs: string[] = []
    const unsub = client.subscribe('a.b', msg => msgs.push(new TextDecoder().decode(msg.data)))
    unsub()
    client.publish('a.b', new TextEncoder().encode('after-unsub'))
    expect(msgs).toEqual([])
  })

  it('wildcard > subscribe matches nested subjects', () => {
    const subjects: string[] = []
    client.subscribe('events.>', msg => subjects.push(msg.subject))
    client.publish('events.foo', new Uint8Array())
    client.publish('events.bar.baz', new Uint8Array())
    expect(subjects).toEqual(['events.foo', 'events.bar.baz'])
  })

  it('publish records message with headers', () => {
    client.publish('x', new TextEncoder().encode('d'), { 'X-Trace': 'abc' })
    expect(client.published[0].headers).toEqual({ 'X-Trace': 'abc' })
  })

  // ── kvGet / kvWatch ─────────────────────────────────────────────────────

  it('kvGet returns null for missing key', async () => {
    expect(await client.kvGet('bucket', 'missing')).toBeNull()
  })

  it('kvGet returns entry after kvSet', async () => {
    client.kvSet('bucket', 'mykey', { x: 1 })
    const entry = await client.kvGet('bucket', 'mykey')
    expect(entry).not.toBeNull()
    expect(entry!.key).toBe('mykey')
    expect(JSON.parse(new TextDecoder().decode(entry!.value))).toEqual({ x: 1 })
  })

  it('kvWatch fires immediately with existing values', () => {
    client.kvSet('b', 'k', { v: 42 })
    const keys: string[] = []
    client.kvWatch('b', 'k', e => keys.push(e.key))
    expect(keys).toEqual(['k'])
  })

  it('kvWatch fires on subsequent kvSet', () => {
    const revisions: number[] = []
    client.kvWatch('b', 'k', e => revisions.push(e.revision))
    client.kvSet('b', 'k', { v: 1 })
    client.kvSet('b', 'k', { v: 2 })
    expect(revisions).toEqual([1, 2])
  })

  it('kvWatch unsubscribe stops callbacks', () => {
    const keys: string[] = []
    const unsub = client.kvWatch('b', 'k', e => keys.push(e.key))
    unsub()
    client.kvSet('b', 'k', { v: 1 })
    expect(keys).toEqual([])
  })

  it('kvWatch wildcard > fires for all keys in bucket', () => {
    const keys: string[] = []
    client.kvWatch('b', '>', e => keys.push(e.key))
    client.kvSet('b', 'key1', {})
    client.kvSet('b', 'key2', {})
    expect(keys).toEqual(['key1', 'key2'])
  })

  // ── disconnect / reconnect lifecycle ────────────────────────────────────

  it('onDisconnect fires when simulateDisconnect called', () => {
    const cb = vi.fn()
    client.onDisconnect(cb)
    client.simulateDisconnect()
    expect(cb).toHaveBeenCalledOnce()
    expect(client.isConnected()).toBe(false)
  })

  it('onReconnect fires when simulateReconnect called', () => {
    const cb = vi.fn()
    client.onReconnect(cb)
    client.simulateDisconnect()
    client.simulateReconnect()
    expect(cb).toHaveBeenCalledOnce()
    expect(client.isConnected()).toBe(true)
  })

  it('onDisconnect unsubscribe stops the callback', () => {
    const cb = vi.fn()
    const unsub = client.onDisconnect(cb)
    unsub()
    client.simulateDisconnect()
    expect(cb).not.toHaveBeenCalled()
  })

  it('reconnect fires disconnect then reconnect', async () => {
    const order: string[] = []
    client.onDisconnect(() => order.push('disconnect'))
    client.onReconnect(() => order.push('reconnect'))
    await client.reconnect('new-jwt')
    expect(order).toEqual(['disconnect', 'reconnect'])
    expect(client.isConnected()).toBe(true)
  })

  // ── request ─────────────────────────────────────────────────────────────

  it('request records to requests list', async () => {
    const data = new TextEncoder().encode('payload')
    await client.request('rpc.subject', data)
    expect(client.requests[0].subject).toBe('rpc.subject')
  })

  it('request uses registered handler for reply', async () => {
    client.requestHandlers.set('rpc.add', _data => new TextEncoder().encode('42'))
    const reply = await client.request('rpc.add', new TextEncoder().encode('input'))
    expect(new TextDecoder().decode(reply.data)).toBe('42')
  })
})
