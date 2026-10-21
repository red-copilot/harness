import type {
  EventRecord,
  HealthResponse,
  LogQuery,
  LogRecord,
  Overview,
  RunRecord,
  VerdictRecord,
} from './types'

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: 'application/json', ...init?.headers },
  })
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try {
      const body = (await response.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // Keep the status-derived message for non-JSON failures.
    }
    throw new ApiError(response.status, message)
  }
  return (await response.json()) as T
}

export function getHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return request<HealthResponse>('/healthz', { signal, cache: 'no-store' })
}

export function getOverview(signal?: AbortSignal): Promise<Overview> {
  return request<Overview>('/v1/overview', { signal, cache: 'no-store' })
}

export function getRuns(signal?: AbortSignal): Promise<RunRecord[]> {
  return request<RunRecord[]>('/v1/runs', { signal, cache: 'no-store' })
}

export function getRun(runId: string, signal?: AbortSignal): Promise<RunRecord> {
  return request<RunRecord>(`/v1/runs/${encodeURIComponent(runId)}`, { signal, cache: 'no-store' })
}

export async function getVerdict(
  runId: string,
  signal?: AbortSignal,
): Promise<VerdictRecord | null> {
  try {
    return await request<VerdictRecord>(
      `/v1/runs/${encodeURIComponent(runId)}/verdict`,
      { signal, cache: 'no-store' },
    )
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null
    throw error
  }
}

export function getLogs(
  runId: string,
  query: LogQuery = {},
  signal?: AbortSignal,
): Promise<LogRecord[]> {
  const params = new URLSearchParams()
  if (query.afterSeq && query.afterSeq > 0) params.set('after_seq', String(query.afterSeq))
  if (query.limit) params.set('limit', String(query.limit))
  if (query.tail) params.set('tail', 'true')
  const suffix = params.size > 0 ? `?${params.toString()}` : ''
  return request<LogRecord[]>(`/v1/runs/${encodeURIComponent(runId)}/logs${suffix}`, {
    signal,
    cache: 'no-store',
  })
}

export interface RunEventStreamHandlers {
  onEvent: (event: EventRecord) => void
  onState: (state: 'connecting' | 'live' | 'reconnecting' | 'closed') => void
}

export async function fetchRunEventsPage(
  runId: string,
  afterSeq: number,
  signal: AbortSignal,
): Promise<Response> {
  const response = await fetch(
    `/v1/runs/${encodeURIComponent(runId)}/events?after_seq=${afterSeq}`,
    { headers: { Accept: 'text/event-stream' }, signal, cache: 'no-store' },
  )
  if (!response.ok) {
    throw new ApiError(response.status, `event stream failed: ${response.statusText}`)
  }
  return response
}
