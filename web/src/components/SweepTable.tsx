import { Button, Table, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router-dom'
import type { SweepState } from '@/api/types'
import { useI18n } from '@/i18n/I18nContext'
import { StatusTag } from './StatusTag'
import { TimeText } from './TimeText'

export function SweepTable({
  sweeps,
  loading,
  compact = false,
}: {
  sweeps: SweepState[]
  loading?: boolean
  compact?: boolean
}) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const columns: ColumnsType<SweepState> = [
    {
      title: t('common.status'),
      dataIndex: 'status',
      width: 130,
      render: (_, sweep) => <StatusTag status={sweep.status} />,
    },
    {
      title: t('common.sweepId'),
      dataIndex: 'id',
      render: (id: string) => (
        <Typography.Text code copyable className="mono-text">
          {id}
        </Typography.Text>
      ),
    },
    {
      title: t('common.challengeCount'),
      key: 'challenges',
      width: 130,
      align: 'right',
      render: (_, sweep) => sweep.results?.length ?? sweep.run_ids?.length ?? 0,
    },
    {
      title: t('common.runCount'),
      key: 'runs',
      width: 110,
      align: 'right',
      render: (_, sweep) => sweep.run_ids?.length ?? 0,
    },
    {
      title: t('common.resultCount'),
      key: 'results',
      width: 110,
      align: 'right',
      render: (_, sweep) => sweep.results?.length ?? 0,
    },
    {
      title: t('common.created'),
      dataIndex: 'created_at',
      width: 180,
      render: (value: string) => <TimeText value={value} />,
    },
    {
      title: t('common.finished'),
      dataIndex: 'finished_at',
      width: 180,
      render: (value: string | null | undefined) => <TimeText value={value} />,
    },
    {
      title: '',
      key: 'actions',
      width: 80,
      render: (_, sweep) => (
        <Button type="link" size="small" onClick={() => navigate(`/sweeps/${sweep.id}`)}>
          {t('common.view')}
        </Button>
      ),
    },
  ]
  return (
    <Table<SweepState>
      rowKey="id"
      size="middle"
      loading={loading}
      columns={compact ? columns.filter((column) => column.key !== 'actions' && column.key !== 'finished_at') : columns}
      dataSource={sweeps}
      pagination={compact ? false : { pageSize: 20, showSizeChanger: false }}
      scroll={{ x: 1000 }}
      onRow={(sweep) => ({ onClick: () => navigate(`/sweeps/${sweep.id}`), className: 'clickable-row' })}
    />
  )
}
