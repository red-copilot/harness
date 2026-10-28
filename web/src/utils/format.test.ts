import { describe, expect, it } from 'vitest'
import { formatBytes, formatCost, formatDateTime, formatDuration, formatPercent, formatWallTimeNs, isZeroTime, totalUsage, truncate } from './format'

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

  it('formats bytes and truncates text', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KiB')
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MiB')
    expect(formatBytes(undefined)).toBe('—')
    expect(truncate('abcdef', 4)).toBe('abc…')
    expect(truncate('abc', 4)).toBe('abc')
  })

  it('treats Go zero time as absent', () => {
    expect(isZeroTime('0001-01-01T00:00:00Z')).toBe(true)
    expect(formatDateTime('0001-01-01T00:00:00Z')).toBe('—')
  })

  it('aggregates usage', () => {
    expect(totalUsage([{ input_tokens: 1, total_tokens: 2 }, { input_tokens: 3, total_tokens: 4, cost_usd: 0.2 }])).toMatchObject({
      input_tokens: 4,
      total_tokens: 6,
      cost_usd: 0.2,
    })
  })
})
