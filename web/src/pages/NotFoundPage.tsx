import { Button, Result } from 'antd'
import { useNavigate } from 'react-router-dom'
import { useI18n } from '@/i18n/I18nContext'

export default function NotFoundPage() {
  const { t } = useI18n()
  const navigate = useNavigate()
  return (
    <Result
      status="404"
      title={t('notFound.title')}
      subTitle={t('notFound.description')}
      extra={<Button type="primary" onClick={() => navigate('/')}>{t('nav.overview')}</Button>}
    />
  )
}
