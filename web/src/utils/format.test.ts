import { describe, expect, it } from 'vitest'
import { formatCost, formatDuration, formatPercent, formatWallTimeNs, totalUsage } from './format'

describe('format utilities', () => {
  it('formats durations and budgets', () => {
    expect(formatDuration(12)).toBe('12 ms')
    expect(formatDuration(1500)).toBe('1.5 s')
    expect(formatDuration(90_000)).toBe('1.5 min')
    expect(formatWallTimeNs(300_000_000_000)).toBe('5 min')
  })

  it('formats percentages and costs', () => {
    expect(formatPercent(0.256)).toBe('25.6%')
    expect(formatCost(1.234)).toBe('$1.23')
    expect(formatCost(0.0012)).toBe('$0.0012')
  })

  it('aggregates usage', () => {
    expect(totalUsage([{ input_tokens: 1, total_tokens: 2 }, { input_tokens: 3, total_tokens: 4, cost_usd: 0.2 }])).toMatchObject({
      input_tokens: 4,
      total_tokens: 6,
      cost_usd: 0.2,
    })
  })
})
