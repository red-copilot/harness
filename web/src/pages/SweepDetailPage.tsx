import ArrowLeftOutlined from '@ant-design/icons/ArrowLeftOutlined'
import ReloadOutlined from '@ant-design/icons/ReloadOutlined'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Descriptions, Result, Space, Table, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError, getSweep } from '@/api/client'
import type { RunResult } from '@/api/types'
import { EmptyState } from '@/components/EmptyState'
import { PageError } from '@/components/PageError'
import { StatusTag } from '@/components/StatusTag'
import { TimeText } from '@/components/TimeText'
import { useI18n } from '@/i18n/I18nContext'
import { formatCost, formatTokens } from '@/utils/format'
import { isActiveStatus } from '@/utils/status'

export default function SweepDetailPage() {
  const { sweepId } = useParams()
  const { t } = useI18n()
  const navigate = useNavigate()
  const query = useQuery({
    queryKey: ['sweep', sweepId],
    queryFn: ({ signal }) => getSweep(sweepId!, signal),
    enabled: Boolean(sweepId),
    refetchInterval: (state) => {
      const sweep = state.state.data
      return sweep && isActiveStatus(sweep.status) ? 2000 : false
    },
  })
  if (query.isError) {
    if (query.error instanceof ApiError && query.error.status === 404) {
      return <Result status="404" title={t('sweeps.notFound')} extra={<Button onClick={() => navigate('/sweeps')}>{t('common.backToSweeps')}</Button>} />
    }
    return <PageError error={query.error} onRetry={() => void query.refetch()} />
  }
  if (!query.data) return <Card loading title={t('common.loading')} />
  const sweep = query.data
  return (
    <div className="page-stack">
      <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/sweeps')}>{t('common.backToSweeps')}</Button>
      <div className="detail-heading">
        <div className="detail-title-row">
          <Typography.Title level={2} className="mono-text">{sweep.id}</Typography.Title>
          <StatusTag status={sweep.status} />
          <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>{t('common.refresh')}</Button>
        </div>
      </div>
      {sweep.error ? <Alert type="error" showIcon title={t('sweeps.error')} description={sweep.error} /> : null}
      <Card title={t('sweeps.detailTitle')}>
        <Descriptions column={{ xs: 1, md: 2, xl: 4 }} size="small">
          <Descriptions.Item label={t('common.status')}><StatusTag status={sweep.status} /></Descriptions.Item>
          <Descriptions.Item label={t('common.runCount')}>{sweep.run_ids?.length ?? 0}</Descriptions.Item>
          <Descriptions.Item label={t('common.resultCount')}>{sweep.results?.length ?? 0}</Descriptions.Item>
          <Descriptions.Item label={t('common.created')}><TimeText value={sweep.created_at} /></Descriptions.Item>
          <Descriptions.Item label={t('common.finished')}><TimeText value={sweep.finished_at} /></Descriptions.Item>
        </Descriptions>
      </Card>
      <Card title={t('sweeps.results')}>
        {sweep.results?.length ? <SweepResults results={sweep.results} /> : <EmptyState description={t('sweeps.noResults')} />}
      </Card>
      {sweep.run_ids?.length ? (
        <Card title={t('sweeps.runs')}>
          <Space wrap>
            {sweep.run_ids.map((runId) => (
              <Button key={runId} type="link" onClick={() => navigate(`/runs/${runId}`)}>
                <Typography.Text code>{runId}</Typography.Text>
              </Button>
            ))}
          </Space>
        </Card>
      ) : null}
    </div>
  )
}

function SweepResults({ results }: { results: RunResult[] }) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const columns: ColumnsType<RunResult> = [
    {
      title: t('common.runId'),
      dataIndex: 'run_id',
      render: (runId: string) => <Typography.Text code>{runId}</Typography.Text>,
    },
    { title: t('common.status'), dataIndex: 'status', render: (status: RunResult['status']) => <StatusTag status={status} /> },
    {
      title: t('common.verdict'),
      key: 'verdict',
      render: (_, result) => result.verdict ? (
        <Typography.Text type={result.verdict.success ? 'success' : 'danger'}>
          {result.verdict.success ? t('status.succeeded') : t('status.failed')}
        </Typography.Text>
      ) : '—',
    },
    { title: t('common.score'), key: 'score', align: 'right', render: (_, result) => result.verdict?.score ?? '—' },
    { title: t('common.tokens'), key: 'tokens', align: 'right', render: (_, result) => formatTokens(result.usage?.total_tokens) },
    { title: t('common.cost'), key: 'cost', align: 'right', render: (_, result) => formatCost(result.usage?.cost_usd) },
    { title: t('common.duration'), key: 'duration', render: (_, result) => result.started_at ? formatRunDurationFromResult(result) : '—' },
    { title: t('common.errorLabel'), dataIndex: 'error', render: (value?: string) => value || '—' },
    {
      title: '',
      key: 'view',
      render: (_, result) => <Button type="link" onClick={(event) => { event.stopPropagation(); navigate(`/runs/${result.run_id}`) }}>{t('common.view')}</Button>,
    },
  ]
  return (
    <Table
      rowKey="run_id"
      size="small"
      columns={columns}
      dataSource={results}
      pagination={false}
      scroll={{ x: 1100 }}
      onRow={(result) => ({ onClick: () => navigate(`/runs/${result.run_id}`), className: 'clickable-row' })}
    />
  )
}

function formatRunDurationFromResult(result: RunResult): string {
  if (!result.started_at || !result.finished_at) return '—'
  const start = new Date(result.started_at).getTime()
  const end = new Date(result.finished_at).getTime()
  if (Number.isNaN(start) || Number.isNaN(end)) return '—'
  const ms = end - start
  if (ms < 1000) return `${ms} ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`
  return `${(ms / 60000).toFixed(1)} min`
}
