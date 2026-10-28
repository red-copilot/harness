import { Button, Table, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router-dom'
import type { RunRecord } from '@/api/types'
import { runStatus } from '@/utils/status'
import { useI18n } from '@/i18n/I18nContext'
import { agentIds, formatCost, formatRunDuration, formatTokens } from '@/utils/format'
import { StatusTag } from './StatusTag'
import { TimeText } from './TimeText'

export function RunTable({
  runs,
  loading,
  pagination = false,
  compact = false,
}: {
  runs: RunRecord[]
  loading?: boolean
  pagination?: false | { pageSize: number }
  compact?: boolean
}) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const columns: ColumnsType<RunRecord> = [
    {
      title: t('common.status'),
      dataIndex: 'status',
      width: 130,
      render: (_, run) => <StatusTag status={runStatus(run)} />,
    },
    {
      title: t('common.task'),
      key: 'task',
      width: compact ? 260 : 320,
      render: (_, run) => (
        <div className="task-cell">
          <Typography.Text strong ellipsis>
            {run.spec?.task?.id ?? '—'}
          </Typography.Text>
          <Typography.Text type="secondary" ellipsis>
            {run.spec?.task?.objective ?? run.error ?? '—'}
          </Typography.Text>
        </div>
      ),
    },
    {
      title: t('common.runId'),
      dataIndex: 'id',
      width: 250,
      render: (id: string) => (
        <Typography.Text code copyable className="mono-text">
          {id}
        </Typography.Text>
      ),
    },
    {
      title: t('common.agents'),
      key: 'agents',
      width: 130,
      render: (_, run) => agentIds(run.spec).join(', ') || '—',
    },
    {
      title: t('common.duration'),
      key: 'duration',
      width: 110,
      render: (_, run) => formatRunDuration(run),
    },
    {
      title: t('common.tokens'),
      key: 'tokens',
      width: 100,
      align: 'right',
      render: (_, run) => formatTokens(run.result?.usage?.total_tokens),
    },
    {
      title: t('common.cost'),
      key: 'cost',
      width: 100,
      align: 'right',
      render: (_, run) => formatCost(run.result?.usage?.cost_usd),
    },
    {
      title: t('common.created'),
      dataIndex: 'created_at',
      width: 180,
      render: (value: string) => <TimeText value={value} />,
    },
    {
      title: '',
      key: 'actions',
      width: 80,
      fixed: 'right',
      render: (_, run) => (
        <Button type="link" size="small" onClick={() => navigate(`/runs/${run.id}`)}>
          {t('common.view')}
        </Button>
      ),
    },
  ]
  return (
    <Table<RunRecord>
      rowKey="id"
      size="middle"
      loading={loading}
      columns={compact ? columns.filter((column) => column.key !== 'actions') : columns}
      dataSource={runs}
      pagination={pagination === false ? false : { pageSize: pagination.pageSize, showSizeChanger: false }}
      scroll={{ x: 1200 }}
      onRow={(run) => ({ onClick: () => navigate(`/runs/${run.id}`), className: 'clickable-row' })}
    />
  )
}
