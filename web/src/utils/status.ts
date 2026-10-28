import type { RunRecord } from '@/api/types'
import type { RunStatus } from '@/api/types'

const ACTIVE_STATUSES = new Set<RunStatus>(['queued', 'provisioning', 'running', 'finalizing', 'verifying'])

export function isActiveStatus(status: RunStatus): boolean {
  return ACTIVE_STATUSES.has(status)
}

export function statusColor(status: RunStatus): string {
  switch (status) {
    case 'queued':
    case 'provisioning':
      return 'default'
    case 'running':
    case 'finalizing':
      return 'processing'
    case 'verifying':
      return 'warning'
    case 'succeeded':
      return 'success'
    case 'failed':
    case 'timed_out':
    case 'stagnated':
      return 'error'
    default:
      return 'default'
  }
}

export function isTerminalStatus(status: RunStatus): boolean {
  return !ACTIVE_STATUSES.has(status)
}

export function runStatus(record: Pick<RunRecord, 'phase' | 'outcome'>): RunStatus {
  if (record.phase === 'terminal') return record.outcome ?? 'failed'
  return (record.phase ?? 'queued') as RunStatus
}
