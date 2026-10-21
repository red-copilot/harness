import ReloadOutlined from '@ant-design/icons/ReloadOutlined'
import SearchOutlined from '@ant-design/icons/SearchOutlined'
import { useQuery } from '@tanstack/react-query'
import { Button, Card, Input, Select, Space, Typography } from 'antd'
import { useMemo, useState } from 'react'
import { getRuns } from '@/api/client'
import { RUN_STATUSES } from '@/api/types'
import type { RunRecord, RunStatus } from '@/api/types'
import { EmptyState } from '@/components/EmptyState'
import { PageError } from '@/components/PageError'
import { PageHeader } from '@/components/PageHeader'
import { RunTable } from '@/components/RunTable'
import { useI18n } from '@/i18n/I18nContext'
import type { MessageKey } from '@/i18n/messages'
import { agentIds } from '@/utils/format'

export default function RunsPage() {
  const { t } = useI18n()
  const [statuses, setStatuses] = useState<RunStatus[]>([])
  const [search, setSearch] = useState('')
  const runs = useQuery({ queryKey: ['runs'], queryFn: ({ signal }) => getRuns(signal), refetchInterval: 5000 })
  const filtered = useMemo(
    () => filterRuns(runs.data ?? [], statuses, search),
    [runs.data, statuses, search],
  )

  return (
    <div className="page-stack">
      <PageHeader
        title={t('runs.title')}
        description={t('runs.description')}
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void runs.refetch()}>
            {t('common.refresh')}
          </Button>
        }
      />
      <Card>
        <Space orientation="vertical" size="middle" className="full-width">
          <Space wrap className="filter-row">
            <Select
              mode="multiple"
              allowClear
              maxTagCount="responsive"
              placeholder={t('runs.statusFilter')}
              value={statuses}
              onChange={setStatuses}
              options={RUN_STATUSES.map((status) => ({
                value: status,
                label: t(`status.${status}` as MessageKey),
              }))}
              className="status-filter"
            />
            <Input
              allowClear
              prefix={<SearchOutlined />}
              placeholder={t('runs.searchPlaceholder')}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              className="search-input"
            />
            <Typography.Text type="secondary">
              {t('runs.resultCount', { shown: filtered.length, total: runs.data?.length ?? 0 })}
            </Typography.Text>
          </Space>
          {runs.isError ? (
            <PageError error={runs.error} onRetry={() => void runs.refetch()} />
          ) : runs.isLoading ? (
            <RunTable runs={[]} loading pagination={{ pageSize: 20 }} />
          ) : runs.data?.length === 0 ? (
            <EmptyState description={t('runs.noRuns')} />
          ) : filtered.length === 0 ? (
            <EmptyState description={t('runs.noMatches')} />
          ) : (
            <RunTable runs={filtered} loading={runs.isLoading} pagination={{ pageSize: 20 }} />
          )}
        </Space>
      </Card>
    </div>
  )
}

function filterRuns(runs: RunRecord[], statuses: RunStatus[], search: string): RunRecord[] {
  const needle = search.trim().toLowerCase()
  return runs.filter((run) => {
    if (statuses.length > 0 && !statuses.includes(run.status)) return false
    if (!needle) return true
    const haystack = [
      run.id,
      run.sweep_id,
      run.error,
      run.spec?.task?.id,
      run.spec?.task?.objective,
      ...(run.spec?.target?.addresses ?? []),
      ...agentIds(run.spec),
    ]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return haystack.includes(needle)
  })
}
