import type { UsageStats } from '@/types'
import { PRICE_PER_M, computeCost, formatTokens, formatCost, loadCalibration } from '@/lib/pricing'

interface TurnUsageSheetProps {
  usage: UsageStats
  model?: string
  sessionUsage?: UsageStats
  onClose: () => void
}

export function TurnUsageSheet({ usage, model, sessionUsage, onClose }: TurnUsageSheetProps) {
  const calibration = loadCalibration()

  const tiles = [
    { label: 'Input',   tokens: usage.inputTokens,      color: 'var(--blue)',   cost: usage.inputTokens      / 1_000_000 * PRICE_PER_M.input      * calibration },
    { label: 'Output',  tokens: usage.outputTokens,     color: 'var(--green)',  cost: usage.outputTokens     / 1_000_000 * PRICE_PER_M.output     * calibration },
    { label: 'Cache W', tokens: usage.cacheWriteTokens, color: 'var(--orange)', cost: usage.cacheWriteTokens / 1_000_000 * PRICE_PER_M.cacheWrite * calibration },
    { label: 'Cache R', tokens: usage.cacheReadTokens,  color: 'var(--purple)', cost: usage.cacheReadTokens  / 1_000_000 * PRICE_PER_M.cacheRead  * calibration },
  ]

  const totalTokens =
    usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
  const totalCost = computeCost(usage, calibration)

  const barSegments = tiles.filter(t => t.tokens > 0)
  const barTotal = barSegments.reduce((s, t) => s + t.tokens, 0)

  const sessionCost = sessionUsage ? computeCost(sessionUsage, calibration) : null
  const sessionPct =
    sessionCost !== null && sessionCost > 0
      ? Math.min(100, (totalCost / sessionCost) * 100)
      : null

  return (
    <>
      {/* Scrim */}
      <div
        onClick={onClose}
        style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(0,0,0,0.5)',
          zIndex: 400,
        }}
      />

      {/* Bottom sheet */}
      <div style={{
        position: 'fixed',
        bottom: 0,
        left: 0,
        right: 0,
        background: 'var(--surf)',
        borderRadius: '16px 16px 0 0',
        zIndex: 401,
        maxHeight: '80vh',
        display: 'flex',
        flexDirection: 'column',
        boxShadow: '0 -4px 24px rgba(0,0,0,0.5)',
      }}>
        {/* Header */}
        <div style={{
          display: 'flex',
          alignItems: 'center',
          padding: '14px 16px',
          borderBottom: '1px solid var(--border)',
          flexShrink: 0,
        }}>
          <span style={{ flex: 1, fontWeight: 600, fontSize: 15 }}>Turn Usage</span>
          <button
            onClick={onClose}
            style={{ color: 'var(--text2)', fontSize: 18, padding: '0 4px' }}
          >
            &times;
          </button>
        </div>

        {/* Content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
          {/* Model badge */}
          {model && (
            <div style={{ color: 'var(--text2)', fontSize: 13, marginBottom: 14 }}>
              {model}
            </div>
          )}

          {/* 2x2 token tiles */}
          <div style={{
            display: 'grid',
            gridTemplateColumns: '1fr 1fr',
            gap: 10,
            marginBottom: 16,
          }}>
            {tiles.map(tile => (
              <div key={tile.label} style={{
                background: 'var(--bg)',
                border: '1px solid var(--border)',
                borderRadius: 12,
                padding: 14,
              }}>
                <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 4 }}>{tile.label}</div>
                <div style={{ fontSize: 22, fontWeight: 700, color: tile.color }}>
                  {formatTokens(tile.tokens)}
                </div>
                <div style={{ color: 'var(--text2)', fontSize: 12, marginTop: 2 }}>
                  {formatCost(tile.cost)}
                </div>
              </div>
            ))}
          </div>

          {/* Stacked bar */}
          {barTotal > 0 && (
            <div style={{ marginBottom: 16 }}>
              <div style={{
                height: 12,
                borderRadius: 6,
                overflow: 'hidden',
                display: 'flex',
                background: 'var(--surf3)',
              }}>
                {barSegments.map(t => (
                  <div key={t.label} style={{
                    height: '100%',
                    width: `${(t.tokens / barTotal) * 100}%`,
                    background: t.color,
                  }} />
                ))}
              </div>
              <div style={{ display: 'flex', gap: 12, marginTop: 6, flexWrap: 'wrap' }}>
                {barSegments.map(t => (
                  <div key={t.label} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                    <div style={{ width: 8, height: 8, borderRadius: 2, background: t.color }} />
                    <span style={{ color: 'var(--text2)', fontSize: 11 }}>{t.label}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Estimated cost card */}
          <div style={{
            background: 'var(--bg)',
            border: '1px solid var(--border)',
            borderRadius: 12,
            padding: 16,
            marginBottom: 12,
          }}>
            <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 4 }}>Estimated Cost</div>
            <div style={{ fontSize: 28, fontWeight: 700, color: 'var(--text)' }}>
              {formatCost(totalCost)}
            </div>
            <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 4 }}>
              {formatTokens(totalTokens)} total tokens
            </div>
          </div>

          {/* Session proportion */}
          {sessionPct !== null && sessionCost !== null && (
            <div style={{
              background: 'var(--bg)',
              border: '1px solid var(--border)',
              borderRadius: 12,
              padding: 16,
            }}>
              <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 8 }}>% of session</div>
              <div style={{
                height: 6,
                borderRadius: 3,
                overflow: 'hidden',
                background: 'var(--surf3)',
                marginBottom: 6,
              }}>
                <div style={{
                  height: '100%',
                  width: `${sessionPct}%`,
                  background: 'var(--blue)',
                  borderRadius: 3,
                }} />
              </div>
              <div style={{ color: 'var(--text3)', fontSize: 11 }}>
                {formatCost(totalCost)} of {formatCost(sessionCost)} total &middot; {sessionPct.toFixed(0)}%
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  )
}
