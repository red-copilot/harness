import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { extractExplorationCycles, extractHypothesisView } from './exploration'

function event(seq: number, type: string, sessionId: string, data: unknown): EventRecord {
  return {
    seq,
    run_id: 'run_1',
    session_id: sessionId,
    source: type.startsWith('exploration.') ? 'control' : 'agent',
    type,
    time: `2026-10-21T04:00:0${seq}Z`,
    data,
  }
}

describe('extractExplorationCycles', () => {
  it('pairs turn, state and frontier events per session and returns newest first', () => {
    const events = [
      event(1, 'turn_end', 'pi-a', { message: { content: [] }, toolResults: [] }),
      event(2, 'turn_end', 'pi-b', { message: { content: [] }, toolResults: [] }),
      event(3, 'exploration.state.updated', 'pi-a', { revision: 1, attempts: [{}], observations: [] }),
      event(4, 'exploration.state.updated', 'pi-b', { revision: 4, attempts: [{}, {}], observations: [] }),
      event(5, 'exploration.frontier.updated', 'pi-a', {
        state_revision: 1,
        frontier: { items: [{ id: 'q1', kind: 'question', text: 'verify a', status: 'open', priority: 90 }] },
        focus: { id: 'q1', kind: 'question', text: 'verify a', status: 'open', priority: 90 },
      }),
      event(6, 'exploration.frontier.updated', 'pi-b', {
        state_revision: 4,
        frontier: { items: [] },
      }),
    ]

    const cycles = extractExplorationCycles(events)
    expect(cycles).toHaveLength(2)
    expect(cycles[0].turn.session_id).toBe('pi-b')
    expect(cycles[0].state?.revision).toBe(4)
    expect(cycles[1].turn.session_id).toBe('pi-a')
    expect(cycles[1].frontier?.focus?.text).toBe('verify a')
  })

  it('ignores incomplete or malformed exploration events', () => {
    const cycles = extractExplorationCycles([
      event(1, 'exploration.state.updated', 'pi', { revision: 'bad' }),
      event(2, 'turn_end', 'pi', {}),
      event(3, 'exploration.frontier.updated', 'pi', null),
    ])
    expect(cycles).toHaveLength(1)
    expect(cycles[0].turn.seq).toBe(2)
    expect(cycles[0].state).toBeUndefined()
    expect(cycles[0].frontier).toBeUndefined()
  })
})

describe('extractHypothesisView', () => {
  it('merges frontier assignment, report, facts, failures and backend progress', () => {
    const view = extractHypothesisView([
      event(10, 'backend.progress', '', { progress: 2, total: 6 }),
      event(1, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-a',
        executor_id: 'executor-1',
      }),
      event(2, 'exploration.report.received', '', {
        round: 1,
        item_id: 'frontier-a',
        verdict: 'found',
      }),
      event(3, 'exploration.facts.merged', '', {
        round: 1,
        item_id: 'frontier-a',
        added: ['f-1'],
        corroborated: [],
        contested: [],
      }),
      event(4, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-b',
        executor_id: 'executor-2',
      }),
      event(5, 'exploration.item.failed', '', {
        round: 1,
        item_id: 'frontier-b',
        error: 'report item_id mismatch',
      }),
    ])

    expect(view.progress).toEqual({ current: 2, total: 6 })
    expect(view.attempts).toHaveLength(2)
    const completed = view.attempts.find((attempt) => attempt.itemId === 'frontier-a')
    expect(completed).toMatchObject({
      round: 1,
      executorId: 'executor-1',
      verdict: 'found',
      status: 'done',
      facts: { added: ['f-1'], corroborated: [], contested: [] },
    })
    const failed = view.attempts.find((attempt) => attempt.itemId === 'frontier-b')
    expect(failed).toMatchObject({ status: 'failed', error: 'report item_id mismatch' })
  })

  it('returns an empty view for malformed hypothesis events', () => {
    const view = extractHypothesisView([
      event(1, 'exploration.item.assigned', '', null),
      event(2, 'exploration.report.received', '', { item_id: 42 }),
      event(3, 'backend.progress', '', { progress: 'bad', total: 6 }),
    ])
    expect(view.attempts).toEqual([])
    expect(view.progress).toBeUndefined()
  })

  it('restores initialized blackboard state before a report exists', () => {
    const view = extractHypothesisView([
      event(1, 'exploration.initialized', '', {
        phase: 'initialized',
        objective: 'find the flag',
        targets: [{ id: 'web', addresses: ['10.0.0.5:80'] }],
        progress: { progress: 0, total: 1 },
        facts: [],
        frontiers: [],
        policy: { rounds: 6, max_workers: 1 },
      }),
      event(2, 'exploration.frontiers.created', '', {
        round: 1,
        items: [{
          id: 'frontier-a',
          question: 'Inspect the HTTP root',
          target_hint: 'web',
          probe_key: 'http.get',
          params: ['/'],
          expected_evidence: 'status and headers',
          failure_evidence: 'HTTP 404',
          max_tools: 5,
          max_duration_seconds: 90,
          status: 'open',
          round: 1,
          fingerprint: 'abc',
        }],
      }),
      event(3, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-a',
        executor_id: 'executor-1',
        item: {
          id: 'frontier-a',
          question: 'Inspect the HTTP root',
          target_hint: 'web',
          expected_evidence: 'status and headers',
          status: 'assigned',
          round: 1,
          fingerprint: 'abc',
        },
      }),
      event(4, 'exploration.executor.started', '', {
        round: 1,
        item_id: 'frontier-a',
        executor_id: 'executor-1',
        fresh_context: true,
      }),
    ])

    expect(view.phase).toBe('executing')
    expect(view.objective).toBe('find the flag')
    expect(view.targets).toEqual([{ id: 'web', addresses: ['10.0.0.5:80'] }])
    expect(view.progress).toEqual({ current: 0, total: 1 })
    expect(view.frontiers).toHaveLength(1)
    expect(view.frontiers[0]).toMatchObject({
      id: 'frontier-a',
      question: 'Inspect the HTTP root',
      targetHint: 'web',
      probeKey: 'http.get',
      params: ['/'],
      expectedEvidence: 'status and headers',
      failureEvidence: 'HTTP 404',
      maxTools: 5,
      maxDurationSeconds: 90,
      status: 'assigned',
      executorId: 'executor-1',
    })
    expect(view.current).toEqual(['frontier-a'])
  })

  it('keeps published fact content and executor activity', () => {
    const view = extractHypothesisView([
      event(1, 'exploration.frontiers.created', '', {
        round: 1,
        items: [{
          id: 'frontier-a',
          question: '根路径是否返回 200？',
          target_hint: 'web',
          probe_key: 'http.get',
          params: ['/'],
          expected_evidence: '200',
          failure_evidence: '404',
          status: 'open',
          round: 1,
        }],
      }),
      event(2, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-a',
        executor_id: 'executor-1',
      }),
      event(3, 'tool_execution_start', 'executor-1', {
        toolName: 'bash',
        args: { command: 'curl -i http://10.0.0.5/' },
      }),
      event(4, 'tool_execution_end', 'executor-1', { toolName: 'bash', isError: false }),
      event(5, 'exploration.facts.merged', '', {
        round: 1,
        item_id: 'frontier-a',
        added: ['f-1-1'],
        corroborated: [],
        contested: [],
        facts: [{
          id: 'f-1-1',
          claim: '根路径返回 HTTP 200',
          evidence: 'curl 输出 200 OK',
          trust: 'observed',
          contested: false,
          targets: ['web'],
        }],
      }),
    ])

    expect(view.facts).toHaveLength(1)
    expect(view.facts[0]).toMatchObject({
      id: 'f-1-1',
      claim: '根路径返回 HTTP 200',
      evidence: 'curl 输出 200 OK',
      trust: 'observed',
      sourceItemId: 'frontier-a',
      round: 1,
    })
    expect(view.activity['frontier-a'].executorId).toBe('executor-1')
    expect(view.activity['frontier-a'].tools).toEqual([
      { seq: 3, tool: 'bash', detail: 'curl -i http://10.0.0.5/', error: false, running: false },
    ])
  })

  it('falls back to fact ids for runs that only published identifiers', () => {
    const view = extractHypothesisView([
      event(1, 'exploration.facts.merged', '', {
        round: 2,
        item_id: 'frontier-a',
        added: ['f-2-1'],
        corroborated: ['f-1-1'],
        contested: [],
      }),
    ])
    expect(view.facts.map((fact) => fact.id)).toEqual(['f-2-1', 'f-1-1'])
    expect(view.facts[0]).toMatchObject({ claim: '', trust: 'observed', round: 2 })
    expect(view.facts[1]).toMatchObject({ claim: '', trust: 'corroborated' })
  })

  it('tracks two concurrently assigned frontiers', () => {
    const view = extractHypothesisView([
      event(1, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-a',
        executor_id: 'executor-1',
      }),
      event(2, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-b',
        executor_id: 'executor-2',
      }),
    ])
    expect(view.current).toEqual(['frontier-a', 'frontier-b'])
  })
})
