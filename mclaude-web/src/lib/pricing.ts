import type { UsageStats } from '@/types'

export const PRICE_PER_M = {
  input: 3.0,
  output: 15.0,
  cacheRead: 0.3,
  cacheWrite: 3.75,
}

const CALIBRATION_KEY = 'mclaude.costCalibration'

export function loadCalibration(): number {
  try {
    const stored = localStorage.getItem(CALIBRATION_KEY)
    const val = stored !== null ? parseFloat(stored) : NaN
    return isNaN(val) || val <= 0 ? 1.0 : val
  } catch {
    return 1.0
  }
}

export function saveCalibration(factor: number): void {
  try {
    localStorage.setItem(CALIBRATION_KEY, String(factor))
  } catch {}
}

export function computeCost(usage: UsageStats, calibration = 1.0): number {
  const raw =
    (usage.inputTokens * PRICE_PER_M.input +
      usage.outputTokens * PRICE_PER_M.output +
      usage.cacheReadTokens * PRICE_PER_M.cacheRead +
      usage.cacheWriteTokens * PRICE_PER_M.cacheWrite) /
    1_000_000
  return raw * calibration
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

export function formatCost(usd: number): string {
  return `$${usd.toFixed(3)}`
}
