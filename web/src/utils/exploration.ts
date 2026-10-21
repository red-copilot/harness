import type { EventRecord } from '@/api/types'

export interface ExplorationAttempt {
  event_seq?: number
  tools?: string[]
  execution_succeeded?: boolean
  error?: string
  observations?: number
}

export interface ExplorationObservation {
  event_seq?: number
  tool?: string
  text?: string
  error?: boolean
}

export interface FrontierItem {
  id: string
  kind: string
  text: string
  priority?: number
  status?: string
  source_seq?: number
}

export interface ExplorationStateSnapshot {
  revision: number
  attempts: ExplorationAttempt[]
  observations: ExplorationObservation[]
  items: FrontierItem[]
}

export interface ExplorationFrontierSnapshot {
  state_revision?: number
  recent_observations?: ExplorationObservation[]
  frontier: {
    items: FrontierItem[]
  }
  focus?: FrontierItem
}

export interface ExplorationCycle {
  turn: EventRecord
  state?: ExplorationStateSnapshot
  frontier?: ExplorationFrontierSnapshot
}

export interface HypothesisFacts {
  added: string[]
  corroborated: string[]
  contested: string[][]
}

export interface HypothesisAttempt {
  seq: number
  round?: number
  itemId: string
  executorId?: string
  verdict?: string
  status: 'assigned' | 'done' | 'failed'
  facts?: HypothesisFacts
  error?: string
}

export interface HypothesisProgress {
  current: number
  total: number
}

export interface HypothesisView {
  attempts: HypothesisAttempt[]
  progress?: HypothesisProgress
}

interface PendingCycle {
  turn?: EventRecord
  state?: ExplorationStateSnapshot
  frontier?: ExplorationFrontierSnapshot
}

export function extractHypothesisView(events: EventRecord[]): HypothesisView {
  const attempts = new Map<string, HypothesisAttempt>()
  let progress: HypothesisProgress | undefined

  for (const event of [...events].sort((a, b) => a.seq - b.seq)) {
    const record = asRecord(event.data)
    if (!record) continue
    if (event.type === 'backend.progress') {
      if (typeof record.progress === 'number' && typeof record.total === 'number') {
        progress = { current: record.progress, total: record.total }
      }
      continue
    }
    const itemId = typeof record.item_id === 'string' ? record.item_id : undefined
    if (!itemId) continue
    const attempt = attempts.get(itemId) ?? { seq: event.seq, itemId, status: 'assigned' }
    if (typeof record.round === 'number') attempt.round = record.round
    switch (event.type) {
      case 'exploration.item.assigned':
        if (typeof record.executor_id === 'string') attempt.executorId = record.executor_id
        break
      case 'exploration.report.received':
        if (typeof record.verdict === 'string') attempt.verdict = record.verdict
        break
      case 'exploration.facts.merged':
        attempt.status = 'done'
        attempt.facts = {
          added: stringArray(record.added),
          corroborated: stringArray(record.corroborated),
          contested: pairArray(record.contested),
        }
        break
      case 'exploration.item.failed':
        attempt.status = 'failed'
        if (typeof record.error === 'string') attempt.error = record.error
        break
      default:
        continue
    }
    attempts.set(itemId, attempt)
  }

  return {
    attempts: [...attempts.values()].sort((a, b) => b.seq - a.seq),
    progress,
  }
}

export function extractExplorationCycles(events: EventRecord[]): ExplorationCycle[] {
  const pending = new Map<string, PendingCycle>()
  const cycles: ExplorationCycle[] = []

  for (const event of [...events].sort((a, b) => a.seq - b.seq)) {
    const key = event.session_id || '__run__'
    const current = pending.get(key) ?? {}
    if (event.type === 'turn_end') {
      current.turn = event
      current.state = undefined
      current.frontier = undefined
      pending.set(key, current)
      continue
    }
    if (event.type === 'exploration.state.updated') {
      current.state = parseStateSnapshot(event.data)
      pending.set(key, current)
      continue
    }
    if (event.type !== 'exploration.frontier.updated') continue

    current.frontier = parseFrontierSnapshot(event.data)
    if (current.turn) {
      cycles.push({ turn: current.turn, state: current.state, frontier: current.frontier })
    }
    pending.delete(key)
  }

  return cycles.sort((a, b) => b.turn.seq - a.turn.seq)
}

function parseStateSnapshot(value: unknown): ExplorationStateSnapshot | undefined {
  const record = asRecord(value)
  if (!record || typeof record.revision !== 'number') return undefined
  return {
    revision: record.revision,
    attempts: asArray(record.attempts).map(asAttempt).filter(isDefined),
    observations: asArray(record.observations).map(asObservation).filter(isDefined),
    items: asArray(record.items).map(asFrontierItem).filter(isDefined),
  }
}

function parseFrontierSnapshot(value: unknown): ExplorationFrontierSnapshot | undefined {
  const record = asRecord(value)
  if (!record) return undefined
  const frontier = asRecord(record.frontier)
  if (!frontier) return undefined
  return {
    state_revision: typeof record.state_revision === 'number' ? record.state_revision : undefined,
    recent_observations: asArray(record.recent_observations).map(asObservation).filter(isDefined),
    frontier: { items: asArray(frontier.items).map(asFrontierItem).filter(isDefined) },
    focus: asFrontierItem(record.focus),
  }
}

function asAttempt(value: unknown): ExplorationAttempt | undefined {
  const record = asRecord(value)
  if (!record) return undefined
  return {
    event_seq: typeof record.event_seq === 'number' ? record.event_seq : undefined,
    tools: asArray(record.tools).filter((tool): tool is string => typeof tool === 'string'),
    execution_succeeded: typeof record.execution_succeeded === 'boolean' ? record.execution_succeeded : undefined,
    error: typeof record.error === 'string' ? record.error : undefined,
    observations: typeof record.observations === 'number' ? record.observations : undefined,
  }
}

function asObservation(value: unknown): ExplorationObservation | undefined {
  const record = asRecord(value)
  if (!record) return undefined
  return {
    event_seq: typeof record.event_seq === 'number' ? record.event_seq : undefined,
    tool: typeof record.tool === 'string' ? record.tool : undefined,
    text: typeof record.text === 'string' ? record.text : undefined,
    error: typeof record.error === 'boolean' ? record.error : undefined,
  }
}

function asFrontierItem(value: unknown): FrontierItem | undefined {
  const record = asRecord(value)
  if (!record || typeof record.id !== 'string' || typeof record.kind !== 'string' || typeof record.text !== 'string') {
    return undefined
  }
  return {
    id: record.id,
    kind: record.kind,
    text: record.text,
    priority: typeof record.priority === 'number' ? record.priority : undefined,
    status: typeof record.status === 'string' ? record.status : undefined,
    source_seq: typeof record.source_seq === 'number' ? record.source_seq : undefined,
  }
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : undefined
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function stringArray(value: unknown): string[] {
  return asArray(value).filter((item): item is string => typeof item === 'string')
}

function pairArray(value: unknown): string[][] {
  return asArray(value)
    .map((item) => stringArray(item))
    .filter((item) => item.length === 2)
}

function isDefined<T>(value: T | undefined): value is T {
  return value !== undefined
}
