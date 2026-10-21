/* eslint-disable react-hooks/set-state-in-effect -- reset state when the observed run changes */
import { useEffect, useRef, useState } from 'react'
import { getLogs } from '@/api/client'
import type { LogRecord } from '@/api/types'
import { mergeBySeq } from '@/utils/merge'

const INITIAL_LIMIT = 2000
const PAGE_LIMIT = 2000
const MAX_LOGS = 10000

export function useRunLogs(runId: string | undefined, isLive: boolean) {
  const [logs, setLogs] = useState<LogRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [ready, setReady] = useState(false)
  const logsRef = useRef<LogRecord[]>([])
  const afterSeqRef = useRef(0)
  const loadedRunRef = useRef<string | null>(null)

  useEffect(() => {
    if (!runId) return
    let cancelled = false
    const controller = new AbortController()
    logsRef.current = []
    afterSeqRef.current = 0
    loadedRunRef.current = null
    setLogs([])
    setReady(false)
    setLoading(true)
    setError(null)
    setTruncated(false)
    getLogs(runId, { tail: true, limit: INITIAL_LIMIT }, controller.signal)
      .then((page) => {
        if (cancelled) return
        const merged = mergeBySeq([], page, MAX_LOGS)
        logsRef.current = merged
        afterSeqRef.current = merged.at(-1)?.seq ?? 0
        setLogs(merged)
        setTruncated(page.length >= INITIAL_LIMIT)
        loadedRunRef.current = runId
        setReady(true)
      })
      .catch((reason: unknown) => {
        if (!cancelled && !controller.signal.aborted) setError(errorText(reason))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [runId])

  useEffect(() => {
    if (!runId || !ready || loadedRunRef.current !== runId) return
    let cancelled = false
    let inFlight = false
    const controller = new AbortController()

    const fetchAfter = async () => {
      if (inFlight || cancelled) return
      inFlight = true
      try {
        const page = await getLogs(
          runId,
          { afterSeq: afterSeqRef.current, limit: PAGE_LIMIT },
          controller.signal,
        )
        if (cancelled || page.length === 0) return
        const merged = mergeBySeq(logsRef.current, page, MAX_LOGS)
        logsRef.current = merged
        afterSeqRef.current = merged.at(-1)?.seq ?? afterSeqRef.current
        setLogs(merged)
        setError(null)
      } catch (reason) {
        if (!cancelled && !controller.signal.aborted) setError(errorText(reason))
      } finally {
        inFlight = false
      }
    }

    if (!isLive) {
      void fetchAfter()
      return () => {
        cancelled = true
        controller.abort()
      }
    }
    void fetchAfter()
    const timer = window.setInterval(() => void fetchAfter(), 1000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
      controller.abort()
    }
  }, [runId, isLive, ready])

  return { logs, loading, error, truncated, limit: INITIAL_LIMIT }
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
