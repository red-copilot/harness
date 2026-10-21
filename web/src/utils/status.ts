import type { RunStatus } from '@/api/types'

const ACTIVE_STATUSES = new Set<RunStatus>(['queued', 'starting', 'running', 'verifying'])
const TERMINAL_STATUSES = new Set<RunStatus>([
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
  'interrupted',
  'stagnated',
])

export function isActiveStatus(status: RunStatus): boolean {
  return ACTIVE_STATUSES.has(status)
}

export function isTerminalStatus(status: RunStatus): boolean {
  return TERMINAL_STATUSES.has(status)
}

export function statusColor(status: RunStatus): string {
  switch (status) {
    case 'queued':
      return 'default'
    case 'starting':
    case 'running':
      return 'processing'
    case 'verifying':
      return 'purple'
    case 'succeeded':
      return 'success'
    case 'failed':
    case 'timed_out':
      return 'error'
    case 'cancelled':
      return 'warning'
    case 'interrupted':
      return 'default'
    case 'stagnated':
      return 'warning'
  }
}
