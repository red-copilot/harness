import { Card, Col, Row, Space, Statistic, Tag, Typography } from 'antd'
import { useMemo } from 'react'
import type { EventRecord } from '@/api/types'
import { BranchTreeTimeline } from '@/components/BranchTreeTimeline'
import { EmptyState } from '@/components/EmptyState'
import { ExplorationGraph } from '@/components/ExplorationGraph'
import { TimeText } from '@/components/TimeText'
import { useI18n } from '@/i18n/I18nContext'
import {
  extractExplorationCycles,
  extractHypothesisView,
  hypothesisStatusColor,
  hypothesisStatusKey,
} from '@/utils/exploration'
import { extractBranchView } from '@/utils/explorationTree'
import type {
  ExplorationCycle,
  ExplorationObservation,
  FrontierItem,
  HypothesisAttempt,
  HypothesisView,
} from '@/utils/exploration'
import type { BranchView } from '@/utils/explorationTree'

type Projection =
  | { kind: 'branches'; view: BranchView }
  | { kind: 'hypothesis'; view: HypothesisView }
  | { kind: 'cycles'; cycles: ExplorationCycle[] }

export function ExplorationTimeline({ events }: { events: EventRecord[] }) {
  const { t } = useI18n()
  const projection = useMemo<Projection>(() => {
    const branches = extractBranchView(events)
    if (branches.branches.length > 0) return { kind: 'branches', view: branches }
    const hypothesis = extractHypothesisView(events)
    if (hypothesis.attempts.length > 0 || hypothesis.progress) {
      return { kind: 'hypothesis', view: hypothesis }
    }
    return { kind: 'cycles', cycles: extractExplorationCycles(events) }
  }, [events])
  if (projection.kind === 'branches') {
    return <BranchTreeTimeline view={projection.view} />
  }
  if (projection.kind === 'hypothesis') {
    return <HypothesisTimeline view={projection.view} />
  }
  const cycles = projection.cycles
  if (cycles.length === 0) {
    return <EmptyState description={t('run.exploration.empty')} />
  }

  const latest = cycles[0]
  const latestState = latest.state
  const latestFrontier = latest.frontier
  const openItems = latestFrontier?.frontier.items.filter((item) => item.status !== 'done').length ?? 0
  const visibleCycles = cycles.slice(0, 20)

  return (
    <div className="page-stack">
      <Row gutter={[16, 16]}>
        <Metric title={t('run.exploration.revision')} value={latestState?.revision ?? 0} />
        <Metric title={t('run.exploration.attempts')} value={latestState?.attempts.length ?? 0} />
        <Metric title={t('run.exploration.observations')} value={latestState?.observations.length ?? 0} />
        <Metric title={t('run.exploration.openItems')} value={openItems} />
      </Row>

      <Card title={t('run.exploration.title')}>
        <Typography.Paragraph type="secondary" className="exploration-note">
          {t('run.exploration.note')}
        </Typography.Paragraph>
        <div className="exploration-cycles">
          {visibleCycles.map((cycle, index) => (
            <ExplorationCycleCard key={cycle.turn.seq} cycle={cycle} latest={index === 0} />
          ))}
        </div>
        {cycles.length > visibleCycles.length ? (
          <Typography.Text type="secondary" className="exploration-limit-note">
            {t('run.exploration.showingRecent', { count: visibleCycles.length })}
          </Typography.Text>
        ) : null}
      </Card>
    </div>
  )
}

function HypothesisTimeline({ view }: { view: HypothesisView }) {
  const { t } = useI18n()
  const failed = view.attempts.filter((attempt) => attempt.status === 'failed').length
  const facts = view.attempts.reduce((total, attempt) => total + (attempt.facts?.added.length ?? 0), 0)
  const activeFrontiers = view.frontiers.filter((frontier) => frontier.status !== 'done' && frontier.status !== 'failed')
  return (
    <div className="page-stack">
      <Row gutter={[16, 16]}>
        <Metric
          title={t('run.hypothesis.progress')}
          value={view.progress ? view.progress.current : 0}
        />
        <Metric title={t('run.hypothesis.activeFrontiers')} value={activeFrontiers.length} />
        <Metric title={t('run.hypothesis.facts')} value={facts} />
        <Metric title={t('run.hypothesis.failed')} value={failed} />
      </Row>
      <Card title={t('run.hypothesis.title')}>
        <Typography.Paragraph type="secondary" className="exploration-note">
          {view.progress
            ? t('run.hypothesis.progressNote', {
                current: view.progress.current,
                total: view.progress.total,
              })
            : t('run.hypothesis.note')}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" className="exploration-note">
          {t('run.graph.legend')}
        </Typography.Paragraph>
        <ExplorationGraph view={view} />
        <div className="exploration-cycles">
          <Typography.Title level={5}>{t('run.hypothesis.executionDetails')}</Typography.Title>
          {view.attempts.slice(0, 20).map((attempt) => (
            <HypothesisAttemptCard key={attempt.itemId} attempt={attempt} />
          ))}
        </div>
      </Card>
    </div>
  )
}

function HypothesisAttemptCard({ attempt }: { attempt: HypothesisAttempt }) {
  const { t } = useI18n()
  const tone = hypothesisStatusColor(attempt.status, 'processing')
  const statusKey = hypothesisStatusKey(attempt.status) ?? 'assigned'
  return (
    <article className="exploration-cycle">
      <div className="exploration-cycle-head">
        <Space wrap size="small">
          <Tag color={tone}>{t(`run.hypothesis.status.${statusKey}`)}</Tag>
          {attempt.round ? <Tag>{t('run.hypothesis.round', { round: attempt.round })}</Tag> : null}
          <Typography.Text code>{attempt.itemId}</Typography.Text>
          {attempt.executorId ? <Typography.Text>{attempt.executorId}</Typography.Text> : null}
        </Space>
      </div>
      <div className="exploration-cycle-grid">
        <div>
          <Typography.Text strong>{t('run.hypothesis.verdict')}</Typography.Text>
          <Typography.Paragraph>{attempt.verdict || '—'}</Typography.Paragraph>
        </div>
        <div>
          <Typography.Text strong>{t('run.hypothesis.facts')}</Typography.Text>
          <Typography.Paragraph>
            {attempt.facts
              ? `+${attempt.facts.added.length} · corroborated ${attempt.facts.corroborated.length} · contested ${attempt.facts.contested.length}`
              : '—'}
          </Typography.Paragraph>
          {attempt.error ? <Typography.Text type="danger">{attempt.error}</Typography.Text> : null}
        </div>
      </div>
    </article>
  )
}

function Metric({ title, value }: { title: string; value: number }) {
  return (
    <Col xs={12} xl={6}>
      <Card size="small" className="exploration-metric">
        <Statistic title={title} value={value} />
      </Card>
    </Col>
  )
}

function ExplorationCycleCard({ cycle, latest }: { cycle: ExplorationCycle; latest: boolean }) {
  const { t } = useI18n()
  const state = cycle.state
  const frontier = cycle.frontier
  const focus = frontier?.focus
  const items = frontier?.frontier.items ?? []
  const observations = (frontier?.recent_observations?.length ? frontier.recent_observations : state?.observations ?? []).slice(-3)
  const toolNames = state?.attempts.at(-1)?.tools ?? []

  return (
    <article className={`exploration-cycle${latest ? ' exploration-cycle-latest' : ''}`}>
      <div className="exploration-cycle-head">
        <Space wrap size="small">
          <Tag color={latest ? 'processing' : 'default'}>{latest ? t('run.exploration.latest') : `#${cycle.turn.seq}`}</Tag>
          <Typography.Text code>{cycle.turn.session_id || '—'}</Typography.Text>
          <TimeText value={cycle.turn.time} />
        </Space>
        <Typography.Text type="secondary">#{cycle.turn.seq}</Typography.Text>
      </div>

      <div className="exploration-chain">
        <ChainNode label={t('run.exploration.action')} value={toolNames.join(', ') || 'turn_end'} tone="action" />
        <ChainArrow />
        <ChainNode label={t('run.exploration.state')} value={`State r${state?.revision ?? '—'}`} tone="state" />
        <ChainArrow />
        <ChainNode label={t('run.exploration.frontier')} value={`${items.length} open`} tone="frontier" />
        <ChainArrow />
        <ChainNode label={t('run.exploration.focus')} value={focus?.text || t('run.exploration.noFocus')} tone="focus" />
      </div>

      <div className="exploration-cycle-grid">
        <div>
          <Typography.Text strong>{t('run.exploration.recentObservations')}</Typography.Text>
          <div className="exploration-observations">
            {observations.length ? observations.map((observation) => (
              <Observation key={`${observation.event_seq}-${observation.tool}-${observation.text}`} observation={observation} />
            )) : (
              <Typography.Text type="secondary">{t('run.exploration.noObservations')}</Typography.Text>
            )}
          </div>
        </div>
        <div className="exploration-focus">
          <Typography.Text strong>{t('run.exploration.focus')}</Typography.Text>
          <Typography.Paragraph>{focus?.text || t('run.exploration.noFocus')}</Typography.Paragraph>
          {items.length ? (
            <Space wrap size={[4, 4]}>
              {items.map((item) => <FrontierTag key={item.id} item={item} />)}
            </Space>
          ) : null}
        </div>
      </div>
    </article>
  )
}

function ChainNode({ label, value, tone }: { label: string; value: string; tone: string }) {
  return (
    <div className={`exploration-node exploration-node-${tone}`}>
      <span className="exploration-node-label">{label}</span>
      <span className="exploration-node-value" title={value}>{value}</span>
    </div>
  )
}

function ChainArrow() {
  return <span className="exploration-arrow" aria-hidden="true">→</span>
}

function Observation({ observation }: { observation: ExplorationObservation }) {
  return (
    <div className="exploration-observation">
      <Space size="small" wrap>
        <Tag color={observation.error ? 'error' : 'default'}>{observation.tool || 'tool'}</Tag>
        {observation.error ? <Tag color="error">error</Tag> : null}
      </Space>
      <Typography.Paragraph ellipsis={{ rows: 2, tooltip: observation.text }}>
        {observation.text || '—'}
      </Typography.Paragraph>
    </div>
  )
}

function FrontierTag({ item }: { item: FrontierItem }) {
  return (
    <Tag color={item.kind === 'candidate' ? 'blue' : 'purple'}>
      {item.kind} · p{item.priority ?? '—'} · {item.status ?? 'open'}
    </Tag>
  )
}
