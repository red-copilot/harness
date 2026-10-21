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

export interface HypothesisTarget {
  id: string
  addresses: string[]
}

export interface HypothesisFrontier {
  id: string
  parentId?: string
  question: string
  targetHint: string
  probeKey: string
  params: string[]
  expectedEvidence: string
  failureEvidence: string
  maxTools?: number
  maxDurationSeconds?: number
  status: string
  round?: number
  fingerprint?: string
  executorId?: string
  anchors: string[]
  seq: number
}

export interface HypothesisFact {
  id: string
  claim: string
  evidence: string
  trust: string
  contested: boolean
  targets: string[]
  sourceItemId: string
  round?: number
  seq: number
}

export interface ExecutorToolEvent {
  seq: number
  tool: string
  detail: string
  error: boolean
  running: boolean
}

export interface ExecutorActivity {
  itemId: string
  executorId: string
  tools: ExecutorToolEvent[]
  lastSeq: number
}

export interface HypothesisView {
  attempts: HypothesisAttempt[]
  progress?: HypothesisProgress
  phase?: string
  objective?: string
  brief: Record<string, string>
  targets?: HypothesisTarget[]
  frontiers: HypothesisFrontier[]
  facts: HypothesisFact[]
  activity: Record<string, ExecutorActivity>
  current: string[]
}

interface PendingCycle {
  turn?: EventRecord
  state?: ExplorationStateSnapshot
  frontier?: ExplorationFrontierSnapshot
}

export function extractHypothesisView(events: EventRecord[]): HypothesisView {
  const attempts = new Map<string, HypothesisAttempt>()
  const frontiers = new Map<string, HypothesisFrontier>()
  const facts = new Map<string, HypothesisFact>()
  const activity: Record<string, ExecutorActivity> = {}
  const itemOfExecutor: Record<string, string> = {}
  let progress: HypothesisProgress | undefined
  let phase: string | undefined
  let objective: string | undefined
  let targets: HypothesisTarget[] | undefined
  let brief: Record<string, string> = {}
  const current = new Set<string>()

  for (const event of [...events].sort((a, b) => a.seq - b.seq)) {
    const record = asRecord(event.data)
    if (!record) continue
    if (event.type === 'tool_execution_start' || event.type === 'tool_execution_end') {
      const sessionId = event.session_id ?? ''
      const itemId = itemOfExecutor[sessionId]
      if (!itemId) continue
      const tool = typeof record.toolName === 'string' ? record.toolName : 'tool'
      const entry: ExecutorToolEvent = {
        seq: event.seq,
        tool,
        detail: toolDetail(record),
        error: record.isError === true,
        running: event.type === 'tool_execution_start',
      }
      const bucket = activity[itemId] ?? { itemId, executorId: sessionId, tools: [], lastSeq: event.seq }
      bucket.executorId = sessionId
      bucket.lastSeq = event.seq
      if (event.type === 'tool_execution_end') {
        for (let index = bucket.tools.length - 1; index >= 0; index -= 1) {
          const candidate = bucket.tools[index]
          if (candidate.running && candidate.tool === tool) {
            candidate.running = false
            candidate.error = entry.error
            break
          }
        }
      } else {
        bucket.tools.push(entry)
        if (bucket.tools.length > 24) bucket.tools = bucket.tools.slice(-24)
      }
      activity[itemId] = bucket
      continue
    }
    if (event.type === 'exploration.initialized') {
      if (typeof record.phase === 'string') phase = record.phase
      if (typeof record.objective === 'string') objective = record.objective
      brief = asStringRecord(record.brief)
      targets = asArray(record.targets).map(asHypothesisTarget).filter(isDefined)
      const initialProgress = asRecord(record.progress)
      if (initialProgress && typeof initialProgress.progress === 'number' && typeof initialProgress.total === 'number') {
        progress = { current: initialProgress.progress, total: initialProgress.total }
      }
      for (const item of asArray(record.frontiers).map(asHypothesisFrontier).filter(isDefined)) {
        frontiers.set(item.id, { ...item, seq: item.seq || event.seq })
      }
      continue
    }
    if (event.type === 'exploration.phase.changed') {
      if (typeof record.phase === 'string') phase = record.phase
      continue
    }
    if (event.type === 'exploration.frontiers.created') {
      for (const item of asArray(record.items).map(asHypothesisFrontier).filter(isDefined)) {
        const existing = frontiers.get(item.id)
        frontiers.set(item.id, { ...existing, ...item, seq: item.seq || existing?.seq || event.seq })
      }
      continue
    }
    if (event.type === 'backend.progress') {
      if (typeof record.progress === 'number' && typeof record.total === 'number') {
        progress = { current: record.progress, total: record.total }
      }
      continue
    }
    const itemId = typeof record.item_id === 'string' ? record.item_id : undefined
    if (!itemId) continue
    const frontier = frontiers.get(itemId) ?? {
      id: itemId,
      question: '',
      targetHint: '',
      probeKey: '',
      params: [],
      expectedEvidence: '',
      failureEvidence: '',
      status: 'open',
      anchors: [],
      seq: event.seq,
    }
    const fullItem = asHypothesisFrontier(record.item)
    if (fullItem) mergeHypothesisFrontier(frontier, fullItem)
    const attempt = attempts.get(itemId) ?? { seq: event.seq, itemId, status: 'assigned' }
    if (typeof record.round === 'number') attempt.round = record.round
    switch (event.type) {
      case 'exploration.item.assigned':
        if (typeof record.executor_id === 'string') {
          attempt.executorId = record.executor_id
          frontier.executorId = record.executor_id
          itemOfExecutor[record.executor_id as string] = itemId
        }
        frontier.status = 'assigned'
        current.add(itemId)
        break
      case 'exploration.executor.started':
        phase = 'executing'
        current.add(itemId)
        frontier.status = 'assigned'
        if (typeof record.executor_id === 'string') {
          frontier.executorId = record.executor_id
          itemOfExecutor[record.executor_id as string] = itemId
        }
        break
      case 'exploration.report.received':
        if (typeof record.verdict === 'string') attempt.verdict = record.verdict
        current.delete(itemId)
        break
      case 'exploration.facts.merged':
        attempt.status = 'done'
        frontier.status = 'done'
        attempt.facts = {
          added: stringArray(record.added),
          corroborated: stringArray(record.corroborated),
          contested: pairArray(record.contested),
        }
        for (const fact of asHypothesisFacts(record, itemId, event.seq)) {
          facts.set(fact.id, fact)
        }
        break
      case 'exploration.item.failed':
        attempt.status = 'failed'
        frontier.status = 'failed'
        current.delete(itemId)
        if (typeof record.error === 'string') attempt.error = record.error
        break
      default:
        continue
    }
    attempts.set(itemId, attempt)
    frontiers.set(itemId, frontier)
  }

  return {
    attempts: [...attempts.values()].sort((a, b) => b.seq - a.seq),
    progress,
    phase,
    objective,
    brief,
    targets,
    frontiers: [...frontiers.values()].sort(
      (a, b) => (a.round ?? 0) - (b.round ?? 0) || a.seq - b.seq || a.id.localeCompare(b.id),
    ),
    // Stable: facts keep the publication order of their facts.merged event.
    facts: [...facts.values()].sort((a, b) => a.seq - b.seq),
    activity,
    current: [...current],
  }
}

// asHypothesisFacts reads the fact content published with a facts.merged event.
// Older runs only published fact ids, so ids without content are kept as
// placeholders instead of being dropped.
function asHypothesisFacts(
  record: Record<string, unknown>,
  itemId: string,
  seq: number,
): HypothesisFact[] {
  const round = typeof record.round === 'number' ? record.round : undefined
  const published = asArray(record.facts).map(asRecord).filter(isRecord)
  if (published.length > 0) {
    return published
      .map((fact) => {
        const id = typeof fact.id === 'string' ? fact.id : ''
        if (!id) return undefined
        return {
          id,
          claim: typeof fact.claim === 'string' ? fact.claim : '',
          evidence: typeof fact.evidence === 'string' ? fact.evidence : '',
          trust: typeof fact.trust === 'string' ? fact.trust : 'observed',
          contested: fact.contested === true,
          targets: stringArray(fact.targets),
          sourceItemId: itemId,
          round,
          seq,
        }
      })
      .filter(isDefined)
  }
  return [...stringArray(record.added), ...stringArray(record.corroborated)].map((id) => ({
    id,
    claim: '',
    evidence: '',
    trust: stringArray(record.corroborated).includes(id) ? 'corroborated' : 'observed',
    contested: false,
    targets: [],
    sourceItemId: itemId,
    round,
    seq,
  }))
}

function toolDetail(record: Record<string, unknown>): string {
  const args = asRecord(record.args)
  if (!args) return ''
  for (const key of ['command', 'path', 'url', 'pattern']) {
    const value = args[key]
    if (typeof value === 'string' && value.trim()) return truncate(value.trim(), 180)
  }
  return ''
}

function truncate(value: string, limit: number): string {
  return value.length > limit ? `${value.slice(0, limit)}…` : value
}

function isRecord(value: Record<string, unknown> | undefined): value is Record<string, unknown> {
  return value !== undefined
}

function mergeHypothesisFrontier(target: HypothesisFrontier, incoming: HypothesisFrontier): void {
  target.id = incoming.id
  if (incoming.parentId) target.parentId = incoming.parentId
  if (incoming.anchors.length) target.anchors = incoming.anchors
  if (incoming.question) target.question = incoming.question
  if (incoming.targetHint) target.targetHint = incoming.targetHint
  if (incoming.probeKey) target.probeKey = incoming.probeKey
  if (incoming.params.length) target.params = incoming.params
  if (incoming.expectedEvidence) target.expectedEvidence = incoming.expectedEvidence
  if (incoming.failureEvidence) target.failureEvidence = incoming.failureEvidence
  if (incoming.maxTools !== undefined) target.maxTools = incoming.maxTools
  if (incoming.maxDurationSeconds !== undefined) target.maxDurationSeconds = incoming.maxDurationSeconds
  if (incoming.status) target.status = incoming.status
  if (incoming.round !== undefined) target.round = incoming.round
  if (incoming.fingerprint) target.fingerprint = incoming.fingerprint
  if (incoming.executorId) target.executorId = incoming.executorId
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

function asStringRecord(value: unknown): Record<string, string> {
  const record = asRecord(value)
  if (!record) return {}
  const out: Record<string, string> = {}
  for (const [key, entry] of Object.entries(record)) {
    if (typeof entry === 'string') out[key] = entry
  }
  return out
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

function asHypothesisTarget(value: unknown): HypothesisTarget | undefined {
  const record = asRecord(value)
  if (!record || typeof record.id !== 'string') return undefined
  return { id: record.id, addresses: stringArray(record.addresses) }
}

function asHypothesisFrontier(value: unknown): HypothesisFrontier | undefined {
  const record = asRecord(value)
  if (!record || typeof record.id !== 'string' || typeof record.question !== 'string') return undefined
  return {
    id: record.id,
    parentId: typeof record.parent_id === 'string' && record.parent_id ? record.parent_id : undefined,
    anchors: stringArray(record.anchors),
    seq: typeof record.source_seq === 'number' ? record.source_seq : 0,
    question: record.question,
    targetHint: typeof record.target_hint === 'string' ? record.target_hint : '',
    probeKey: typeof record.probe_key === 'string' ? record.probe_key : '',
    params: stringArray(record.params),
    expectedEvidence: typeof record.expected_evidence === 'string' ? record.expected_evidence : '',
    failureEvidence: typeof record.failure_evidence === 'string' ? record.failure_evidence : '',
    maxTools: typeof record.max_tools === 'number' ? record.max_tools : undefined,
    maxDurationSeconds: typeof record.max_duration_seconds === 'number' ? record.max_duration_seconds : undefined,
    status: typeof record.status === 'string' ? record.status : 'open',
    round: typeof record.round === 'number' ? record.round : undefined,
    fingerprint: typeof record.fingerprint === 'string' ? record.fingerprint : undefined,
    executorId: typeof record.executor_id === 'string' ? record.executor_id : undefined,
  }
}

function isDefined<T>(value: T | undefined): value is T {
  return value !== undefined
}
