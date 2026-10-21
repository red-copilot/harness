import { Card } from 'antd'
import type { ReactNode } from 'react'

export function MetricCard({
  title,
  value,
  hint,
  icon,
}: {
  title: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
}) {
  return (
    <Card className="metric-card">
      <div className="metric-card-top">
        <span className="metric-card-title">{title}</span>
        {icon ? <span className="metric-card-icon">{icon}</span> : null}
      </div>
      <div className="metric-card-value">{value}</div>
      {hint ? <div className="metric-card-hint">{hint}</div> : null}
    </Card>
  )
}
