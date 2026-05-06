/**
 * ADR-0092: First-run token gate.
 *
 * After login hydration, if tokens.anthropic is absent in KV → render the
 * Connect sheet (auto-opened, [✕] disabled). After connect → auto-deliver
 * K_wrapped to the default platform → proceed to existing first-run flow.
 *
 * Delivery timeout: 30s spinner → "Your host isn't responding" + [Try again].
 * Connect sheet remains open behind the error. First-run flow (project +
 * session creation) does not proceed until delivery succeeds.
 */

import { useState, useEffect, useRef } from 'react'
import { ConnectSheet } from './ConnectSheet'
import type { INATSClient } from '@/types'
import type { UserInfoStore } from '@/stores/user-info-store'
import type { AuthClient } from '@/transport/auth-client'
import { deliverToken } from '@/stores/token-delivery-service'
import { addPendingDelivery } from '@/lib/token-delivery'

const DELIVERY_TIMEOUT_MS = 30_000

type GateState = 'connecting' | 'delivering' | 'timeout' | 'done'

interface TokenGateProps {
  nc: INATSClient
  userInfoStore: UserInfoStore
  authClient: AuthClient
  uslug: string
  /** Default platform host slug — delivery target. If absent, gate skips delivery. */
  defaultHostSlug?: string
  /** Called when the gate is satisfied (token exists + delivered, or no default host). */
  onReady: () => void
}

export function TokenGate({ nc, userInfoStore, authClient, uslug, defaultHostSlug, onReady }: TokenGateProps) {
  const [gateState, setGateState] = useState<GateState>('connecting')
  const [showSheet, setShowSheet] = useState(true)
  const deliveryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const clearDeliveryTimer = () => {
    if (deliveryTimerRef.current) {
      clearTimeout(deliveryTimerRef.current)
      deliveryTimerRef.current = null
    }
  }

  const startDelivery = async (token: string) => {
    if (!defaultHostSlug) {
      // No default host — skip delivery, proceed
      onReady()
      return
    }
    setGateState('delivering')
    clearDeliveryTimer()

    deliveryTimerRef.current = setTimeout(() => {
      setGateState('timeout')
      addPendingDelivery(defaultHostSlug, 'anthropic')
    }, DELIVERY_TIMEOUT_MS)

    const result = await deliverToken(nc, authClient, uslug, defaultHostSlug, token)
    clearDeliveryTimer()

    if (result.ok) {
      setGateState('done')
      onReady()
    } else {
      setGateState('timeout')
      addPendingDelivery(defaultHostSlug, 'anthropic')
    }
  }

  const handleTokenObtained = async (token: string) => {
    // Write token to KV
    userInfoStore.writeToken(token)
    // Start delivery to default platform
    await startDelivery(token)
  }

  const handleTryAgain = () => {
    const token = userInfoStore.token
    if (!token) return
    void startDelivery(token)
  }

  // Clean up timer on unmount
  useEffect(() => () => clearDeliveryTimer(), [])

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 8000,
      background: 'var(--bg)',
      display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
    }}>
      {/* Dimmed background content */}
      <div style={{ color: 'var(--text3)', fontSize: 14, textAlign: 'center', padding: '0 20px' }}>
        {gateState === 'delivering' && (
          <div>
            <div style={{ fontSize: 24, marginBottom: 12 }}>⟳</div>
            <div>Setting up your environment…</div>
          </div>
        )}
        {gateState === 'timeout' && (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 }}>
            <div style={{ fontSize: 15, color: 'var(--text)', fontWeight: 500 }}>
              Your host isn&#39;t responding.
            </div>
            <button
              onClick={handleTryAgain}
              style={{
                padding: '10px 24px',
                background: 'var(--blue)',
                color: '#fff',
                border: 'none',
                borderRadius: 10,
                fontSize: 14,
                fontWeight: 600,
                cursor: 'pointer',
              }}
            >
              Try again
            </button>
            {showSheet && (
              <button
                onClick={() => setShowSheet(false)}
                style={{ color: 'var(--text2)', fontSize: 13, background: 'none', border: 'none', cursor: 'pointer' }}
              >
                Dismiss for now
              </button>
            )}
          </div>
        )}
      </div>

      {/* Connect sheet — rendered for all states except done */}
      {gateState !== 'done' && showSheet && (
        <ConnectSheet
          firstRun
          onTokenObtained={(token) => void handleTokenObtained(token)}
          authClient={authClient}
        />
      )}
    </div>
  )
}
