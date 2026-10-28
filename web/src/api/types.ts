import type { components } from './generated'

export const RUN_STATUSES = [
  'queued',
  'provisioning',
  'running',
  'finalizing',
  'verifying',
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
  'interrupted',
  'stagnated',
] as const

export type RunStatus = (typeof RUN_STATUSES)[number]

export const RUN_PHASES = ['queued', 'provisioning', 'running', 'finalizing', 'verifying', 'terminal'] as const
export type RunPhase = (typeof RUN_PHASES)[number]

export const RUN_OUTCOMES = ['succeeded', 'failed', 'cancelled', 'timed_out', 'interrupted', 'stagnated'] as const
export type RunOutcome = (typeof RUN_OUTCOMES)[number]

export const EVENT_TYPES = [
  'run.created',
  'run.phase',
  'run.provisioned',
  'run.finalized',
  'agent.session.started',
  'agent.event',
  'artifact.stored',
  'observation.created',
  'authority.progress',
  'authority.completed',
  'authority.observe_error',
  'claim.created',
  'hint.created',
  'reason.started',
  'reason.completed',
  'reason.rejected',
  'intent.created',
  'intent.claimed',
  'intent.released',
  'intent.concluded',
  'intent.abandoned',
  'intent.rejected',
  'intent.attempt.started',
  'intent.attempt.concluded',
  'intent.attempt.failed',
  'context.truncated',
  'attempt.started',
  'attempt.concluded',
  'attempt.failed',
] as const
export type EventType = (typeof EVENT_TYPES)[number]

export const EVENT_SCHEMA_VERSIONS: Record<EventType, number> = {
  'run.created': 1,
  'run.phase': 1,
  'run.provisioned': 1,
  'run.finalized': 1,
  'agent.session.started': 1,
  'agent.event': 2,
  'artifact.stored': 1,
  'observation.created': 1,
  'authority.progress': 1,
  'authority.completed': 1,
  'authority.observe_error': 1,
  'claim.created': 1,
  'hint.created': 1,
  'reason.started': 1,
  'reason.completed': 1,
  'reason.rejected': 1,
  'intent.created': 1,
  'intent.claimed': 1,
  'intent.released': 1,
  'intent.concluded': 1,
  'intent.abandoned': 1,
  'intent.rejected': 1,
  'intent.attempt.started': 1,
  'intent.attempt.concluded': 1,
  'intent.attempt.failed': 1,
  'context.truncated': 1,
  'attempt.started': 1,
  'attempt.concluded': 1,
  'attempt.failed': 1,
}

export function supportsEventSchema(type: string, version?: number): boolean {
  const current = EVENT_SCHEMA_VERSIONS[type as EventType]
  if (current === undefined) return false
  return (version ?? 1) <= current
}

export interface Mount {
  source: string
  destination: string
  read_only?: boolean
}

export interface Task {
  id: string
  objective: string
}

export type IsolationMode = 'caller_managed'

/** Per-Run execution detail. Never identity: the Target is its id. */
export interface TargetRuntime {
  container_name?: string
  network?: string
  compose_project?: string
  compose_file?: string
  compose_override?: string
  challenge_code?: string
  benchmark_base_url?: string
  hint?: string
  hint_used?: boolean
}

export interface Target {
  id: string
  addresses?: string[]
  isolation?: IsolationMode
}

export interface TargetBinding {
  target_id: string
  addresses?: string[]
  isolation?: IsolationMode
  runtime?: TargetRuntime
}

export interface ResourceSpec {
  cpus?: string
  memory?: string
  pids_limit?: number
}

export interface AgentSpec {
  id: string
  image?: string
  command?: string[]
  env?: Record<string, string>
  env_from?: string[]
  mounts?: Mount[]
  network?: string
  cap_add?: string[]
  resources?: ResourceSpec
  runtime?: unknown
}

export interface Budget {
  wall_time?: number
  max_tokens?: number
  max_model_calls?: number
  max_tool_calls?: number
  max_cost_usd?: number
}

export interface ExplorationPolicy {
  max_open_intents: number
  max_total_intents: number
  max_attempts_per_intent: number
  max_tool_calls_per_attempt: number
  max_wall_time_per_attempt: number
  conclude_timeout: number
  stagnation_rounds: number
  max_reasoner_tool_calls: number
  max_reasoner_wall_time: number
  max_reasoner_failures: number
  bootstrap_enabled?: boolean
}

export interface ContextPolicy {
  max_board_items: number
  max_board_bytes: number
  max_claim_bytes: number
  max_intent_bytes: number
  max_hint_bytes: number
  max_assistant_bytes: number
  max_report_bytes: number
}

export interface EvidencePolicy {
  max_report_bytes: number
  max_evidence_files_per_attempt: number
  max_artifact_bytes: number
  max_attempt_artifact_bytes: number
}

export interface RunPolicy {
  budget?: Budget
  exploration?: ExplorationPolicy
  context?: ContextPolicy
  evidence?: EvidencePolicy
}

export interface RunDescriptor {
  schema_version: number
  task: Task
  target: Target
  reasoner: AgentSpec
  executor: AgentSpec
  policy?: RunPolicy
}

export interface Usage {
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  model_calls?: number
  tool_calls?: number
  cost_usd?: number
}

export interface Verdict {
  success: boolean
  score?: number
  message?: string
  evidence?: unknown
}

export interface SessionResult {
  id: string
  status: RunStatus
  exit_code?: number
  usage?: Usage
  error?: string
}

export interface Artifact {
  id: string
  run_id: string
  attempt_id?: string
  session_id?: string
  name: string
  media_type?: string
  size?: number
  sha256?: string
  redacted?: boolean
  created_at?: string
}

export interface Observation {
  id: string
  run_id: string
  intent_id?: string
  attempt_id: string
  kind: string
  summary: string
  target?: string
  evidence?: Artifact[]
  created_at?: string
}

export interface RunResult {
  run_id: string
  phase?: RunPhase
  outcome?: RunOutcome
  verdict?: Verdict
  claims?: Claim[]
  intents?: Intent[]
  hints?: Hint[]
  intent_attempts?: IntentAttempt[]
  observations?: Observation[]
  artifacts?: Artifact[]
  sessions?: SessionResult[]
  usage?: Usage
  error?: string
  started_at?: string
  finished_at?: string
}

export type ClaimKind = 'origin' | 'goal' | 'finding' | 'negative'
export type IntentKind = 'bootstrap' | 'explore'
export type IntentState = 'open' | 'claimed' | 'concluded' | 'abandoned'
export type IntentAttemptPhase = 'running' | 'concluded' | 'failed' | 'cancelled' | 'timed_out'

export interface Claim {
  id: string
  run_id: string
  task_id: string
  kind: ClaimKind
  description: string
  source_intent_id?: string
  observation_ids?: string[]
  artifact_ids?: string[]
  created_at?: string
}

export interface Intent {
  id: string
  run_id: string
  kind: IntentKind
  from_claim_ids: string[]
  description: string
  state: IntentState
  slot?: number
  executor_id?: string
  attempt_ids?: string[]
  result_claim_id?: string
  created_at?: string
  claimed_at?: string
  concluded_at?: string
  error?: string
}

export interface Hint {
  id: string
  run_id: string
  content: string
  creator: string
  created_at?: string
}

export interface IntentAttempt {
  id: string
  run_id: string
  intent_id: string
  slot: number
  session_id: string
  phase: IntentAttemptPhase
  conclusion?: string
  started_at?: string
  finished_at?: string
  error?: string
}

type GeneratedBoard = components["schemas"]["BoardResponse"]
export type BoardResponse = Omit<GeneratedBoard, 'claims' | 'intents' | 'hints' | 'attempts' | 'observations' | 'artifacts' | 'slots' | 'board_revision' | 'reason'> & {
  claims: Claim[]
  intents: Intent[]
  hints: Hint[]
  attempts: IntentAttempt[]
  observations: Observation[]
  artifacts: Artifact[]
  slots: number[]
  board_revision: number
  reason: { active: boolean; frontier_version: number; free_slots?: number[] }
}

export interface RunRecord {
  id: string
  phase?: RunPhase
  outcome?: RunOutcome
  spec?: RunDescriptor | null
  result?: RunResult | null
  error?: string
  metadata?: unknown
  created_at: string
  started_at?: string | null
  finished_at?: string | null
}

export interface EventRecord {
  seq: number
  schema_version?: number
  run_id: string
  session_id?: string
  source: string
  type: EventType | string
  time: string
  data?: unknown
  raw?: unknown
}

export interface LogRecord {
  seq: number
  run_id: string
  session_id?: string
  stream: string
  time: string
  text: string
}

export interface SweepState {
  id: string
  status: RunStatus
  run_ids?: string[]
  results?: RunResult[]
  error?: string
  created_at: string
  finished_at?: string | null
}

export interface VerdictRecord {
  run_id: string
  success: boolean
  score?: number
  message?: string
  evidence?: unknown
  created_at: string
}

export interface RunOverview {
  total: number
  active: number
  terminal: number
  queued: number
  provisioning: number
  running: number
  finalizing: number
  verifying: number
  succeeded: number
  failed: number
  cancelled: number
  timed_out: number
  interrupted: number
  stagnated: number
  success_rate: number
}

export interface DurationOverview {
  samples: number
  average_ms: number
  p50_ms: number
  p95_ms: number
}

export interface Overview {
  generated_at: string
  window: number
  runs: RunOverview
  usage: Usage
  duration: DurationOverview
}

export interface HealthResponse { status: string }
export interface LogQuery { afterSeq?: number; limit?: number; tail?: boolean }
