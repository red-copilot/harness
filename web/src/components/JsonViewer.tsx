import { Button, Card, Tooltip } from 'antd'
import CopyOutlined from '@ant-design/icons/CopyOutlined'
import { useState } from 'react'
import { useI18n } from '@/i18n/I18nContext'
import { prettyJson } from '@/utils/json'

export function JsonViewer({ value, title }: { value: unknown; title?: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const text = prettyJson(value)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <Card
      title={title}
      className="json-viewer"
      extra={
        <Tooltip title={copied ? t('common.copied') : t('common.copy')}>
          <Button type="text" icon={<CopyOutlined />} onClick={() => void copy()} aria-label={t('common.copy')} />
        </Tooltip>
      }
    >
      <pre>{text}</pre>
    </Card>
  )
}
