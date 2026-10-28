import type { RunDescriptor, RunRecord, RunStatus, Usage } from '@/api/types'

/** agentIds flattens a run descriptor's reasoner and executor into ids. */
export function agentIds(spec?: RunDescriptor | null): string[] {
  if (!spec) return []
  return [spec.reasoner?.id, spec.executor?.id].filter(
    (id): id is string => Boolean(id),
  )
}
import { isActiveStatus } from './status'

export type UiLocale = 'zh' | 'en'

export function localeTag(locale: UiLocale): string {
  return locale === 'zh' ? 'zh-CN' : 'en-US'
}

/** isZeroTime reports Go's zero time sentinel used for unset timestamps. */
export function isZeroTime(value?: string | null): boolean {
  return Boolean(value?.startsWith('0001-01-01'))
}

export function formatDateTime(value?: string | null, locale: UiLocale = 'zh'): string {
  if (!value || isZeroTime(value)) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(localeTag(locale), {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

export function formatDuration(ms?: number | null): string {
  if (ms === undefined || ms === null || !Number.isFinite(ms)) return '—'
  if (ms < 1000) return `${Math.max(0, Math.round(ms))} ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${trimNumber(seconds)} s`
  const minutes = seconds / 60
  if (minutes < 60) return `${trimNumber(minutes)} min`
  const hours = minutes / 60
  return `${trimNumber(hours)} h`
}

export function formatRunDuration(run: RunRecord, now = Date.now()): string {
  if (!run.started_at) return '—'
  const start = new Date(run.started_at).getTime()
  if (Number.isNaN(start)) return '—'
  const end = run.finished_at ? new Date(run.finished_at).getTime() : now
  if (Number.isNaN(end)) return '—'
  return formatDuration(end - start)
}

export function formatTokens(value?: number): string {
  return new Intl.NumberFormat('en-US').format(value ?? 0)
}

export function formatCost(value?: number): string {
  return `$${(value ?? 0).toFixed(value !== undefined && value < 0.01 ? 4 : 2)}`
}

export function formatPercent(value?: number): string {
  return `${((value ?? 0) * 100).toFixed(1)}%`
}

export function formatWallTimeNs(value?: number): string {
  if (!value) return '—'
  return formatDuration(value / 1_000_000)
}

/** truncate shortens text to at most limit characters, ellipsis included. */
export function truncate(value: string, limit: number): string {
  if (value.length <= limit) return value
  return `${value.slice(0, Math.max(0, limit - 1))}…`
}

const BYTE_UNITS = ['KiB', 'MiB', 'GiB'] as const

export function formatBytes(value?: number): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value < 1024) return `${Math.max(0, Math.round(value))} B`
  let size = value / 1024
  let unitIndex = 0
  while (unitIndex < BYTE_UNITS.length - 1 && size >= 1024) {
    size /= 1024
    unitIndex++
  }
  return `${size.toFixed(1)} ${BYTE_UNITS[unitIndex]}`
}

export function totalUsage(usages: Array<Usage | undefined>): Usage {
  return usages.reduce<Usage>(
    (acc, usage) => ({
      input_tokens: (acc.input_tokens ?? 0) + (usage?.input_tokens ?? 0),
      output_tokens: (acc.output_tokens ?? 0) + (usage?.output_tokens ?? 0),
      total_tokens: (acc.total_tokens ?? 0) + (usage?.total_tokens ?? 0),
      model_calls: (acc.model_calls ?? 0) + (usage?.model_calls ?? 0),
      tool_calls: (acc.tool_calls ?? 0) + (usage?.tool_calls ?? 0),
      cost_usd: (acc.cost_usd ?? 0) + (usage?.cost_usd ?? 0),
    }),
    {},
  )
}

export function runIsLive(status: RunStatus): boolean {
  return isActiveStatus(status)
}

function trimNumber(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(1)
}
