import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import App from './App'
import { http, HttpResponse, renderWithProviders, server } from '@/test/renderTestUtils'

const overview = {
  generated_at: '2026-10-20T12:00:00Z',
  window: 100,
  runs: {
    total: 0,
    active: 0,
    terminal: 0,
    queued: 0,
    starting: 0,
    running: 0,
    verifying: 0,
    succeeded: 0,
    failed: 0,
    cancelled: 0,
    timed_out: 0,
    interrupted: 0,
    stagnated: 0,
    success_rate: 0,
  },
  usage: {},
  duration: { samples: 0, average_ms: 0, p50_ms: 0, p95_ms: 0 },
  sweeps: { total: 0, active: 0, succeeded: 0, failed: 0 },
}

describe('App shell', () => {
  it('renders navigation and the overview route', async () => {
    server.use(
      http.get('*/healthz', () => HttpResponse.json({ status: 'ok' })),
      http.get('*/v1/overview', () => HttpResponse.json(overview)),
      http.get('*/v1/runs', () => HttpResponse.json([])),
      http.get('*/v1/tsec/sweeps', () => HttpResponse.json([])),
    )
    renderWithProviders(<App />)
    expect(await screen.findByText('运行总览')).toBeInTheDocument()
    expect(screen.getByLabelText('打开导航')).toBeInTheDocument()
    expect(screen.queryByText('运行')).not.toBeInTheDocument()
  })
})
