import { useMemo } from 'react'
import type { EventRecord } from '@/api/types'
import { BranchTreeTimeline } from '@/components/BranchTreeTimeline'
import { EmptyState } from '@/components/EmptyState'
import { useI18n } from '@/i18n/I18nContext'
import { extractBranchView } from '@/utils/explorationTree'

export function ExplorationTimeline({ events }: { events: EventRecord[] }) {
  const { t } = useI18n()
  const view = useMemo(() => extractBranchView(events), [events])
  if (view.branches.length === 0) {
    return <EmptyState description={t('run.exploration.empty')} />
  }
  return <BranchTreeTimeline view={view} />
}
