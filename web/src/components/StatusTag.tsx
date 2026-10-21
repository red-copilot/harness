import { Tag } from 'antd'
import type { RunStatus } from '@/api/types'
import { useI18n } from '@/i18n/I18nContext'
import type { MessageKey } from '@/i18n/messages'
import { statusColor } from '@/utils/status'

export function StatusTag({ status }: { status: RunStatus }) {
  const { t } = useI18n()
  return (
    <Tag color={statusColor(status)} className="status-tag">
      <span className={`status-dot status-${status}`} />
      {t(`status.${status}` as MessageKey)}
    </Tag>
  )
}
