import { useState } from 'react'
import { NavBar } from './NavBar'

interface UsageData {
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  costUsd: number
}

interface TokenUsageProps {
  usage: UsageData
  onBack: () => void
  connected: boolean
}

const PRICE_PER_M = {
  input: 3.0,
  output: 15.0,
  cacheRead: 0.3,
  cacheWrite: 3.75,
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function formatCost(usd: number): string {
  return `$${usd.toFixed(3)}`
}

type TimeRange = '1H' | '6H' | '24H' | '7D' | '30D'

export function TokenUsage({ usage, onBack, connected }: TokenUsageProps) {
  const [range, setRange] = useState<TimeRange>('24H')
  const ranges: TimeRange[] = ['1H', '6H', '24H', '7D', '30D']

  const tiles = [
    { label: 'Input', tokens: usage.inputTokens, color: 'var(--blue)', cost: usage.inputTokens / 1_000_000 * PRICE_PER_M.input },
    { label: 'Output', tokens: usage.outputTokens, color: 'var(--green)', cost: usage.outputTokens / 1_000_000 * PRICE_PER_M.output },
    { label: 'Cache W', tokens: usage.cacheWriteTokens, color: 'var(--orange)', cost: usage.cacheWriteTokens / 1_000_000 * PRICE_PER_M.cacheWrite },
    { label: 'Cache R', tokens: usage.cacheReadTokens, color: 'var(--purple)', cost: usage.cacheReadTokens / 1_000_000 * PRICE_PER_M.cacheRead },
  ]

  const totalTokens = usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
  const totalCost = tiles.reduce((s, t) => s + t.cost, 0)

  // Bar chart: just show tiles as stacked bar proportions
  const barSegments = tiles.filter(t => t.tokens > 0)
  const barTotal = barSegments.reduce((s, t) => s + t.tokens, 0)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <NavBar title="Token Usage" onBack={onBack} connected={connected} />

      <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
        {/* Time range chips */}
        <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
          {ranges.map(r => (
            <button
              key={r}
              onClick={() => setRange(r)}
              style={{
                padding: '4px 12px',
                borderRadius: 14,
                fontSize: 13,
                fontWeight: 500,
                background: range === r ? 'var(--blue)' : 'var(--surf2)',
                color: range === r ? '#fff' : 'var(--text2)',
              }}
            >
              {r}
            </button>
          ))}
        </div>

        {/* 2x2 token tiles */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: 10,
          marginBottom: 16,
        }}>
          {tiles.map(tile => (
            <div key={tile.label} style={{
              background: 'var(--surf)',
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

        {/* Total cost card */}
        <div style={{
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 12,
          padding: 16,
        }}>
          <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 4 }}>Estimated Cost</div>
          <div style={{ fontSize: 28, fontWeight: 700, color: 'var(--text)' }}>
            {formatCost(totalCost)}
          </div>
          <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 4 }}>
            {formatTokens(totalTokens)} total tokens
          </div>
          <div style={{ color: 'var(--text3)', fontSize: 11, marginTop: 8 }}>
            Prices: input ${PRICE_PER_M.input}/M · output ${PRICE_PER_M.output}/M
          </div>
        </div>
      </div>
    </div>
  )
}
