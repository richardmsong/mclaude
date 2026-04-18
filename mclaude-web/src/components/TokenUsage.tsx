import { useState, useMemo } from 'react'
import { NavBar } from './NavBar'
import { PRICE_PER_M, loadCalibration, saveCalibration, formatTokens, formatCost } from '@/lib/pricing'
import type { SessionKVState } from '@/types'

const BUDGET_KEY = 'mclaude.monthlyBudget'

function loadBudget(): number | null {
  try {
    const stored = localStorage.getItem(BUDGET_KEY)
    const val = stored !== null ? parseFloat(stored) : NaN
    return isNaN(val) || val <= 0 ? null : val
  } catch {
    return null
  }
}

function saveBudget(val: number): void {
  try {
    localStorage.setItem(BUDGET_KEY, String(val))
  } catch {}
}

interface TokenUsageProps {
  sessions: SessionKVState[]
  onBack: () => void
  connected: boolean
}

type TimeRange = '1H' | '6H' | '24H' | '7D' | '30D'

const RANGE_MS: Record<TimeRange, number> = {
  '1H':  60 * 60 * 1000,
  '6H':  6 * 60 * 60 * 1000,
  '24H': 24 * 60 * 60 * 1000,
  '7D':  7 * 24 * 60 * 60 * 1000,
  '30D': 30 * 24 * 60 * 60 * 1000,
}

export function TokenUsage({ sessions, onBack, connected }: TokenUsageProps) {
  const [range, setRange] = useState<TimeRange>('24H')
  const [calibration, setCalibration] = useState<number>(loadCalibration)
  const [showCalibration, setShowCalibration] = useState(false)
  const [calibrationInput, setCalibrationInput] = useState('')
  const [budget, setBudget] = useState<number | null>(loadBudget)
  const [showBudgetPrompt, setShowBudgetPrompt] = useState(false)
  const [budgetInput, setBudgetInput] = useState('')
  const ranges: TimeRange[] = ['1H', '6H', '24H', '7D', '30D']

  // Filter sessions by time range using stateSince as the timestamp proxy
  const filteredSessions = useMemo(() => {
    const cutoff = Date.now() - RANGE_MS[range]
    return sessions.filter(s => {
      if (!s.stateSince) return true
      const t = new Date(s.stateSince).getTime()
      return isNaN(t) ? true : t >= cutoff
    })
  }, [sessions, range])

  // Aggregate usage from filtered sessions
  const usage = useMemo(() => {
    let inputTokens = 0, outputTokens = 0, cacheReadTokens = 0, cacheWriteTokens = 0
    for (const s of filteredSessions) {
      inputTokens += s.usage.inputTokens
      outputTokens += s.usage.outputTokens
      cacheReadTokens += s.usage.cacheReadTokens
      cacheWriteTokens += s.usage.cacheWriteTokens
    }
    return { inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens }
  }, [filteredSessions])

  // Tok/m: find the oldest and newest stateSince timestamps in filtered sessions
  const tokPerMin = useMemo(() => {
    const times = filteredSessions
      .map(s => s.stateSince ? new Date(s.stateSince).getTime() : NaN)
      .filter(t => !isNaN(t))
    if (times.length < 2) return null
    const earliest = Math.min(...times)
    const latest = Math.max(...times)
    const elapsedMin = (latest - earliest) / 60_000
    if (elapsedMin < 1) return null
    const totalTok = usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
    return totalTok / elapsedMin
  }, [filteredSessions, usage])

  const rawBreakdown = [
    { label: 'Input',       tokens: usage.inputTokens,       color: 'var(--blue)',   cost: usage.inputTokens / 1_000_000 * PRICE_PER_M.input },
    { label: 'Output',      tokens: usage.outputTokens,      color: 'var(--green)',  cost: usage.outputTokens / 1_000_000 * PRICE_PER_M.output },
    { label: 'Cache Read',  tokens: usage.cacheReadTokens,   color: 'var(--purple)', cost: usage.cacheReadTokens / 1_000_000 * PRICE_PER_M.cacheRead },
    { label: 'Cache Write', tokens: usage.cacheWriteTokens,  color: 'var(--orange)', cost: usage.cacheWriteTokens / 1_000_000 * PRICE_PER_M.cacheWrite },
  ]

  // Apply calibration multiplier to costs
  const breakdown = rawBreakdown.map(t => ({ ...t, cost: t.cost * calibration }))

  const totalTokens = usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
  const totalCost = breakdown.reduce((s, t) => s + t.cost, 0)

  // Monthly budget projection: linear extrapolation from days elapsed this month
  const budgetPct = budget != null && budget > 0 ? Math.min((totalCost / budget) * 100, 100) : null
  const projectedCost = useMemo(() => {
    if (budget == null) return null
    const now = new Date()
    const daysInMonth = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate()
    const dayOfMonth = now.getDate() + now.getHours() / 24
    if (dayOfMonth < 0.5) return null
    // Use 30D range cost as the monthly base for projection
    let monthCost = 0
    for (const s of sessions) {
      const u = s.usage
      const cost = (u.inputTokens / 1_000_000 * PRICE_PER_M.input +
        u.outputTokens / 1_000_000 * PRICE_PER_M.output +
        u.cacheReadTokens / 1_000_000 * PRICE_PER_M.cacheRead +
        u.cacheWriteTokens / 1_000_000 * PRICE_PER_M.cacheWrite) * calibration
      monthCost += cost
    }
    return (monthCost / dayOfMonth) * daysInMonth
  }, [sessions, budget, calibration])

  const budgetBarColor = budgetPct == null ? 'var(--blue)'
    : budgetPct >= 85 ? 'var(--red)'
    : budgetPct >= 60 ? 'var(--orange)'
    : 'var(--green)'

  // Stacked bar for token composition
  const barSegments = breakdown.filter(t => t.tokens > 0)
  const barTotal = barSegments.reduce((s, t) => s + t.tokens, 0)

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

  const handleSaveBudget = () => {
    const val = parseFloat(budgetInput)
    if (!isNaN(val) && val > 0) {
      setBudget(val)
      saveBudget(val)
    }
    setShowBudgetPrompt(false)
    setBudgetInput('')
  }

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

        {/* Stat tiles row: Tokens, Cost, Tok/m */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr',
          gap: 10,
          marginBottom: 16,
        }}>
          <div style={{
            background: 'var(--surf)',
            border: '1px solid var(--border)',
            borderRadius: 12,
            padding: 14,
          }}>
            <div style={{ color: 'var(--text2)', fontSize: 11, marginBottom: 4 }}>Tokens</div>
            <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--text)' }}>
              {formatTokens(totalTokens)}
            </div>
          </div>
          <div style={{
            background: 'var(--surf)',
            border: '1px solid var(--border)',
            borderRadius: 12,
            padding: 14,
          }}>
            <div style={{ color: 'var(--text2)', fontSize: 11, marginBottom: 4 }}>Cost</div>
            <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--text)' }}>
              {formatCost(totalCost)}
            </div>
          </div>
          <div style={{
            background: 'var(--surf)',
            border: '1px solid var(--border)',
            borderRadius: 12,
            padding: 14,
          }}>
            <div style={{ color: 'var(--text2)', fontSize: 11, marginBottom: 4 }}>Tok/m</div>
            <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--text)' }}>
              {tokPerMin != null ? formatTokens(Math.round(tokPerMin)) : '—'}
            </div>
          </div>
        </div>

        {/* Monthly budget bar */}
        <div style={{
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 12,
          padding: 14,
          marginBottom: 16,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
            <div style={{ color: 'var(--text2)', fontSize: 12, fontWeight: 500 }}>Monthly Budget</div>
            {budget != null ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                {projectedCost != null && (
                  <span style={{ color: 'var(--text2)', fontSize: 11 }}>
                    Projected: {formatCost(projectedCost)}
                  </span>
                )}
                <button
                  onClick={() => { setBudgetInput(String(budget)); setShowBudgetPrompt(true) }}
                  style={{ color: 'var(--blue)', fontSize: 11 }}
                >
                  ${budget.toFixed(2)}
                </button>
              </div>
            ) : (
              <button
                onClick={() => setShowBudgetPrompt(true)}
                style={{
                  fontSize: 11,
                  color: 'var(--blue)',
                  padding: '2px 8px',
                  background: 'var(--surf2)',
                  borderRadius: 8,
                }}
              >
                Set budget
              </button>
            )}
          </div>

          {budget != null && budgetPct != null ? (
            <>
              <div style={{
                height: 8,
                borderRadius: 4,
                overflow: 'hidden',
                background: 'var(--surf3)',
                marginBottom: 4,
              }}>
                <div style={{
                  height: '100%',
                  width: `${budgetPct}%`,
                  background: budgetBarColor,
                  borderRadius: 4,
                  transition: 'width 0.3s ease',
                }} />
              </div>
              <div style={{ color: 'var(--text2)', fontSize: 11 }}>
                {budgetPct.toFixed(1)}% of ${budget.toFixed(2)} used
              </div>
            </>
          ) : (
            <div style={{ color: 'var(--text3)', fontSize: 12 }}>
              No budget set — tap "Set budget" to track monthly spend.
            </div>
          )}

          {showBudgetPrompt && (
            <div style={{ marginTop: 12 }}>
              <input
                type="number"
                step="1"
                min="1"
                value={budgetInput}
                onChange={e => setBudgetInput(e.target.value)}
                placeholder="Monthly budget in USD"
                style={{
                  width: '100%',
                  padding: '8px 10px',
                  background: 'var(--surf2)',
                  border: '1px solid var(--border)',
                  borderRadius: 8,
                  color: 'var(--text)',
                  fontSize: 14,
                  marginBottom: 8,
                }}
              />
              <div style={{ display: 'flex', gap: 8 }}>
                <button
                  onClick={handleSaveBudget}
                  style={{
                    flex: 1,
                    padding: '6px 0',
                    background: 'var(--blue)',
                    color: '#fff',
                    borderRadius: 8,
                    fontWeight: 600,
                    fontSize: 13,
                  }}
                >
                  Save
                </button>
                <button
                  onClick={() => { setShowBudgetPrompt(false); setBudgetInput('') }}
                  style={{
                    padding: '6px 14px',
                    background: 'var(--surf2)',
                    color: 'var(--text2)',
                    borderRadius: 8,
                    fontSize: 13,
                  }}
                >
                  Cancel
                </button>
              </div>
            </div>
          )}
        </div>

        {/* Token breakdown list */}
        <div style={{
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 12,
          padding: 14,
          marginBottom: 16,
        }}>
          <div style={{ color: 'var(--text2)', fontSize: 12, fontWeight: 500, marginBottom: 10 }}>Breakdown</div>
          {breakdown.map((t, i) => (
            <div
              key={t.label}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                paddingBottom: i < breakdown.length - 1 ? 8 : 0,
                marginBottom: i < breakdown.length - 1 ? 8 : 0,
                borderBottom: i < breakdown.length - 1 ? '1px solid var(--border)' : 'none',
              }}
            >
              {/* Colored dot */}
              <div style={{
                width: 8,
                height: 8,
                borderRadius: '50%',
                background: t.color,
                flexShrink: 0,
              }} />
              {/* Label */}
              <div style={{ flex: 1, color: 'var(--text)', fontSize: 13 }}>{t.label}</div>
              {/* Token count */}
              <div style={{ color: 'var(--text2)', fontSize: 13, minWidth: 50, textAlign: 'right' }}>
                {formatTokens(t.tokens)}
              </div>
              {/* Cost */}
              <div style={{ color: 'var(--text2)', fontSize: 12, minWidth: 50, textAlign: 'right' }}>
                {formatCost(t.cost)}
              </div>
            </div>
          ))}
        </div>

        {/* Stacked token composition bar */}
        {barTotal > 0 && (
          <div style={{ marginBottom: 16 }}>
            <div style={{
              height: 10,
              borderRadius: 5,
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
                  <div style={{ width: 7, height: 7, borderRadius: 2, background: t.color }} />
                  <span style={{ color: 'var(--text2)', fontSize: 11 }}>{t.label}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Total cost card with calibration */}
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
