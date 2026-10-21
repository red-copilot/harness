import ArrowLeftOutlined from '@ant-design/icons/ArrowLeftOutlined'
import CopyOutlined from '@ant-design/icons/CopyOutlined'
import DownloadOutlined from '@ant-design/icons/DownloadOutlined'
import PauseOutlined from '@ant-design/icons/PauseOutlined'
import PlayCircleOutlined from '@ant-design/icons/PlayCircleOutlined'
import ReloadOutlined from '@ant-design/icons/ReloadOutlined'
import SearchOutlined from '@ant-design/icons/SearchOutlined'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  App as AntdApp,
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Progress,
  Result,
  Row,
  Select,
  Space,
  Statistic,
  Switch,
  Table,
  Tabs,
  Timeline,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useCallback, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ApiError, getRun, getVerdict } from '@/api/client'
import type {
  EventRecord,
  LogRecord,
  RunRecord,
  SessionResult,
  Usage,
  VerdictRecord,
} from '@/api/types'
import { EmptyState } from '@/components/EmptyState'
import { ExplorationTimeline } from '@/components/ExplorationTimeline'
import { JsonViewer } from '@/components/JsonViewer'
import { LogViewer } from '@/components/LogViewer'
import { PageError } from '@/components/PageError'
import { StatusTag } from '@/components/StatusTag'
import { TimeText } from '@/components/TimeText'
import { useRunEvents } from '@/hooks/useRunEvents'
import { useRunLogs } from '@/hooks/useRunLogs'
import { useNow } from '@/hooks/useNow'
import { useI18n } from '@/i18n/I18nContext'
import {
  agentIds,
  formatCost,
  formatDuration,
  formatRunDuration,
  formatTokens,
  formatWallTimeNs,
  totalUsage,
} from '@/utils/format'
import { prettyJson } from '@/utils/json'
import { isActiveStatus, isTerminalStatus } from '@/utils/status'

type RunTab = 'overview' | 'exploration' | 'events' | 'logs' | 'verdict'

export default function RunDetailPage() {
  const { runId } = useParams()
  const { t } = useI18n()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const [paused, setPaused] = useState(false)
  const rawTab = searchParams.get('tab')
  const tab: RunTab = isRunTab(rawTab) ? rawTab : 'overview'

  const runQuery = useQuery({
    queryKey: ['run', runId],
    queryFn: ({ signal }) => getRun(runId!, signal),
    enabled: Boolean(runId),
    refetchInterval: (query) => {
      const data = query.state.data as RunRecord | undefined
      return data && isTerminalStatus(data.status) ? false : 2000
    },
  })
  const run = runQuery.data
  const live = run ? isActiveStatus(run.status) : false
  const onFinished = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ['run', runId] })
    void queryClient.invalidateQueries({ queryKey: ['verdict', runId] })
    void queryClient.invalidateQueries({ queryKey: ['runs'] })
    void queryClient.invalidateQueries({ queryKey: ['overview'] })
  }, [queryClient, runId])
  const eventsState = useRunEvents(runId, paused, onFinished, Boolean(run && isTerminalStatus(run.status)))
  const logsState = useRunLogs(runId, live)
  const verdictQuery = useQuery({
    queryKey: ['verdict', runId],
    queryFn: ({ signal }) => getVerdict(runId!, signal),
    enabled: Boolean(runId),
    refetchInterval: live ? 5000 : false,
  })

  if (runQuery.isError) {
    if (runQuery.error instanceof ApiError && runQuery.error.status === 404) {
      return (
        <Result
          status="404"
          title={t('run.notFound')}
          extra={<Button onClick={() => navigate('/runs')}>{t('common.backToRuns')}</Button>}
        />
      )
    }
    return <PageError error={runQuery.error} onRetry={() => void runQuery.refetch()} />
  }
  if (!run) {
    return <PageHeaderLoading title={t('run.title')} loading={t('common.loading')} />
  }

  const verdict = verdictQuery.data ?? fallbackVerdict(run)

  const setTab = (next: string) => {
    const params = new URLSearchParams(searchParams)
    params.set('tab', next)
    setSearchParams(params, { replace: true })
  }

  return (
    <div className="page-stack">
      <div className="detail-heading">
        <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/runs')}>
          {t('common.backToRuns')}
        </Button>
        <div className="detail-title-row">
          <Typography.Title level={2} className="mono-text">
            {run.id}
          </Typography.Title>
          <StatusTag status={run.status} />
          {live ? <Badge status="processing" text={t('common.live')} /> : null}
          <Button
            type="text"
            icon={<CopyOutlined />}
            aria-label={t('common.copy')}
            onClick={() => void navigator.clipboard.writeText(run.id)}
          />
          <Button icon={<ReloadOutlined />} onClick={() => void runQuery.refetch()}>
            {t('common.refresh')}
          </Button>
        </div>
        <Space separator="·" wrap className="detail-meta">
          <span>{run.spec?.task?.id ?? '—'}</span>
          <span>{formatRunDuration(run)}</span>
          <TimeText value={run.created_at} />
        </Space>
      </div>

      <Tabs
        className="run-detail-tabs"
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'overview',
            label: t('run.tab.overview'),
            children: <RunOverview run={run} verdict={verdict} />,
          },
          {
            key: 'exploration',
            label: t('run.tab.exploration'),
            children: <ExplorationTimeline events={eventsState.events} />,
          },
          {
            key: 'events',
            label: `${t('run.tab.events')} (${eventsState.events.length})`,
            children: (
              <RunEvents
                events={eventsState.events}
                streamState={eventsState.streamState}
                streamError={eventsState.streamError}
                paused={paused}
                onPausedChange={setPaused}
              />
            ),
          },
          {
            key: 'logs',
            label: `${t('run.tab.logs')} (${logsState.logs.length})`,
            children: (
              <RunLogs
                logs={logsState.logs}
                loading={logsState.loading}
                error={logsState.error}
                truncated={logsState.truncated}
                limit={logsState.limit}
              />
            ),
          },
          {
            key: 'verdict',
            label: t('run.tab.verdict'),
            children: <RunVerdict verdict={verdict} />,
          },
        ]}
      />
    </div>
  )
}

function RunOverview({ run, verdict }: { run: RunRecord; verdict: VerdictRecord | null }) {
  const { t } = useI18n()
  const [specOpen, setSpecOpen] = useState(false)
  const usage = effectiveUsage(run)
  const address = run.spec?.target?.addresses?.join(', ') || run.spec?.target?.id || '—'
  return (
    <div className="page-stack">
      {run.error ? <Alert type="error" showIcon title={t('common.errorLabel')} description={run.error} /> : null}
      <Card
        title={t('run.taskObjective')}
        extra={<Button onClick={() => setSpecOpen(true)}>{t('run.rawSpec')}</Button>}
      >
        <Typography.Paragraph className="objective-text">
          {run.spec?.task?.objective || '—'}
        </Typography.Paragraph>
        <Descriptions column={{ xs: 1, sm: 2, xl: 3 }} size="small">
          <Descriptions.Item label={t('common.runId')}>
            <Typography.Text code>{run.id}</Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label={t('common.status')}>
            <StatusTag status={run.status} />
          </Descriptions.Item>
          <Descriptions.Item label={t('common.task')}>{run.spec?.task?.id ?? '—'}</Descriptions.Item>
          <Descriptions.Item label={t('common.sweepId')}>{run.sweep_id || '—'}</Descriptions.Item>
          <Descriptions.Item label={t('common.agents')}>
            {agentIds(run.spec).join(', ') || '—'}
          </Descriptions.Item>
          <Descriptions.Item label={t('common.target')}>{address}</Descriptions.Item>
        </Descriptions>
      </Card>

      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card title={t('run.runTiming')}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label={t('common.created')}><TimeText value={run.created_at} /></Descriptions.Item>
              <Descriptions.Item label={t('common.started')}><TimeText value={run.started_at} /></Descriptions.Item>
              <Descriptions.Item label={t('common.finished')}><TimeText value={run.finished_at} /></Descriptions.Item>
              <Descriptions.Item label={t('common.duration')}>{formatRunDuration(run)}</Descriptions.Item>
            </Descriptions>
            <Timeline
              className="run-timeline"
              items={[
                { color: 'gray', content: <span>{t('common.created')} · <TimeText value={run.created_at} /></span> },
                { color: run.started_at ? 'blue' : 'gray', content: <span>{t('common.started')} · <TimeText value={run.started_at} /></span> },
                { color: run.status === 'succeeded' ? 'green' : run.finished_at ? 'red' : 'gray', content: <span>{t('common.finished')} · <TimeText value={run.finished_at} /></span> },
                ...(verdict ? [{ color: verdict.success ? 'green' : 'red', content: <span>{t('common.verdict')} · {verdict.success ? t('run.verdictSuccess') : t('run.verdictFailure')}</span> }] : []),
              ]}
            />
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card title={t('run.usageSummary')}>
            <Row gutter={[12, 12]}>
              <Col span={8}><Statistic title={t('common.inputTokens')} value={usage.input_tokens ?? 0} /></Col>
              <Col span={8}><Statistic title={t('common.outputTokens')} value={usage.output_tokens ?? 0} /></Col>
              <Col span={8}><Statistic title={t('common.totalTokens')} value={usage.total_tokens ?? 0} /></Col>
              <Col span={8}><Statistic title={t('common.modelCalls')} value={usage.model_calls ?? 0} /></Col>
              <Col span={8}><Statistic title={t('common.toolCalls')} value={usage.tool_calls ?? 0} /></Col>
              <Col span={8}><Statistic title={t('common.cost')} value={formatCost(usage.cost_usd)} /></Col>
            </Row>
          </Card>
        </Col>
      </Row>

      <Card title={t('run.budgetProgress')}>
        <BudgetProgress run={run} usage={usage} />
      </Card>

      <Card title={t('run.sessions')}>
        <SessionsTable sessions={run.result?.sessions ?? []} />
      </Card>

      <Card title={t('common.verdict')}>
        <VerdictSummary verdict={verdict} />
      </Card>

      <Drawer title={t('run.rawSpec')} size={760} open={specOpen} onClose={() => setSpecOpen(false)}>
        <JsonViewer value={run.spec} />
      </Drawer>
    </div>
  )
}

function BudgetProgress({ run, usage }: { run: RunRecord; usage: Usage }) {
  const { t } = useI18n()
  const budget = run.spec?.budget
  const wallLimit = budget?.wall_time ?? 0
  const started = run.started_at ? new Date(run.started_at).getTime() : 0
  const now = useNow(!run.finished_at)
  const ended = run.finished_at ? new Date(run.finished_at).getTime() : now
  const elapsed = started ? Math.max(0, ended - started) : 0
  return (
    <div className="budget-grid">
      <div>
        <div className="budget-label">
          <span>{t('run.wallTime')}</span>
          <span>{formatWallTimeNs(wallLimit)}</span>
        </div>
        <Progress
          percent={wallLimit > 0 ? Math.min(100, (elapsed / (wallLimit / 1_000_000)) * 100) : 0}
          status={wallLimit > 0 && elapsed > wallLimit / 1_000_000 ? 'exception' : 'normal'}
          format={() => formatDuration(elapsed)}
        />
      </div>
      <div>
        <div className="budget-label">
          <span>{t('run.maxTokens')}</span>
          <span>{budget?.max_tokens ? formatTokens(budget.max_tokens) : '—'}</span>
        </div>
        <Progress
          percent={budget?.max_tokens ? Math.min(100, ((usage.total_tokens ?? 0) / budget.max_tokens) * 100) : 0}
          status={budget?.max_tokens && (usage.total_tokens ?? 0) > budget.max_tokens ? 'exception' : 'normal'}
          format={() => formatTokens(usage.total_tokens)}
        />
      </div>
      <div>
        <div className="budget-label">
          <span>{t('run.maxCost')}</span>
          <span>{budget?.max_cost_usd ? formatCost(budget.max_cost_usd) : '—'}</span>
        </div>
        <Progress
          percent={budget?.max_cost_usd ? Math.min(100, ((usage.cost_usd ?? 0) / budget.max_cost_usd) * 100) : 0}
          status={budget?.max_cost_usd && (usage.cost_usd ?? 0) > budget.max_cost_usd ? 'exception' : 'normal'}
          format={() => formatCost(usage.cost_usd)}
        />
      </div>
    </div>
  )
}

function SessionsTable({ sessions }: { sessions: SessionResult[] }) {
  const { t } = useI18n()
  if (sessions.length === 0) return <EmptyState description={t('run.noSessions')} />
  const columns: ColumnsType<SessionResult> = [
    { title: t('common.session'), dataIndex: 'id', render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
    { title: t('common.status'), dataIndex: 'status', render: (value: SessionResult['status']) => <StatusTag status={value} /> },
    { title: 'Exit', dataIndex: 'exit_code', width: 90, render: (value?: number) => value ?? '—' },
    { title: t('common.totalTokens'), key: 'tokens', align: 'right', render: (_, session) => formatTokens(session.usage?.total_tokens) },
    { title: t('common.cost'), key: 'cost', align: 'right', render: (_, session) => formatCost(session.usage?.cost_usd) },
    { title: t('common.errorLabel'), dataIndex: 'error', render: (value?: string) => value || '—' },
  ]
  return <Table rowKey="id" size="small" columns={columns} dataSource={sessions} pagination={false} scroll={{ x: 800 }} />
}

function RunEvents({
  events,
  streamState,
  streamError,
  paused,
  onPausedChange,
}: {
  events: EventRecord[]
  streamState: 'connecting' | 'live' | 'reconnecting' | 'closed' | 'paused'
  streamError: string | null
  paused: boolean
  onPausedChange: (paused: boolean) => void
}) {
  const { t } = useI18n()
  const [search, setSearch] = useState('')
  const [source, setSource] = useState<string[]>([])
  const [session, setSession] = useState<string[]>([])
  const [selected, setSelected] = useState<EventRecord | null>(null)
  const sources = unique(events.map((event) => event.source))
  const sessions = unique(events.map((event) => event.session_id).filter(Boolean) as string[])
  const filtered = useMemo(() => filterEvents(events, search, source, session), [events, search, source, session])
  const columns: ColumnsType<EventRecord> = [
    { title: t('common.sequence'), dataIndex: 'seq', width: 80, render: (value: number) => <span className="mono-text">{value}</span> },
    { title: t('common.time'), dataIndex: 'time', width: 190, render: (value: string) => <TimeText value={value} /> },
    { title: t('common.source'), dataIndex: 'source', width: 100 },
    { title: t('common.session'), dataIndex: 'session_id', width: 130, render: (value?: string) => value || '—' },
    {
      title: t('common.type'),
      dataIndex: 'type',
      width: 220,
      render: (value: string) => <Typography.Text code>{value}</Typography.Text>,
    },
    {
      title: t('run.eventData'),
      dataIndex: 'data',
      render: (value: unknown) => <span className="event-preview">{eventPreview(value)}</span>,
    },
  ]
  const stateText =
    streamState === 'live'
      ? t('run.sseLive')
      : streamState === 'reconnecting'
        ? t('run.sseReconnecting')
        : streamState === 'connecting'
          ? t('run.sseConnecting')
          : streamState === 'paused'
            ? t('common.paused')
            : t('run.sseClosed')
  return (
    <div className="page-stack">
      {streamError ? <Alert type="warning" showIcon title={t('error.sseDisconnected')} description={streamError} /> : null}
      <Card>
        <Space orientation="vertical" size="middle" className="full-width">
          <Space wrap className="filter-row">
            <Badge status={streamState === 'live' ? 'processing' : streamState === 'reconnecting' ? 'warning' : 'default'} text={stateText} />
            <Button
              icon={paused ? <PlayCircleOutlined /> : <PauseOutlined />}
              onClick={() => onPausedChange(!paused)}
            >
              {paused ? t('common.resume') : t('common.pause')}
            </Button>
            <Select mode="multiple" allowClear placeholder={t('run.allSources')} value={source} onChange={setSource}
              options={sources.map((value) => ({ value, label: value }))} className="compact-filter" />
            <Select mode="multiple" allowClear placeholder={t('run.allSessions')} value={session} onChange={setSession}
              options={sessions.map((value) => ({ value, label: value }))} className="compact-filter" />
            <Input allowClear prefix={<SearchOutlined />} placeholder={t('run.eventSearchPlaceholder')}
              value={search} onChange={(event) => setSearch(event.target.value)} className="search-input" />
            <Typography.Text type="secondary">{filtered.length} {t('common.events')}</Typography.Text>
          </Space>
          {filtered.length === 0 ? (
            <EmptyState description={events.length === 0 ? t('run.noEvents') : t('common.noMatches')} />
          ) : (
            <Table
              rowKey="seq"
              size="small"
              columns={columns}
              dataSource={filtered}
              pagination={{ pageSize: 50, showSizeChanger: false }}
              scroll={{ x: 1050 }}
              onRow={(event) => ({ onClick: () => setSelected(event), className: 'clickable-row' })}
            />
          )}
        </Space>
      </Card>
      <Drawer title={`#${selected?.seq ?? ''} ${selected?.type ?? ''}`} size={760} open={Boolean(selected)} onClose={() => setSelected(null)}>
        {selected ? (
          <Tabs
            items={[
              { key: 'data', label: t('run.eventData'), children: <JsonViewer value={selected.data} /> },
              { key: 'raw', label: t('run.eventRaw'), children: <JsonViewer value={selected.raw} /> },
            ]}
          />
        ) : null}
      </Drawer>
    </div>
  )
}

function RunLogs({
  logs,
  loading,
  error,
  truncated,
  limit,
}: {
  logs: LogRecord[]
  loading: boolean
  error: string | null
  truncated: boolean
  limit: number
}) {
  const { t } = useI18n()
  const { message } = AntdApp.useApp()
  const [search, setSearch] = useState('')
  const [session, setSession] = useState<string[]>([])
  const [stream, setStream] = useState<string[]>([])
  const [follow, setFollow] = useState(true)
  const sessions = unique(logs.map((log) => log.session_id).filter(Boolean) as string[])
  const streams = unique(logs.map((log) => log.stream))
  const filtered = useMemo(() => filterLogs(logs, search, session, stream), [logs, search, session, stream])
  const visibleText = filtered.map(logLine).join('\n')
  const copyVisible = async () => {
    try {
      await navigator.clipboard.writeText(visibleText)
      message.success(t('common.copied'))
    } catch {
      message.error(t('common.error'))
    }
  }
  const download = () => {
    const blob = new Blob([visibleText], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'harness-logs.txt'
    anchor.click()
    URL.revokeObjectURL(url)
  }
  return (
    <div className="page-stack">
      {truncated ? <Alert type="info" showIcon title={t('run.truncatedLogs', { limit })} /> : null}
      {error ? <Alert type="warning" showIcon title={t('common.error')} description={error} /> : null}
      <Card>
        <Space orientation="vertical" size="middle" className="full-width">
          <Space wrap className="filter-row">
            <Select mode="multiple" allowClear placeholder={t('run.allSessions')} value={session} onChange={setSession}
              options={sessions.map((value) => ({ value, label: value }))} className="compact-filter" />
            <Select mode="multiple" allowClear placeholder={t('run.allStreams')} value={stream} onChange={setStream}
              options={streams.map((value) => ({ value, label: value }))} className="compact-filter" />
            <Input allowClear prefix={<SearchOutlined />} placeholder={t('run.logSearchPlaceholder')}
              value={search} onChange={(event) => setSearch(event.target.value)} className="search-input" />
            <Space>
              <Switch checked={follow} onChange={setFollow} />
              <Typography.Text>{t('run.autoFollow')}</Typography.Text>
            </Space>
            <Button icon={<CopyOutlined />} onClick={() => void copyVisible()}>{t('run.copyVisible')}</Button>
            <Button icon={<DownloadOutlined />} onClick={download}>{t('run.downloadLogs')}</Button>
            <Typography.Text type="secondary">{filtered.length} {t('common.lines')}</Typography.Text>
          </Space>
          {loading && logs.length === 0 ? (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('common.loading')} />
          ) : filtered.length === 0 ? (
            <EmptyState description={logs.length === 0 ? t('run.noLogs') : t('common.noMatches')} />
          ) : (
            <LogViewer logs={filtered} follow={follow} onFollowChange={setFollow} />
          )}
        </Space>
      </Card>
    </div>
  )
}

function RunVerdict({ verdict }: { verdict: VerdictRecord | null }) {
  const { t } = useI18n()
  if (!verdict) {
    return (
      <Card>
        <EmptyState description={t('run.noVerdict')} />
        <Alert type="info" showIcon title={t('run.verdictPending')} description={t('run.authoritativeNote')} />
      </Card>
    )
  }
  return (
    <div className="page-stack">
      <Alert
        type={verdict.success ? 'success' : 'error'}
        showIcon
        title={verdict.success ? t('run.verdictSuccess') : t('run.verdictFailure')}
        description={t('run.authoritativeNote')}
      />
      <Card>
        <Row gutter={[24, 24]}>
          <Col xs={24} md={6}><Statistic title={t('run.score')} value={verdict.score ?? 0} precision={2} /></Col>
          <Col xs={24} md={18}>
            <Typography.Text type="secondary">{t('common.message')}</Typography.Text>
            <Typography.Paragraph className="verdict-message">{verdict.message || '—'}</Typography.Paragraph>
            <Typography.Text type="secondary">{t('common.created')}: <TimeText value={verdict.created_at} /></Typography.Text>
          </Col>
        </Row>
      </Card>
      <JsonViewer title={t('run.evidenceJson')} value={verdict.evidence} />
    </div>
  )
}

function VerdictSummary({ verdict }: { verdict: VerdictRecord | null }) {
  const { t } = useI18n()
  if (!verdict) return <Alert type="info" showIcon title={t('run.noVerdict')} description={t('run.verdictPending')} />
  return (
    <Alert
      type={verdict.success ? 'success' : 'error'}
      showIcon
      title={verdict.success ? t('run.verdictSuccess') : t('run.verdictFailure')}
      description={verdict.message || t('run.authoritativeNote')}
    />
  )
}

function effectiveUsage(run: RunRecord): Usage {
  if (run.result?.usage) return run.result.usage
  return totalUsage(run.result?.sessions?.map((session) => session.usage) ?? [])
}

function fallbackVerdict(run: RunRecord): VerdictRecord | null {
  const verdict = run.result?.verdict
  if (!verdict) return null
  return {
    run_id: run.id,
    success: verdict.success,
    score: verdict.score,
    message: verdict.message,
    evidence: verdict.evidence,
    created_at: run.finished_at ?? run.created_at,
  }
}

function filterEvents(events: EventRecord[], search: string, sources: string[], sessions: string[]): EventRecord[] {
  const needle = search.trim().toLowerCase()
  return events.filter((event) => {
    if (sources.length && !sources.includes(event.source)) return false
    if (sessions.length && (!event.session_id || !sessions.includes(event.session_id))) return false
    if (!needle) return true
    return [event.type, event.source, event.session_id, prettyJson(event.data), prettyJson(event.raw)]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
      .includes(needle)
  })
}

function filterLogs(logs: LogRecord[], search: string, sessions: string[], streams: string[]): LogRecord[] {
  const needle = search.trim().toLowerCase()
  return logs.filter((log) => {
    if (sessions.length && (!log.session_id || !sessions.includes(log.session_id))) return false
    if (streams.length && !streams.includes(log.stream)) return false
    if (!needle) return true
    return `${log.text} ${log.session_id ?? ''} ${log.stream}`.toLowerCase().includes(needle)
  })
}

function logLine(log: LogRecord): string {
  return `${log.seq}\t${log.time}\t${log.session_id ?? '-'}\t${log.stream}\t${log.text}`
}

function eventPreview(value: unknown): string {
  const text = prettyJson(value).replace(/\s+/g, ' ')
  return text.length > 160 ? `${text.slice(0, 160)}…` : text
}

function unique(values: string[]): string[] {
  return [...new Set(values)].sort()
}

function isRunTab(value: string | null): value is RunTab {
  return value === 'overview' || value === 'exploration' || value === 'events' || value === 'logs' || value === 'verdict'
}

function PageHeaderLoading({ title, loading }: { title: string; loading: string }) {
  return (
    <div className="page-stack">
      <Typography.Title level={2}>{title}</Typography.Title>
      <Card loading title={loading} />
    </div>
  )
}
