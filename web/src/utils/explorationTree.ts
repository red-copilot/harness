import type { EventRecord } from '@/api/types'

export interface BranchNode {
  id: string
  parentId?: string
  kind?: 'root' | 'hypothesis' | 'review'
  depth: number
  topic: string
  goal?: string
  hypothesis?: string
  status: string
  scope?: string
  priority?: number
  reviewOf?: string[]
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
  version?: number
  contradicts?: string[]
  seq: number
}

export interface BranchArtifact {
  id: string
  attemptId?: string
  name: string
  mediaType?: string
  size?: number
  sha256?: string
  redacted?: boolean
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
  artifacts: BranchArtifact[]
  progress?: BranchProgress
}

export function extractBranchView(events: EventRecord[]): BranchView {
  const branches = new Map<string, BranchNode>()
  const attempts = new Map<string, BranchAttempt>()
  const observations = new Map<string, BranchObservation>()
  const knowledge = new Map<string, BranchKnowledge>()
  const artifacts = new Map<string, BranchArtifact>()
  let progress: BranchProgress | undefined

  for (const event of [...events].sort((a, b) => a.seq - b.seq)) {
    const record = asRecord(event.data)
    if (!record) continue
    switch (event.type) {
      case 'exploration.branch.created':
      case 'exploration.branch.updated': {
        const branch = parseBranch(record, event.seq)
        if (branch) {
          const previous = branches.get(branch.id)
          branches.set(branch.id, previous ? { ...previous, ...branch, seq: previous.seq } : branch)
        }
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
      case 'exploration.observation.created':
      case 'exploration.observation.recorded': {
        const observation = parseObservation(record, event.seq)
        if (observation) observations.set(observation.id, observation)
        break
      }
      case 'knowledge.promoted':
      case 'exploration.knowledge.promoted': {
        const item = parseKnowledge(record, event.seq)
        if (item) knowledge.set(item.id, item)
        break
      }
      case 'artifact.stored': {
        const artifact = parseArtifact(record, event.seq)
        if (artifact) artifacts.set(`${artifact.id}:${event.seq}`, artifact)
        break
      }
      case 'backend.progress':
      case 'backend.completed':
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
    artifacts: [...artifacts.values()].sort((a, b) => a.seq - b.seq),
    progress,
  }
}

function parseBranch(record: Record<string, unknown>, seq: number): BranchNode | undefined {
  if (typeof record.id !== 'string' || !record.id || typeof record.topic !== 'string') return undefined
  return {
    id: record.id,
    parentId: typeof record.parent_id === 'string' ? record.parent_id : undefined,
    kind: record.kind === 'root' || record.kind === 'hypothesis' || record.kind === 'review' ? record.kind : undefined,
    depth: typeof record.depth === 'number' ? record.depth : 0,
    topic: record.topic,
    goal: typeof record.goal === 'string' ? record.goal : undefined,
    hypothesis: typeof record.hypothesis === 'string' ? record.hypothesis : undefined,
    status: typeof record.status === 'string' ? record.status : 'open',
    scope: typeof record.scope === 'string' ? record.scope : undefined,
    priority: typeof record.priority === 'number' ? record.priority : undefined,
    reviewOf: Array.isArray(record.review_of) ? record.review_of.filter((value): value is string => typeof value === 'string') : undefined,
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
    version: typeof record.version === 'number' ? record.version : undefined,
    contradicts: Array.isArray(record.contradicts) ? record.contradicts.filter((value): value is string => typeof value === 'string') : undefined,
    seq,
  }
}

function parseArtifact(record: Record<string, unknown>, seq: number): BranchArtifact | undefined {
  if (typeof record.id !== 'string' || typeof record.name !== 'string') return undefined
  return {
    id: record.id,
    attemptId: typeof record.attempt_id === 'string' ? record.attempt_id : undefined,
    name: record.name,
    mediaType: typeof record.media_type === 'string' ? record.media_type : undefined,
    size: typeof record.size === 'number' ? record.size : undefined,
    sha256: typeof record.sha256 === 'string' ? record.sha256 : undefined,
    redacted: typeof record.redacted === 'boolean' ? record.redacted : undefined,
    seq,
  }
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined
}
