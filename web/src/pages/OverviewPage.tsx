import CheckCircleOutlined from '@ant-design/icons/CheckCircleOutlined'
import ClockCircleOutlined from '@ant-design/icons/ClockCircleOutlined'
import DollarOutlined from '@ant-design/icons/DollarOutlined'
import ExclamationCircleOutlined from '@ant-design/icons/ExclamationCircleOutlined'
import FireOutlined from '@ant-design/icons/FireOutlined'
import ThunderboltOutlined from '@ant-design/icons/ThunderboltOutlined'
import { useQuery } from '@tanstack/react-query'
import { Button, Card, Col, Progress, Row, Space, Tag, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { getOverview, getRuns, getSweeps } from '@/api/client'
import type { RunStatus } from '@/api/types'
import { EmptyState } from '@/components/EmptyState'
import { MetricCard } from '@/components/MetricCard'
import { PageError } from '@/components/PageError'
import { PageHeader } from '@/components/PageHeader'
import { RunTable } from '@/components/RunTable'
import { SweepTable } from '@/components/SweepTable'
import { useI18n } from '@/i18n/I18nContext'
import type { MessageKey } from '@/i18n/messages'
import { formatCost, formatDuration, formatPercent, formatTokens } from '@/utils/format'
import { isActiveStatus } from '@/utils/status'

const STATUS_ORDER: RunStatus[] = [
  'running',
  'starting',
  'verifying',
  'queued',
  'succeeded',
  'failed',
  'timed_out',
  'cancelled',
  'interrupted',
]

const STATUS_CLASS: Record<RunStatus, string> = {
  queued: 'bar-queued',
  starting: 'bar-starting',
  running: 'bar-running',
  verifying: 'bar-verifying',
  succeeded: 'bar-succeeded',
  failed: 'bar-failed',
  cancelled: 'bar-cancelled',
  timed_out: 'bar-timed-out',
  interrupted: 'bar-interrupted',
}

export default function OverviewPage() {
  const { t } = useI18n()
  const navigate = useNavigate()
  const overview = useQuery({ queryKey: ['overview'], queryFn: ({ signal }) => getOverview(signal), refetchInterval: 5000 })
  const runs = useQuery({ queryKey: ['runs'], queryFn: ({ signal }) => getRuns(signal), refetchInterval: 5000 })
  const sweeps = useQuery({ queryKey: ['sweeps'], queryFn: ({ signal }) => getSweeps(signal), refetchInterval: 5000 })

  if (overview.isError) {
    return <PageError error={overview.error} onRetry={() => void overview.refetch()} />
  }
  if (!overview.data) {
    return <PageHeader title={t('overview.title')} description={t('common.loading')} />
  }

  const data = overview.data
  const statusCounts = statusCountsFromOverview(data.runs)
  const activeSweeps = (sweeps.data ?? []).filter((sweep) => isActiveStatus(sweep.status)).slice(0, 5)
  const recentSweeps = activeSweeps.length > 0 ? activeSweeps : (sweeps.data ?? []).slice(0, 5)

  return (
    <div className="page-stack">
      <PageHeader
        title={t('overview.title')}
        description={t('overview.description', { window: data.window })}
        extra={
          <Typography.Text type="secondary">
            {t('overview.windowNote', { window: data.window })}
          </Typography.Text>
        }
      />
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} xl={8}>
          <MetricCard title={t('overview.activeRuns')} value={data.runs.active} icon={<FireOutlined />} />
        </Col>
        <Col xs={24} sm={12} xl={8}>
          <MetricCard title={t('overview.successRate')} value={formatPercent(data.runs.success_rate)} icon={<CheckCircleOutlined />} />
        </Col>
        <Col xs={24} sm={12} xl={8}>
          <MetricCard title={t('overview.failedRuns')} value={data.runs.failed + data.runs.timed_out} icon={<ExclamationCircleOutlined />} />
        </Col>
        <Col xs={24} sm={12} xl={8}>
          <MetricCard title={t('overview.totalTokens')} value={formatTokens(data.usage.total_tokens)} icon={<ThunderboltOutlined />} />
        </Col>
        <Col xs={24} sm={12} xl={8}>
          <MetricCard title={t('overview.totalCost')} value={formatCost(data.usage.cost_usd)} icon={<DollarOutlined />} />
        </Col>
        <Col xs={24} sm={12} xl={8}>
          <MetricCard
            title={t('overview.avgDuration')}
            value={formatDuration(data.duration.average_ms)}
            hint={`P95 ${formatDuration(data.duration.p95_ms)}`}
            icon={<ClockCircleOutlined />}
          />
        </Col>
      </Row>

      <Card title={t('overview.statusDistribution')}>
        <div className="status-bar" aria-label={t('overview.statusDistribution')}>
          {STATUS_ORDER.map((status) => {
            const count = statusCounts[status]
            if (!count) return null
            return (
              <div
                key={status}
                className={`status-bar-segment ${STATUS_CLASS[status]}`}
                style={{ width: `${(count / Math.max(1, data.runs.total)) * 100}%` }}
                title={`${t(`status.${status}` as MessageKey)}: ${count}`}
              />
            )
          })}
        </div>
        <Space size={[8, 8]} wrap className="status-legend">
          {STATUS_ORDER.filter((status) => statusCounts[status] > 0).map((status) => (
            <Tag key={status} className={`legend-tag ${STATUS_CLASS[status]}`}>
              {t(`status.${status}` as MessageKey)} {statusCounts[status]}
            </Tag>
          ))}
        </Space>
        {data.runs.total === 0 ? <Progress percent={0} showInfo={false} /> : null}
      </Card>

      <Card
        title={t('overview.recentRuns')}
        extra={<Button type="link" onClick={() => navigate('/runs')}>{t('common.view')}</Button>}
      >
        {runs.isError ? (
          <PageError error={runs.error} onRetry={() => void runs.refetch()} />
        ) : runs.isLoading ? (
          <RunTable runs={[]} loading />
        ) : runs.data?.length ? (
          <RunTable runs={runs.data.slice(0, 10)} loading={runs.isLoading} compact />
        ) : (
          <EmptyState description={t('overview.noRuns')} />
        )}
      </Card>

      <Card
        title={t('overview.activeSweeps')}
        extra={<Button type="link" onClick={() => navigate('/sweeps')}>{t('common.view')}</Button>}
      >
        {sweeps.isError ? (
          <PageError error={sweeps.error} onRetry={() => void sweeps.refetch()} />
        ) : sweeps.isLoading ? (
          <SweepTable sweeps={[]} loading compact />
        ) : recentSweeps.length ? (
          <SweepTable sweeps={recentSweeps} loading={sweeps.isLoading} compact />
        ) : (
          <EmptyState description={t('overview.noSweeps')} />
        )}
      </Card>
    </div>
  )
}

function statusCountsFromOverview(runs: OverviewCounts): Record<RunStatus, number> {
  return {
    queued: runs.queued,
    starting: runs.starting,
    running: runs.running,
    verifying: runs.verifying,
    succeeded: runs.succeeded,
    failed: runs.failed,
    cancelled: runs.cancelled,
    timed_out: runs.timed_out,
    interrupted: runs.interrupted,
  }
}

interface OverviewCounts {
  queued: number
  starting: number
  running: number
  verifying: number
  succeeded: number
  failed: number
  cancelled: number
  timed_out: number
  interrupted: number
}
