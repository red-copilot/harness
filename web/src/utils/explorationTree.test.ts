import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { extractBranchView } from './explorationTree'

function event(seq: number, type: string, data: unknown): EventRecord {
  return { seq, run_id: 'run_1', session_id: '', source: 'control', type, time: `2026-10-21T04:00:0${seq}Z`, data }
}

describe('extractBranchView', () => {
  it('projects typed branch, attempt, evidence, knowledge and authority events', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', { branch: { id: 'root', task_id: 'task-1', depth: 0, topic: 'root', phase: 'closed', outcome: 'supported', scope: 'target-1' } }),
      event(2, 'exploration.branch.created', { branch: { id: 'branch-a', parent_id: 'root', task_id: 'task-1', depth: 1, topic: 'web.oa', goal: 'find OA', hypothesis: 'OA is exposed', phase: 'closed', outcome: 'supported', scope: 'target-1' } }),
      event(3, 'exploration.attempt.completed', { attempt: { id: 'attempt-1', branch_id: 'branch-a', session_id: 'executor-1', phase: 'completed', outcome: 'supported' } }),
      event(4, 'observation.created', { observation: { id: 'obs-1', branch_id: 'branch-a', attempt_id: 'attempt-1', summary: 'OA path exists' } }),
      event(5, 'knowledge.promoted', { knowledge: { id: 'k-1', task_id: 'task-1', topic: 'web.oa', kind: 'anchor', statement: 'OA path exists', trust: 'observed', scope: 'target' } }),
      event(6, 'artifact.stored', { artifact: { id: 'abc', attempt_id: 'attempt-1', name: 'proof.txt', media_type: 'text/plain', size: 12, sha256: 'abc', redacted: true } }),
      event(7, 'authority.progress', { completion: { progress: 2, total: 6 } }),
    ])
    expect(view.branches).toHaveLength(2)
    expect(view.branches[1]).toMatchObject({ id: 'branch-a', parentId: 'root', depth: 1, topic: 'web.oa', phase: 'closed', outcome: 'supported' })
    expect(view.attempts[0]).toMatchObject({ branchId: 'branch-a', phase: 'completed', outcome: 'supported' })
    expect(view.observations[0].summary).toBe('OA path exists')
    expect(view.knowledge[0]).toMatchObject({ topic: 'web.oa', trust: 'observed', scope: 'target' })
    expect(view.artifacts[0]).toMatchObject({ name: 'proof.txt', redacted: true })
    expect(view.progress).toEqual({ current: 2, total: 6 })
  })

  it('updates an existing branch without duplicating it', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', { branch: { id: 'root', depth: 0, topic: 'root', phase: 'open', outcome: 'pending' } }),
      event(2, 'exploration.branch.updated', { branch: { id: 'root', depth: 0, topic: 'root', phase: 'closed', outcome: 'supported' } }),
    ])
    expect(view.branches).toHaveLength(1)
    expect(view.branches[0]).toMatchObject({ phase: 'closed', outcome: 'supported', seq: 1 })
  })

  it('ignores malformed, legacy, and unknown events', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', { id: '', topic: 'bad' }),
      event(2, 'exploration.facts.merged', { item_id: 'legacy' }),
      { ...event(3, 'run.created', {}), type: 'future.unknown' },
    ])
    expect(view.branches).toHaveLength(0)
    expect(view.attempts).toHaveLength(0)
    expect(view.observations).toHaveLength(0)
    expect(view.knowledge).toHaveLength(0)
  })
})

describe('event schema compatibility', () => {
  it('ignores a known event with an unsupported schema version', () => {
    const view = extractBranchView([
      { ...event(1, 'exploration.branch.created', { branch: { id: 'root', depth: 0, topic: 'root', phase: 'open', outcome: 'pending' } }), schema_version: 99 },
    ])
    expect(view.branches).toHaveLength(0)
  })
})
