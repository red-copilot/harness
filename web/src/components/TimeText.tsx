import { Tooltip } from 'antd'
import { useI18n } from '@/i18n/I18nContext'
import { formatDateTime } from '@/utils/format'

export function TimeText({ value }: { value?: string | null }) {
  const { locale } = useI18n()
  const formatted = formatDateTime(value, locale)
  return <Tooltip title={value ?? undefined}>{formatted}</Tooltip>
}
