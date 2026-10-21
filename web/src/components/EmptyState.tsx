import { Empty } from 'antd'
import type { ReactNode } from 'react'
import { useI18n } from '@/i18n/I18nContext'

export function EmptyState({
  description,
  children,
}: {
  description?: ReactNode
  children?: ReactNode
}) {
  const { t } = useI18n()
  return (
    <div className="empty-state">
      <Empty description={description ?? t('common.empty')}>{children}</Empty>
    </div>
  )
}
