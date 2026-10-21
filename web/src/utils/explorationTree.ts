import type { EventRecord } from '@/api/types'

export interface BranchNode {
  id: string
  parentId?: string
  depth: number
  topic: string
  goal?: string
  hypothesis?: string
  status: string
  scope?: string
  priority?: number
  seq: number
}

export interface BranchAttempt {
  id: string
  branchId: string
  sessionId?: string
  status: string
  outcome?: string
  error?: string
  seq: number
}

export interface BranchObservation {
  id: string
  branchId: string
  attemptId?: string
  summary: string
  kind?: string
  seq: number
}

export interface BranchKnowledge {
  id: string
  topic: string
  kind: string
  statement: string
  trust: string
  reuse: string
  seq: number
}

export interface BranchProgress {
  current: number
  total: number
}

export interface BranchView {
  branches: BranchNode[]
  attempts: BranchAttempt[]
  observations: BranchObservation[]
  knowledge: BranchKnowledge[]
  progress?: BranchProgress
}

export function extractBranchView(events: EventRecord[]): BranchView {
  const branches = new Map<string, BranchNode>()
  const attempts = new Map<string, BranchAttempt>()
  const observations = new Map<string, BranchObservation>()
  const knowledge = new Map<string, BranchKnowledge>()
  let progress: BranchProgress | undefined

  for (const event of [...events].sort((a, b) => a.seq - b.seq)) {
    const record = asRecord(event.data)
    if (!record) continue
    switch (event.type) {
      case 'exploration.branch.created': {
        const branch = parseBranch(record, event.seq)
        if (branch) branches.set(branch.id, branch)
        break
      }
      case 'exploration.branch.closed': {
        if (typeof record.id !== 'string') break
        const branch = branches.get(record.id)
        if (branch) branch.status = typeof record.status === 'string' ? record.status : 'abandoned'
        break
      }
      case 'exploration.attempt.started':
      case 'exploration.attempt.completed':
      case 'exploration.attempt.failed': {
        const attempt = parseAttempt(record, event.seq)
        if (attempt) attempts.set(attempt.id, attempt)
        break
      }
      case 'exploration.observation.recorded': {
        const observation = parseObservation(record, event.seq)
        if (observation) observations.set(observation.id, observation)
        break
      }
      case 'exploration.knowledge.promoted': {
        const item = parseKnowledge(record, event.seq)
        if (item) knowledge.set(item.id, item)
        break
      }
      case 'backend.progress':
      case 'exploration.progress.updated': {
        if (typeof record.progress === 'number' && typeof record.total === 'number') {
          progress = { current: record.progress, total: record.total }
        }
        break
      }
      default:
        break
    }
  }

  return {
    branches: [...branches.values()].sort((a, b) => a.depth - b.depth || a.seq - b.seq || a.id.localeCompare(b.id)),
    attempts: [...attempts.values()].sort((a, b) => a.seq - b.seq),
    observations: [...observations.values()].sort((a, b) => a.seq - b.seq),
    knowledge: [...knowledge.values()].sort((a, b) => a.seq - b.seq),
    progress,
  }
}

function parseBranch(record: Record<string, unknown>, seq: number): BranchNode | undefined {
  if (typeof record.id !== 'string' || !record.id || typeof record.topic !== 'string') return undefined
  return {
    id: record.id,
    parentId: typeof record.parent_id === 'string' ? record.parent_id : undefined,
    depth: typeof record.depth === 'number' ? record.depth : 0,
    topic: record.topic,
    goal: typeof record.goal === 'string' ? record.goal : undefined,
    hypothesis: typeof record.hypothesis === 'string' ? record.hypothesis : undefined,
    status: typeof record.status === 'string' ? record.status : 'open',
    scope: typeof record.scope === 'string' ? record.scope : undefined,
    priority: typeof record.priority === 'number' ? record.priority : undefined,
    seq,
  }
}

function parseAttempt(record: Record<string, unknown>, seq: number): BranchAttempt | undefined {
  if (typeof record.id !== 'string' || typeof record.branch_id !== 'string') return undefined
  return {
    id: record.id,
    branchId: record.branch_id,
    sessionId: typeof record.session_id === 'string' ? record.session_id : undefined,
    status: typeof record.status === 'string' ? record.status : 'running',
    outcome: typeof record.outcome === 'string' ? record.outcome : undefined,
    error: typeof record.error === 'string' ? record.error : undefined,
    seq,
  }
}

function parseObservation(record: Record<string, unknown>, seq: number): BranchObservation | undefined {
  if (typeof record.id !== 'string' || typeof record.branch_id !== 'string' || typeof record.summary !== 'string') return undefined
  return {
    id: record.id,
    branchId: record.branch_id,
    attemptId: typeof record.attempt_id === 'string' ? record.attempt_id : undefined,
    summary: record.summary,
    kind: typeof record.kind === 'string' ? record.kind : undefined,
    seq,
  }
}

function parseKnowledge(record: Record<string, unknown>, seq: number): BranchKnowledge | undefined {
  if (
    typeof record.id !== 'string' ||
    typeof record.topic !== 'string' ||
    typeof record.kind !== 'string' ||
    typeof record.statement !== 'string' ||
    typeof record.trust !== 'string' ||
    typeof record.reuse !== 'string'
  ) {
    return undefined
  }
  return {
    id: record.id,
    topic: record.topic,
    kind: record.kind,
    statement: record.statement,
    trust: record.trust,
    reuse: record.reuse,
    seq,
  }
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined
}
