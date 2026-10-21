import { Alert, Button } from 'antd'
import { useI18n } from '@/i18n/I18nContext'

export function PageError({
  error,
  onRetry,
}: {
  error: unknown
  onRetry?: () => void
}) {
  const { t } = useI18n()
  const message =
    error instanceof Error ? error.message : error ? String(error) : t('error.loadFailed')
  return (
    <Alert
      type="error"
      showIcon
      title={t('error.loadFailed')}
      description={message}
      action={
        onRetry ? (
          <Button size="small" onClick={onRetry}>
            {t('common.retry')}
          </Button>
        ) : undefined
      }
    />
  )
}
