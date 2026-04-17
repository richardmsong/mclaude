// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { PRICE_PER_M, computeCost, formatTokens, formatCost, loadCalibration } from './pricing'
import type { UsageStats } from '@/types'

const makeUsage = (overrides: Partial<UsageStats> = {}): UsageStats => ({
  inputTokens: 0,
  outputTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  costUsd: 0,
  ...overrides,
})

describe('pricing', () => {
  describe('PRICE_PER_M', () => {
    it('has expected values', () => {
      expect(PRICE_PER_M.input).toBe(3.0)
      expect(PRICE_PER_M.output).toBe(15.0)
      expect(PRICE_PER_M.cacheRead).toBe(0.3)
      expect(PRICE_PER_M.cacheWrite).toBe(3.75)
    })
  })

  describe('computeCost', () => {
    it('returns 0 for zero usage', () => {
      expect(computeCost(makeUsage())).toBe(0)
    })

    it('computes input cost correctly', () => {
      const usage = makeUsage({ inputTokens: 1_000_000 })
      expect(computeCost(usage)).toBeCloseTo(3.0)
    })

    it('computes output cost correctly', () => {
      const usage = makeUsage({ outputTokens: 1_000_000 })
      expect(computeCost(usage)).toBeCloseTo(15.0)
    })

    it('computes cache-read cost correctly', () => {
      const usage = makeUsage({ cacheReadTokens: 1_000_000 })
      expect(computeCost(usage)).toBeCloseTo(0.3)
    })

    it('computes cache-write cost correctly', () => {
      const usage = makeUsage({ cacheWriteTokens: 1_000_000 })
      expect(computeCost(usage)).toBeCloseTo(3.75)
    })

    it('applies calibration multiplier', () => {
      const usage = makeUsage({ inputTokens: 1_000_000 })
      expect(computeCost(usage, 2.0)).toBeCloseTo(6.0)
    })

    it('combines all token types', () => {
      const usage = makeUsage({
        inputTokens: 1_000_000,
        outputTokens: 1_000_000,
        cacheReadTokens: 1_000_000,
        cacheWriteTokens: 1_000_000,
      })
      const expected = (3.0 + 15.0 + 0.3 + 3.75)
      expect(computeCost(usage)).toBeCloseTo(expected)
    })
  })

  describe('formatTokens', () => {
    it('formats numbers below 1K as-is', () => {
      expect(formatTokens(0)).toBe('0')
      expect(formatTokens(999)).toBe('999')
    })

    it('formats numbers in thousands as K', () => {
      expect(formatTokens(1000)).toBe('1.0K')
      expect(formatTokens(12345)).toBe('12.3K')
    })

    it('formats numbers in millions as M', () => {
      expect(formatTokens(1_000_000)).toBe('1.0M')
      expect(formatTokens(2_500_000)).toBe('2.5M')
    })
  })

  describe('formatCost', () => {
    it('formats cost as dollar string with 3 decimals', () => {
      expect(formatCost(0)).toBe('$0.000')
      expect(formatCost(0.041)).toBe('$0.041')
      expect(formatCost(1.2346)).toBe('$1.235')
    })
  })

  describe('loadCalibration', () => {
    beforeEach(() => {
      localStorage.clear()
    })

    it('returns 1.0 when nothing stored', () => {
      expect(loadCalibration()).toBe(1.0)
    })

    it('returns stored calibration', () => {
      localStorage.setItem('mclaude.costCalibration', '1.5')
      expect(loadCalibration()).toBe(1.5)
    })

    it('returns 1.0 for invalid stored value', () => {
      localStorage.setItem('mclaude.costCalibration', 'bad')
      expect(loadCalibration()).toBe(1.0)
    })

    it('returns 1.0 for zero or negative stored value', () => {
      localStorage.setItem('mclaude.costCalibration', '0')
      expect(loadCalibration()).toBe(1.0)
      localStorage.setItem('mclaude.costCalibration', '-1')
      expect(loadCalibration()).toBe(1.0)
    })
  })
})
