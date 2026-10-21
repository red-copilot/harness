import { Spin } from 'antd'
import { lazy, Suspense } from 'react'
import { Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/AppShell'

const OverviewPage = lazy(() => import('@/pages/OverviewPage'))
const RunsPage = lazy(() => import('@/pages/RunsPage'))
const RunDetailPage = lazy(() => import('@/pages/RunDetailPage'))
const SweepsPage = lazy(() => import('@/pages/SweepsPage'))
const SweepDetailPage = lazy(() => import('@/pages/SweepDetailPage'))
const NotFoundPage = lazy(() => import('@/pages/NotFoundPage'))

export default function App() {
  return (
    <Suspense
      fallback={
        <div className="page-loading">
          <Spin size="large" />
        </div>
      }
    >
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<OverviewPage />} />
          <Route path="runs" element={<RunsPage />} />
          <Route path="runs/:runId" element={<RunDetailPage />} />
          <Route path="sweeps" element={<SweepsPage />} />
          <Route path="sweeps/:sweepId" element={<SweepDetailPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </Suspense>
  )
}
