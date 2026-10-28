import { fireEvent, screen } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import RunDetailPage from './RunDetailPage'
import { http, HttpResponse, renderWithProviders, server } from '@/test/renderTestUtils'

vi.mock('@/hooks/useRunEvents', () => ({
  useRunEvents: () => ({
    events: [
      {
        seq: 1,
        run_id: 'run_1',
        session_id: 'executor-1',
        source: 'agent',
        type: 'agent.event',
        time: '2026-10-20T12:00:01Z',
        data: { native_type: 'turn_end', data: { message: { content: [{ type: 'toolCall', name: 'bash' }] } } },
      },
      {
        seq: 2,
        run_id: 'run_1',
        session_id: '',
        source: 'harness',
        type: 'exploration.branch.created',
        time: '2026-10-20T12:00:02Z',
        data: { branch: { id: 'root', depth: 0, topic: 'root', phase: 'open', outcome: 'pending', scope: 'target' } },
      },
      {
        seq: 3,
        run_id: 'run_1',
        session_id: '',
        source: 'harness',
        type: 'exploration.branch.created',
        time: '2026-10-20T12:00:03Z',
        data: { branch: { id: 'branch-1', parent_id: 'root', depth: 1, topic: 'web.verify', hypothesis: 'Verify the port observation', phase: 'closed', outcome: 'supported', scope: 'target' } },
      },
      {
        seq: 4,
        run_id: 'run_1',
        session_id: '',
        source: 'harness',
        type: 'run.finalized',
        time: '2026-10-20T12:00:04Z',
        data: { phase: 'terminal', outcome: 'succeeded', status: 'succeeded' },
      },
    ],
    streamState: 'closed',
    streamError: null,
  }),
}))

vi.mock('@/hooks/useRunLogs', () => ({
  useRunLogs: () => ({ logs: [], loading: false, error: null, truncated: false, limit: 2000 }),
}))

describe('RunDetailPage', () => {
  it('shows the authoritative verdict and event tab', async () => {
    server.use(
      http.get('*/v1/runs/run_1', () =>
        HttpResponse.json({
          id: 'run_1',
          phase: 'terminal',
          outcome: 'succeeded',
          status: 'succeeded',
          created_at: '2026-10-20T11:59:00Z',
          started_at: '2026-10-20T12:00:00Z',
          finished_at: '2026-10-20T12:01:00Z',
          spec: {
            task: { id: 'demo', objective: 'Find and prove the flag' },
            agents: [{ id: 'pi' }],
            target: { id: 'target', addresses: ['10.0.0.5:8080'] },
          },
          result: {
            run_id: 'run_1',
            phase: 'terminal',
            outcome: 'succeeded',
            status: 'succeeded',
            usage: { total_tokens: 10, cost_usd: 0.1 },
            sessions: [{ id: 'pi', status: 'succeeded', exit_code: 0 }],
            verdict: { success: true, score: 3, message: 'flag accepted' },
          },
        }),
      ),
      http.get('*/v1/runs/run_1/graph', () =>
        HttpResponse.json({ facts: [], intents: [], hints: [], attempts: [], slots: [1, 2] }),
      ),
      http.get('*/v1/runs/run_1/verdict', () =>
        HttpResponse.json({
          run_id: 'run_1',
          success: true,
          score: 3,
          message: 'flag accepted',
          evidence: { flag: 'ok' },
          created_at: '2026-10-20T12:01:00Z',
        }),
      ),
    )
    renderWithProviders(
      <Routes>
        <Route path="/runs/:runId" element={<RunDetailPage />} />
      </Routes>,
      { route: '/runs/run_1' },
    )
    expect(await screen.findByText('Find and prove the flag')).toBeInTheDocument()
    expect(await screen.findByText('权威判定：成功')).toBeInTheDocument()
    fireEvent.click(screen.getByText('探索'))
    expect((await screen.findAllByText('web.verify')).length).toBeGreaterThan(0)
    expect((await screen.findAllByText('Verify the port observation')).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByText('事件 (4)'))
    expect(await screen.findByText('run.finalized')).toBeInTheDocument()
  })
})
