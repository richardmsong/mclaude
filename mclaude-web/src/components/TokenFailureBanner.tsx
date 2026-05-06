/**
 * ADR-0092: Token failure banner.
 *
 * Rendered at the top of the session view when any session reports
 * status: "token_expired" in session KV.
 *
 * - Background: --warning, foreground: --text
 * - [Reconnect] opens the Connect sheet
 */

import type { AuthClient } from '@/transport/auth-client'

interface TokenFailureBannerProps {
  /** Called when user clicks [Reconnect] — opens Connect sheet. */
  onReconnect: () => void
}

export function TokenFailureBanner({ onReconnect }: TokenFailureBannerProps) {
  return (
    <div
      role="alert"
      style={{
        background: 'var(--warning, #7c5c00)',
        color: 'var(--text)',
        padding: '10px 16px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 12,
        fontSize: 14,
      }}
    >
      <span>⚠ Your Claude token is invalid</span>
      <button
        onClick={onReconnect}
        style={{
          background: 'none',
          border: '1px solid var(--text)',
          borderRadius: 6,
          color: 'var(--text)',
          padding: '4px 12px',
          fontSize: 13,
          fontWeight: 500,
          cursor: 'pointer',
          whiteSpace: 'nowrap',
        }}
      >
        Reconnect
      </button>
    </div>
  )
}

// Re-export for convenience
export type { AuthClient }
