/* eslint-disable react-hooks/set-state-in-effect -- reset state when the observed run changes */
import { useEffect, useRef, useState } from 'react'
import { fetchRunEventsPage } from '@/api/client'
import { parseSseStream } from '@/api/sse'
import type { EventRecord } from '@/api/types'
import { mergeBySeq } from '@/utils/merge'

export type EventStreamState = 'connecting' | 'live' | 'reconnecting' | 'closed' | 'paused'

const MAX_EVENTS = 10000

export function useRunEvents(
  runId: string | undefined,
  paused: boolean,
  onFinished?: () => void,
  terminal = false,
) {
  const [events, setEvents] = useState<EventRecord[]>([])
  const [streamState, setStreamState] = useState<EventStreamState>('closed')
  const [streamError, setStreamError] = useState<string | null>(null)
  const eventsRef = useRef<EventRecord[]>([])
  const lastSeqRef = useRef(0)
  const finishedRef = useRef(false)
  const callbackRef = useRef(onFinished)
  const terminalRef = useRef(terminal)

  useEffect(() => {
    callbackRef.current = onFinished
    terminalRef.current = terminal
  }, [onFinished, terminal])

  useEffect(() => {
    eventsRef.current = []
    lastSeqRef.current = 0
    finishedRef.current = false
    setEvents([])
    setStreamError(null)
    setStreamState(runId ? 'connecting' : 'closed')
  }, [runId])

  useEffect(() => {
    if (!runId) return
    if (paused) {
      setStreamState('paused')
      return
    }
    if (finishedRef.current) {
      setStreamState('closed')
      return
    }
    let cancelled = false
    let attempts = 0
    const controller = new AbortController()

    const connect = async () => {
      while (!cancelled && !finishedRef.current) {
        setStreamState(attempts === 0 ? 'connecting' : 'reconnecting')
        try {
          const response = await fetchRunEventsPage(runId, lastSeqRef.current, controller.signal)
          if (!response.body) throw new Error('event stream has no body')
          setStreamState('live')
          setStreamError(null)
          attempts = 0
          for await (const frame of parseSseStream(response.body)) {
            if (cancelled || finishedRef.current) return
            const event = decodeEvent(frame.data, frame.event)
            if (!event) continue
            if (event.seq > lastSeqRef.current) lastSeqRef.current = event.seq
            const merged = mergeBySeq(eventsRef.current, [event], MAX_EVENTS)
            eventsRef.current = merged
            setEvents(merged)
            if (event.type === 'run.finalized') {
              finishedRef.current = true
              setStreamState('closed')
              callbackRef.current?.()
              return
            }
          }
          if (terminalRef.current) {
            setStreamState('closed')
            return
          }
        } catch (error) {
          if (cancelled || controller.signal.aborted) return
          setStreamError(error instanceof Error ? error.message : String(error))
        }
        if (cancelled || finishedRef.current) return
        attempts += 1
        setStreamState('reconnecting')
        await sleep(Math.min(15000, 1000 * 2 ** (attempts - 1)), controller.signal)
      }
    }

    void connect()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [runId, paused])

  return { events, streamState, streamError }
}

function decodeEvent(data: string, fallbackType?: string): EventRecord | null {
  try {
    const parsed = JSON.parse(data) as Partial<EventRecord>
    if (typeof parsed.seq !== 'number' || typeof parsed.run_id !== 'string') return null
    const type = typeof parsed.type === 'string' ? parsed.type : fallbackType
    if (!type) return null
    return {
      seq: parsed.seq,
      run_id: parsed.run_id,
      session_id: parsed.session_id,
      source: typeof parsed.source === 'string' ? parsed.source : 'unknown',
      type,
      time: typeof parsed.time === 'string' ? parsed.time : new Date().toISOString(),
      data: parsed.data,
      raw: parsed.raw,
    }
  } catch {
    return null
  }
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = window.setTimeout(resolve, ms)
    signal.addEventListener(
      'abort',
      () => {
        window.clearTimeout(timer)
        resolve()
      },
      { once: true },
    )
  })
}
