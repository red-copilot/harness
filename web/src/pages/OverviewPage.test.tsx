import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import OverviewPage from './OverviewPage'
import { http, HttpResponse, renderWithProviders, server } from '@/test/renderTestUtils'

const overview = {
  generated_at: '2026-10-20T12:00:00Z',
  window: 100,
  runs: {
    total: 4,
    active: 2,
    terminal: 2,
    queued: 0,
    starting: 0,
    running: 2,
    verifying: 0,
    succeeded: 1,
    failed: 1,
    cancelled: 0,
    timed_out: 0,
    interrupted: 0,
    stagnated: 0,
    success_rate: 0.5,
  },
  usage: { input_tokens: 10, output_tokens: 5, total_tokens: 15, cost_usd: 0.1 },
  duration: { samples: 2, average_ms: 1500, p50_ms: 1000, p95_ms: 2000 },
  sweeps: { total: 1, active: 1, succeeded: 0, failed: 0 },
}

describe('OverviewPage', () => {
  it('renders metrics and recent runs', async () => {
    server.use(
      http.get('*/v1/overview', () => HttpResponse.json(overview)),
      http.get('*/v1/runs', () =>
        HttpResponse.json([
          {
            id: 'run_1',
            status: 'running',
            created_at: '2026-10-20T12:00:00Z',
            spec: { task: { id: 'demo', objective: 'solve' }, agents: [{ id: 'pi' }] },
          },
        ]),
      ),
      http.get('*/v1/tsec/sweeps', () => HttpResponse.json([])),
    )
    renderWithProviders(<OverviewPage />)
    expect(await screen.findByText('运行总览')).toBeInTheDocument()
    expect(await screen.findByText('50.0%')).toBeInTheDocument()
    expect(await screen.findByText('run_1')).toBeInTheDocument()
  })
})
