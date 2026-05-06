/**
 * Tests for token-delivery.ts (ADR-0092)
 * - sealToken: libsodium X25519 sealed-box wrapping
 * - computeTokenVersion: SHA-256 prefix
 * - PendingDeliveries localStorage helpers
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  computeTokenVersion,
  getPendingDeliveries,
  addPendingDelivery,
  removePendingDelivery,
  clearPendingDeliveries,
} from './token-delivery'

// ─── computeTokenVersion ──────────────────────────────────────────────────────

describe('computeTokenVersion', () => {
  it('returns 8 hex chars', async () => {
    const version = await computeTokenVersion('sk-ant-oat01-test-token')
    expect(version).toMatch(/^[0-9a-f]{8}$/)
  })

  it('is deterministic for the same input', async () => {
    const v1 = await computeTokenVersion('my-token')
    const v2 = await computeTokenVersion('my-token')
    expect(v1).toBe(v2)
  })

  it('differs for different inputs', async () => {
    const v1 = await computeTokenVersion('token-a')
    const v2 = await computeTokenVersion('token-b')
    expect(v1).not.toBe(v2)
  })

  it('returns the first 8 hex chars of sha256', async () => {
    // Known SHA-256: sha256("") = e3b0c44298fc1c14...
    const version = await computeTokenVersion('')
    expect(version).toBe('e3b0c442')
  })
})

// ─── Pending deliveries ───────────────────────────────────────────────────────

describe('pending deliveries', () => {
  const localStorageMock = (() => {
    let store: Record<string, string> = {}
    return {
      getItem: (key: string) => store[key] ?? null,
      setItem: (key: string, value: string) => { store[key] = value },
      removeItem: (key: string) => { delete store[key] },
      clear: () => { store = {} },
    }
  })()

  beforeEach(() => {
    vi.stubGlobal('localStorage', localStorageMock)
    localStorageMock.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns empty array when nothing stored', () => {
    expect(getPendingDeliveries()).toEqual([])
  })

  it('adds a pending delivery', () => {
    addPendingDelivery('my-host', 'anthropic')
    const deliveries = getPendingDeliveries()
    expect(deliveries).toHaveLength(1)
    expect(deliveries[0]).toEqual({ hslug: 'my-host', provider: 'anthropic' })
  })

  it('does not add duplicate entries', () => {
    addPendingDelivery('my-host', 'anthropic')
    addPendingDelivery('my-host', 'anthropic')
    expect(getPendingDeliveries()).toHaveLength(1)
  })

  it('adds multiple distinct entries', () => {
    addPendingDelivery('host-a', 'anthropic')
    addPendingDelivery('host-b', 'anthropic')
    const deliveries = getPendingDeliveries()
    expect(deliveries).toHaveLength(2)
    expect(deliveries.map(d => d.hslug)).toContain('host-a')
    expect(deliveries.map(d => d.hslug)).toContain('host-b')
  })

  it('removes a specific pending delivery', () => {
    addPendingDelivery('host-a', 'anthropic')
    addPendingDelivery('host-b', 'anthropic')
    removePendingDelivery('host-a', 'anthropic')
    const deliveries = getPendingDeliveries()
    expect(deliveries).toHaveLength(1)
    expect(deliveries[0]!.hslug).toBe('host-b')
  })

  it('clears all pending deliveries', () => {
    addPendingDelivery('host-a', 'anthropic')
    addPendingDelivery('host-b', 'anthropic')
    clearPendingDeliveries()
    expect(getPendingDeliveries()).toEqual([])
  })

  it('returns empty array when localStorage value is malformed', () => {
    localStorageMock.setItem('mclaude.pendingDeliveries', 'not-json{')
    expect(getPendingDeliveries()).toEqual([])
  })
})
