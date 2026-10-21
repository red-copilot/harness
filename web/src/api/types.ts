export const RUN_STATUSES = [
  'queued',
  'starting',
  'running',
  'verifying',
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
  'interrupted',
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
  inputs?: Mount[]
  knowledge?: Mount[]
  skills?: Mount[]
  targets?: Target[]
  metadata?: Record<string, string>
}

export interface Target {
  id?: string
  addresses?: string[]
  metadata?: Record<string, string>
}

export interface Budget {
  wall_time?: number
  max_tokens?: number
  max_model_calls?: number
  max_tool_calls?: number
  max_cost_usd?: number
}

export interface PiSpec {
  provider?: string
  model?: string
  thinking?: string
  tools?: string[]
  extra_args?: string[]
  env_passthrough?: string[]
  system_prompt?: string
  skills?: string[]
  image?: string
  models_json?: string
}

export interface AgentSpec {
  id: string
  image?: string
  command?: string[]
  env?: Record<string, string>
  env_from?: string[]
  knowledge?: Mount[]
  skills?: Mount[]
  network?: string
  cap_add?: string[]
  pi?: PiSpec
}

export interface RunSpec {
  task: Task
  target?: Target
  agents: AgentSpec[]
  budget?: Budget
  secret_env?: string[]
  workspace_root?: string
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

export interface RunResult {
  run_id: string
  status: RunStatus
  verdict?: Verdict
  sessions?: SessionResult[]
  usage?: Usage
  error?: string
  started_at?: string
  finished_at?: string
}

export interface RunRecord {
  id: string
  status: RunStatus
  spec?: RunSpec | null
  result?: RunResult | null
  error?: string
  sweep_id?: string
  metadata?: unknown
  created_at: string
  started_at?: string | null
  finished_at?: string | null
}

export interface EventRecord {
  seq: number
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

export interface SweepState {
  id: string
  status: RunStatus
  run_ids?: string[]
  results?: RunResult[]
  error?: string
  created_at: string
  finished_at?: string | null
}

export interface RunOverview {
  total: number
  active: number
  terminal: number
  queued: number
  starting: number
  running: number
  verifying: number
  succeeded: number
  failed: number
  cancelled: number
  timed_out: number
  interrupted: number
  success_rate: number
}

export interface DurationOverview {
  samples: number
  average_ms: number
  p50_ms: number
  p95_ms: number
}

export interface SweepOverview {
  total: number
  active: number
  succeeded: number
  failed: number
}

export interface Overview {
  generated_at: string
  window: number
  runs: RunOverview
  usage: Usage
  duration: DurationOverview
  sweeps: SweepOverview
}

export interface HealthResponse {
  status: string
}

export interface LogQuery {
  afterSeq?: number
  limit?: number
  tail?: boolean
}
