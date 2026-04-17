import { useState } from 'react'
import { NavBar } from './NavBar'
import { PRICE_PER_M, loadCalibration, saveCalibration, formatTokens, formatCost } from '@/lib/pricing'

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

type TimeRange = '1H' | '6H' | '24H' | '7D' | '30D'

export function TokenUsage({ usage, onBack, connected }: TokenUsageProps) {
  const [range, setRange] = useState<TimeRange>('24H')
  const [calibration, setCalibration] = useState<number>(loadCalibration)
  const [showCalibration, setShowCalibration] = useState(false)
  const [calibrationInput, setCalibrationInput] = useState('')
  const ranges: TimeRange[] = ['1H', '6H', '24H', '7D', '30D']

  const rawTiles = [
    { label: 'Input', tokens: usage.inputTokens, color: 'var(--blue)', cost: usage.inputTokens / 1_000_000 * PRICE_PER_M.input },
    { label: 'Output', tokens: usage.outputTokens, color: 'var(--green)', cost: usage.outputTokens / 1_000_000 * PRICE_PER_M.output },
    { label: 'Cache W', tokens: usage.cacheWriteTokens, color: 'var(--orange)', cost: usage.cacheWriteTokens / 1_000_000 * PRICE_PER_M.cacheWrite },
    { label: 'Cache R', tokens: usage.cacheReadTokens, color: 'var(--purple)', cost: usage.cacheReadTokens / 1_000_000 * PRICE_PER_M.cacheRead },
  ]

  // Apply calibration multiplier to costs
  const tiles = rawTiles.map(t => ({ ...t, cost: t.cost * calibration }))

  const totalTokens = usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
  const totalCost = tiles.reduce((s, t) => s + t.cost, 0)

  const handleSaveCalibration = () => {
    const val = parseFloat(calibrationInput)
    if (!isNaN(val) && val > 0) {
      setCalibration(val)
      saveCalibration(val)
    }
    setShowCalibration(false)
    setCalibrationInput('')
  }

  const handleResetCalibration = () => {
    setCalibration(1.0)
    saveCalibration(1.0)
    setShowCalibration(false)
  }

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
          marginBottom: 12,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
            <div style={{ color: 'var(--text2)', fontSize: 12 }}>Estimated Cost</div>
            {calibration !== 1.0 && (
              <span style={{
                background: 'var(--surf2)',
                color: 'var(--orange)',
                fontSize: 11,
                padding: '2px 6px',
                borderRadius: 8,
                fontWeight: 600,
              }}>
                &times;{calibration.toFixed(2)}
              </span>
            )}
          </div>
          <div style={{ fontSize: 28, fontWeight: 700, color: 'var(--text)' }}>
            {formatCost(totalCost)}
          </div>
          <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 4 }}>
            {formatTokens(totalTokens)} total tokens
          </div>
          <div style={{ color: 'var(--text3)', fontSize: 11, marginTop: 8 }}>
            Prices: input ${PRICE_PER_M.input}/M &middot; output ${PRICE_PER_M.output}/M
          </div>
          <button
            onClick={() => {
              setCalibrationInput(calibration !== 1.0 ? String(calibration) : '')
              setShowCalibration(true)
            }}
            style={{
              marginTop: 10,
              color: 'var(--blue)',
              fontSize: 12,
            }}
          >
            Calibrate
          </button>
        </div>

        {/* Calibration sheet */}
        {showCalibration && (
          <div style={{
            background: 'var(--surf)',
            border: '1px solid var(--border)',
            borderRadius: 12,
            padding: 16,
          }}>
            <div style={{ fontWeight: 600, marginBottom: 8 }}>Calibrate Cost Estimates</div>
            <div style={{ color: 'var(--text2)', fontSize: 13, marginBottom: 12 }}>
              Enter a multiplier to match Anthropic Console actuals. E.g., 1.2 = 20% higher than estimated.
            </div>
            <input
              type="number"
              step="0.01"
              min="0.01"
              value={calibrationInput}
              onChange={e => setCalibrationInput(e.target.value)}
              placeholder={String(calibration)}
              style={{
                width: '100%',
                padding: '10px 12px',
                background: 'var(--surf2)',
                border: '1px solid var(--border)',
                borderRadius: 8,
                color: 'var(--text)',
                fontSize: 15,
                marginBottom: 12,
              }}
            />
            <div style={{ display: 'flex', gap: 8 }}>
              <button
                onClick={handleSaveCalibration}
                style={{
                  flex: 1,
                  padding: '8px 0',
                  background: 'var(--blue)',
                  color: '#fff',
                  borderRadius: 8,
                  fontWeight: 600,
                }}
              >
                Save
              </button>
              {calibration !== 1.0 && (
                <button
                  onClick={handleResetCalibration}
                  style={{
                    padding: '8px 16px',
                    background: 'var(--surf2)',
                    color: 'var(--text2)',
                    borderRadius: 8,
                  }}
                >
                  Reset
                </button>
              )}
              <button
                onClick={() => setShowCalibration(false)}
                style={{
                  padding: '8px 16px',
                  background: 'var(--surf2)',
                  color: 'var(--text2)',
                  borderRadius: 8,
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
