import { Typography } from 'antd'
import type { ReactNode } from 'react'

export function PageHeader({
  title,
  description,
  extra,
}: {
  title: ReactNode
  description?: ReactNode
  extra?: ReactNode
}) {
  return (
    <div className="page-header">
      <div>
        <Typography.Title level={2}>{title}</Typography.Title>
        {description ? <Typography.Paragraph type="secondary">{description}</Typography.Paragraph> : null}
      </div>
      {extra ? <div className="page-header-extra">{extra}</div> : null}
    </div>
  )
}
