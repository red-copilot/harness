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
  max_depth: number
  max_nodes: number
  max_children_per_branch: number
  max_active_branches: number
  max_attempts_per_branch: number
  max_tool_calls_per_attempt: number
  max_wall_time_per_attempt: number
  stagnation_rounds: number
}

export interface RunDescriptor {
  schema_version: number
  task: Task
  target: Target
  planner: AgentSpec
  executors: AgentSpec[]
  policy?: ExplorationPolicy
  budget?: Budget
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

export interface Branch {
  id: string
  parent_id?: string
  run_id: string
  task_id: string
  kind: 'root' | 'hypothesis' | 'review'
  depth: number
  topic: string
  goal?: string
  hypothesis?: string
  success_condition?: string
  failure_condition?: string
  scope?: string
  review_of?: string[]
  status: string
  priority?: number
  created_at?: string
  updated_at?: string
}

export interface Attempt {
  id: string
  run_id: string
  branch_id: string
  session_id?: string
  status: string
  outcome?: string
  observation_ids?: string[]
  started_at?: string
  finished_at?: string
  error?: string
}

export interface Observation {
  id: string
  run_id: string
  branch_id: string
  attempt_id: string
  kind: string
  summary: string
  target?: string
  evidence?: Artifact[]
  created_at?: string
}

export interface KnowledgeSource {
  run_id: string
  attempt_id: string
  session_id: string
  observation: string
}

export interface Knowledge {
  id: string
  task_id: string
  run_id: string
  topic: string
  kind: string
  statement: string
  scope: string
  target?: string
  trust: 'observed' | 'corroborated' | 'contested' | 'refuted'
  reuse: 'stable' | 'instance' | 'never'
  sources?: KnowledgeSource[]
  contradicts?: string[]
  valid_from?: string
  last_validated?: string
  expires_at?: string
  version?: number
}

export interface RunResult {
  run_id: string
  status: RunStatus
  verdict?: Verdict
  branches?: Branch[]
  attempts?: Attempt[]
  observations?: Observation[]
  knowledge?: Knowledge[]
  artifacts?: Artifact[]
  sessions?: SessionResult[]
  usage?: Usage
  error?: string
  started_at?: string
  finished_at?: string
}

export interface RunRecord {
  id: string
  status: RunStatus
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
  type: string
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
