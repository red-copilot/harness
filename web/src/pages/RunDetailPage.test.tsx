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
        source: 'harness',
        type: 'run.finished',
        time: '2026-10-20T12:00:00Z',
        data: { status: 'succeeded' },
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
            status: 'succeeded',
            usage: { total_tokens: 10, cost_usd: 0.1 },
            sessions: [{ id: 'pi', status: 'succeeded', exit_code: 0 }],
            verdict: { success: true, score: 3, message: 'flag accepted' },
          },
        }),
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
    fireEvent.click(screen.getByText('事件 (1)'))
    expect(await screen.findByText('run.finished')).toBeInTheDocument()
  })
})
