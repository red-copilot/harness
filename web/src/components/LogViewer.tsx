/* eslint-disable react-hooks/incompatible-library -- TanStack Virtual is not memo-safe by design */
import { useEffect, useRef } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { Grid } from 'antd'
import type { LogRecord } from '@/api/types'
import { useI18n } from '@/i18n/I18nContext'
import { formatDateTime } from '@/utils/format'

export function LogViewer({
  logs,
  follow,
  onFollowChange,
}: {
  logs: LogRecord[]
  follow: boolean
  onFollowChange?: (follow: boolean) => void
}) {
  const { locale } = useI18n()
  const parentRef = useRef<HTMLDivElement>(null)
  const screens = Grid.useBreakpoint()
  const compact = !screens.md
  const virtualizer = useVirtualizer({
    count: logs.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => (compact ? 72 : 24),
    overscan: 24,
  })

  useEffect(() => {
    if (follow && logs.length > 0) {
      virtualizer.scrollToIndex(logs.length - 1, { align: 'end' })
    }
  }, [follow, logs.length, virtualizer])

  const handleScroll = () => {
    const element = parentRef.current
    if (!element || !follow || !onFollowChange) return
    const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 32
    if (!atBottom) onFollowChange(false)
  }

  return (
    <div ref={parentRef} className="log-viewer" onScroll={handleScroll}>
      <div style={{ height: `${virtualizer.getTotalSize()}px`, position: 'relative' }}>
        {virtualizer.getVirtualItems().map((item) => {
          const log = logs[item.index]
          return (
            <div
              key={log.seq}
              data-index={item.index}
              ref={virtualizer.measureElement}
              className={`log-line ${log.stream === 'stderr' ? 'log-line-error' : ''}`}
              style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                transform: `translateY(${item.start}px)`,
              }}
            >
              <span className="log-seq">{log.seq}</span>
              <span className="log-time">{formatDateTime(log.time, locale)}</span>
              <span className="log-session">{log.session_id || '—'}</span>
              <span className={`log-stream stream-${log.stream}`}>{log.stream}</span>
              <span className="log-text">{log.text}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
