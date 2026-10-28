import { supportsEventSchema, type EventRecord } from '@/api/types'
import { sortedBySeq } from './merge'

export interface BranchNode {
  id: string
  parentId?: string
  kind?: 'root' | 'hypothesis' | 'review'
  depth: number
  topic: string
  goal?: string
  hypothesis?: string
  phase: 'open' | 'active' | 'closed'
  outcome?: 'pending' | 'supported' | 'refuted' | 'blocked' | 'inconclusive' | 'abandoned' | 'resolved'
  scope?: string
  priority?: number
  reviewOf?: string[]
  seq: number
}

export interface BranchAttempt {
  id: string
  branchId: string
  sessionId?: string
  phase: string
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
  scope: string
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

  for (const event of sortedBySeq(events)) {
    if (!supportsEventSchema(event.type, event.schema_version)) continue
    const envelope = asRecord(event.data)
    if (!envelope) continue
    switch (event.type) {
      case 'exploration.branch.created':
      case 'exploration.branch.updated': {
        const branch = parseBranch(asRecord(envelope.branch), event.seq)
        if (branch) {
          const previous = branches.get(branch.id)
          branches.set(branch.id, previous ? { ...previous, ...branch, seq: previous.seq } : branch)
        }
        break
      }
      case 'exploration.attempt.started':
      case 'exploration.attempt.completed':
      case 'exploration.attempt.failed':
      case 'attempt.started':
      case 'attempt.concluded':
      case 'attempt.failed': {
        const attempt = parseAttempt(asRecord(envelope.attempt), event.seq)
        if (attempt) attempts.set(attempt.id, attempt)
        break
      }
      case 'observation.created': {
        const observation = parseObservation(asRecord(envelope.observation), event.seq)
        if (observation) observations.set(observation.id, observation)
        break
      }
      case 'knowledge.promoted':
      case 'knowledge.supported':
      case 'knowledge.contested':
      case 'knowledge.refuted': {
        const item = parseKnowledge(asRecord(envelope.knowledge), event.seq)
        if (item) knowledge.set(item.id, item)
        break
      }
      case 'artifact.stored': {
        const artifact = parseArtifact(asRecord(envelope.artifact), event.seq)
        if (artifact) artifacts.set(`${artifact.id}:${event.seq}`, artifact)
        break
      }
      case 'authority.progress':
      case 'authority.completed': {
        const completion = asRecord(envelope.completion)
        if (completion && typeof completion.progress === 'number' && typeof completion.total === 'number') {
          progress = { current: completion.progress, total: completion.total }
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

function parseBranch(record: Record<string, unknown> | undefined, seq: number): BranchNode | undefined {
  if (!record || typeof record.id !== 'string' || !record.id || typeof record.topic !== 'string') return undefined
  const phase = record.phase === 'open' || record.phase === 'active' || record.phase === 'closed' ? record.phase : 'open'
  return {
    id: record.id,
    parentId: typeof record.parent_id === 'string' ? record.parent_id : undefined,
    kind: record.kind === 'root' || record.kind === 'hypothesis' || record.kind === 'review' ? record.kind : undefined,
    depth: typeof record.depth === 'number' ? record.depth : 0,
    topic: record.topic,
    goal: typeof record.goal === 'string' ? record.goal : undefined,
    hypothesis: typeof record.hypothesis === 'string' ? record.hypothesis : undefined,
    phase,
    outcome: branchOutcome(record.outcome),
    scope: typeof record.scope === 'string' ? record.scope : undefined,
    priority: typeof record.priority === 'number' ? record.priority : undefined,
    reviewOf: stringArray(record.review_of),
    seq,
  }
}

function parseAttempt(record: Record<string, unknown> | undefined, seq: number): BranchAttempt | undefined {
  const intentId = typeof record?.intent_id === 'string' ? record.intent_id : undefined
  const branchId = typeof record?.branch_id === 'string' ? record.branch_id : intentId
  if (!record || typeof record.id !== 'string' || !branchId) return undefined
  return {
    id: record.id,
    branchId,
    sessionId: typeof record.session_id === 'string' ? record.session_id : undefined,
    phase: typeof record.phase === 'string' ? record.phase : 'running',
    outcome: typeof record.outcome === 'string' ? record.outcome : undefined,
    error: typeof record.error === 'string' ? record.error : undefined,
    seq,
  }
}

function parseObservation(record: Record<string, unknown> | undefined, seq: number): BranchObservation | undefined {
  const intentId = typeof record?.intent_id === 'string' ? record.intent_id : undefined
  const branchId = typeof record?.branch_id === 'string' ? record.branch_id : intentId
  if (!record || typeof record.id !== 'string' || !branchId || typeof record.summary !== 'string') return undefined
  return {
    id: record.id,
    branchId,
    attemptId: typeof record.attempt_id === 'string' ? record.attempt_id : undefined,
    summary: record.summary,
    kind: typeof record.kind === 'string' ? record.kind : undefined,
    seq,
  }
}

function parseKnowledge(record: Record<string, unknown> | undefined, seq: number): BranchKnowledge | undefined {
  if (!record || typeof record.id !== 'string' || typeof record.topic !== 'string' || typeof record.kind !== 'string' || typeof record.statement !== 'string' || typeof record.trust !== 'string') {
    return undefined
  }
  return {
    id: record.id,
    topic: record.topic,
    kind: record.kind,
    statement: record.statement,
    trust: record.trust,
    scope: typeof record.scope === 'string' ? record.scope : 'task',
    version: typeof record.version === 'number' ? record.version : undefined,
    contradicts: stringArray(record.contradicts),
    seq,
  }
}

function parseArtifact(record: Record<string, unknown> | undefined, seq: number): BranchArtifact | undefined {
  if (!record || typeof record.id !== 'string' || typeof record.name !== 'string') return undefined
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

function branchOutcome(value: unknown): BranchNode['outcome'] {
  return value === 'pending' || value === 'supported' || value === 'refuted' || value === 'blocked' || value === 'inconclusive' || value === 'abandoned' || value === 'resolved'
    ? value
    : undefined
}

function stringArray(value: unknown): string[] | undefined {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : undefined
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined
}
