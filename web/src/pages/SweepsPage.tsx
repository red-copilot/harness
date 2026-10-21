import ReloadOutlined from '@ant-design/icons/ReloadOutlined'
import SearchOutlined from '@ant-design/icons/SearchOutlined'
import { useQuery } from '@tanstack/react-query'
import { Button, Card, Input, Select, Space, Typography } from 'antd'
import { useMemo, useState } from 'react'
import { getSweeps } from '@/api/client'
import { RUN_STATUSES } from '@/api/types'
import type { RunStatus, SweepState } from '@/api/types'
import { EmptyState } from '@/components/EmptyState'
import { PageError } from '@/components/PageError'
import { PageHeader } from '@/components/PageHeader'
import { SweepTable } from '@/components/SweepTable'
import { useI18n } from '@/i18n/I18nContext'
import type { MessageKey } from '@/i18n/messages'

export default function SweepsPage() {
  const { t } = useI18n()
  const [statuses, setStatuses] = useState<RunStatus[]>([])
  const [search, setSearch] = useState('')
  const sweeps = useQuery({ queryKey: ['sweeps'], queryFn: ({ signal }) => getSweeps(signal), refetchInterval: 5000 })
  const filtered = useMemo(() => filterSweeps(sweeps.data ?? [], statuses, search), [sweeps.data, statuses, search])
  return (
    <div className="page-stack">
      <PageHeader
        title={t('sweeps.title')}
        description={t('sweeps.description')}
        extra={<Button icon={<ReloadOutlined />} onClick={() => void sweeps.refetch()}>{t('common.refresh')}</Button>}
      />
      <Card>
        <Space orientation="vertical" size="middle" className="full-width">
          <Space wrap className="filter-row">
            <Select
              mode="multiple"
              allowClear
              maxTagCount="responsive"
              placeholder={t('sweeps.statusFilter')}
              value={statuses}
              onChange={setStatuses}
              options={RUN_STATUSES.map((status) => ({ value: status, label: t(`status.${status}` as MessageKey) }))}
              className="status-filter"
            />
            <Input allowClear prefix={<SearchOutlined />} placeholder={t('sweeps.searchPlaceholder')}
              value={search} onChange={(event) => setSearch(event.target.value)} className="search-input" />
            <Typography.Text type="secondary">{filtered.length} / {sweeps.data?.length ?? 0}</Typography.Text>
          </Space>
          {sweeps.isError ? (
            <PageError error={sweeps.error} onRetry={() => void sweeps.refetch()} />
          ) : sweeps.isLoading ? (
            <SweepTable sweeps={[]} loading />
          ) : sweeps.data?.length === 0 ? (
            <EmptyState description={t('sweeps.noSweeps')} />
          ) : filtered.length === 0 ? (
            <EmptyState description={t('sweeps.noMatches')} />
          ) : (
            <SweepTable sweeps={filtered} loading={sweeps.isLoading} />
          )}
        </Space>
      </Card>
    </div>
  )
}

function filterSweeps(sweeps: SweepState[], statuses: RunStatus[], search: string): SweepState[] {
  const needle = search.trim().toLowerCase()
  return sweeps.filter((sweep) => {
    if (statuses.length && !statuses.includes(sweep.status)) return false
    return !needle || sweep.id.toLowerCase().includes(needle)
  })
}
