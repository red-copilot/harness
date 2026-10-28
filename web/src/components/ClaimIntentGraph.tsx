import { Alert, Card, Descriptions, Drawer, Tag, Typography } from 'antd'
import { Fragment, memo, useCallback, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import type { Artifact, BoardResponse, Claim, Hint, IntentAttempt, Observation } from '@/api/types'
import { useI18n } from '@/i18n/I18nContext'
import type { MessageKey } from '@/i18n/messages'
import { TimeText } from '@/components/TimeText'
import { formatBytes, isZeroTime, truncate } from '@/utils/format'
import {
  layoutExplorationGraph,
  type ExplorationGraphNode,
} from './explorationGraphLayout'

export function ClaimIntentGraph({ graph }: { graph: BoardResponse }) {
  const { t } = useI18n()
  const { claims, intents, attempts, observations, artifacts } = graph
  const layout = useMemo(
    () => layoutExplorationGraph({ claims, intents, attempts, observations, artifacts }),
    [claims, intents, attempts, observations, artifacts],
  )
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null)
  const selectedNode = useMemo(
    () => layout.nodes.find((node) => node.id === selectedNodeID),
    [layout, selectedNodeID],
  )
  const selectNode = useCallback((id: string) => setSelectedNodeID(id), [])

  return (
    <Card
      title={t('run.graph.title')}
      extra={<Typography.Text type="secondary">{t('run.graph.revision', { revision: graph.board_revision ?? 0 })}</Typography.Text>}
    >
      <div className="exploration-graph">
        <div className="exploration-graph-legend">
          <span className="exploration-graph-legend-item exploration-graph-legend-root">
            <span className="exploration-graph-legend-line" />{t('run.graph.initial')}
          </span>
          <span className="exploration-graph-legend-item exploration-graph-legend-derived">
            <span className="exploration-graph-legend-line" />{t('run.graph.edge.derived')}
          </span>
          <span className="exploration-graph-legend-item exploration-graph-legend-planned">
            <span className="exploration-graph-legend-line" />{t('run.graph.edge.planned')}
          </span>
          <span className="exploration-graph-legend-item exploration-graph-legend-fact">
            <span className="exploration-graph-legend-line" />{t('run.graph.result.finding')}
          </span>
          <span className="exploration-graph-legend-item exploration-graph-legend-negative">
            <span className="exploration-graph-legend-line" />{t('run.graph.result.negative')}
          </span>
        </div>
        <Typography.Text type="secondary" className="exploration-graph-hint">
          {t('run.graph.hint')}
        </Typography.Text>

        <div className="exploration-graph-scroll" data-testid="exploration-graph-scroll">
          <div className="exploration-graph-board" style={{ width: layout.width, height: layout.height }}>
            <svg
              className="exploration-graph-edges"
              width={layout.width}
              height={layout.height}
              viewBox={`0 0 ${layout.width} ${layout.height}`}
              aria-hidden="true"
            >
              {layout.edges.map((edge) => (
                <path
                  key={edge.id}
                  className={edgeClass(edge.root, edge.planned)}
                  d={edge.path}
                  data-testid={`exploration-edge-${edge.id}`}
                />
              ))}
            </svg>
            {layout.nodes.map((node) => (
              <ExplorationNodeButton
                key={node.id}
                node={node}
                selected={node.id === selectedNodeID}
                onSelect={selectNode}
              />
            ))}
          </div>
        </div>
        {layout.nodes.length === 1 ? (
          <Typography.Text type="secondary" className="exploration-graph-hint">
            {t('run.graph.waiting')}
          </Typography.Text>
        ) : null}
      </div>

      <Drawer
        open={Boolean(selectedNode)}
        onClose={() => setSelectedNodeID(null)}
        title={selectedNode ? nodeTitle(selectedNode, t) : t('run.graph.detailTitle')}
        size="min(560px, 100vw)"
        destroyOnHidden
      >
        {selectedNode ? <ExplorationNodeDetail node={selectedNode} hints={graph.hints ?? []} /> : null}
      </Drawer>
    </Card>
  )
}

const ExplorationNodeButton = memo(function ExplorationNodeButton({ node, selected, onSelect }: {
  node: ExplorationGraphNode
  selected: boolean
  onSelect: (id: string) => void
}) {
  const { t } = useI18n()
  const intent = node.intent
  const result = node.resultClaim
  const latestAttempt = node.attempts[node.attempts.length - 1]
  const active = intent?.state === 'claimed' || latestAttempt?.phase === 'running'
  const status = nodeStatus(node, active)
  const title = nodeTitle(node, t)
  const kindLabel = node.kind === 'origin'
    ? t('run.graph.initial')
    : t(intent?.kind === 'bootstrap' ? 'run.graph.kind.bootstrap' : 'run.graph.kind.explore')
  const stateLabel = node.kind === 'origin'
    ? t('run.graph.state.origin')
    : t(`run.graph.state.${intent?.state ?? 'open'}` as MessageKey)
  const resultLabel = result
    ? t(result.kind === 'finding' ? 'run.graph.result.finding' : 'run.graph.result.negative')
    : null
  const classes = [
    'exploration-graph-node',
    `exploration-graph-node-${NODE_TONE[status]}`,
    active ? 'exploration-graph-node-active' : '',
    selected ? 'exploration-graph-node-selected' : '',
  ].filter(Boolean).join(' ')

  return (
    <button
      type="button"
      className={classes}
      style={{ left: node.x, top: node.y, width: node.width, height: node.height }}
      onClick={() => onSelect(node.id)}
      aria-label={`${kindLabel}: ${title}`}
      data-testid={node.kind === 'origin' ? 'exploration-node-origin' : `exploration-node-${node.id}`}
      data-node-level={node.level}
    >
      <span className="exploration-graph-node-head">
        <span className="exploration-graph-node-kind">{kindLabel}</span>
        <Tag color={NODE_COLOR[status]}>{stateLabel}</Tag>
      </span>
      <span className="exploration-graph-node-title">{title}</span>
      {result ? (
        <span className="exploration-graph-node-result">
          <Tag color={claimColor(result.kind)}>{resultLabel}</Tag>
          <span>{result.description}</span>
        </span>
      ) : node.kind === 'intent' ? (
        <span className="exploration-graph-node-brief">{truncate(intent?.description ?? '', 96)}</span>
      ) : null}
      <span className="exploration-graph-node-meta">
        {node.kind === 'origin' ? t('run.graph.originMeta') : (
          <>
            <span>{t('run.graph.attempts', { count: node.attempts.length })}</span>
            <span>{t('run.graph.observations', { count: node.observations.length })}</span>
            <span>{t('run.graph.artifacts', { count: node.artifacts.length })}</span>
          </>
        )}
      </span>
    </button>
  )
})

function ExplorationNodeDetail({ node, hints }: { node: ExplorationGraphNode; hints: Hint[] }) {
  const { t } = useI18n()
  const intent = node.intent
  const result = node.resultClaim
  return (
    <div className="exploration-detail">
      {node.unresolvedFrom.length > 0 ? (
        <Alert type="warning" showIcon title={t('run.graph.detail.unresolved')} description={node.unresolvedFrom.join(', ')} />
      ) : null}
      <Descriptions size="small" column={1}>
        <Descriptions.Item label={t('run.graph.detail.type')}>
          {node.kind === 'origin' ? t('run.graph.initial') : t('run.graph.frontier')}
        </Descriptions.Item>
        <Descriptions.Item label={t('run.graph.detail.id')}>
          <Typography.Text code copyable>{node.kind === 'origin' ? (node.origin?.id ?? 'origin') : intent?.id}</Typography.Text>
        </Descriptions.Item>
        {intent ? <Descriptions.Item label={t('common.status')}>{t(`run.graph.state.${intent.state}` as MessageKey)}</Descriptions.Item> : null}
        {intent?.executor_id ? (
          <Descriptions.Item label={t('run.graph.detail.executor')}>
            <Typography.Text code>{intent.executor_id}</Typography.Text>
          </Descriptions.Item>
        ) : null}
        {intent ? (
          <Descriptions.Item label={t('run.graph.detail.kind')}>
            {t(intent.kind === 'bootstrap' ? 'run.graph.kind.bootstrap' : 'run.graph.kind.explore')}
          </Descriptions.Item>
        ) : null}
      </Descriptions>

      {node.kind === 'origin' ? (
        <DetailSection
          title={t('run.graph.detail.hints')}
          items={hints}
          render={(hint) => (
            <div className="exploration-detail-item">
              <Typography.Text>{hint.content}</Typography.Text>
              <Typography.Text type="secondary">{hint.creator}</Typography.Text>
            </div>
          )}
        />
      ) : (
        <>
          <DetailSection title={t('run.graph.detail.from')} items={node.fromClaims} render={(claim) => <ClaimDetail claim={claim} />} />
          <DetailSection title={t('run.graph.detail.result')} items={result ? [result] : []} render={(claim) => <ClaimDetail claim={claim} />} />
          <DetailSection title={t('run.graph.detail.attempts')} items={node.attempts} render={(attempt) => <AttemptDetail attempt={attempt} />} />
          <DetailSection title={t('run.graph.detail.observations')} items={node.observations} render={(observation) => <ObservationDetail observation={observation} />} />
          <DetailSection title={t('run.graph.detail.artifacts')} items={node.artifacts} render={(artifact) => <ArtifactDetail artifact={artifact} />} />
        </>
      )}
    </div>
  )
}

function ClaimDetail({ claim }: { claim: Claim }) {
  const { t } = useI18n()
  return (
    <div className="exploration-detail-item">
      <div className="exploration-detail-item-head">
        <Tag color={claimColor(claim.kind)}>{claim.kind}</Tag>
        <Typography.Text code>{claim.id}</Typography.Text>
      </div>
      <Typography.Text>{claim.description}</Typography.Text>
      {claim.observation_ids?.length ? <Typography.Text type="secondary">{t('run.graph.detail.observations')}: {claim.observation_ids.join(', ')}</Typography.Text> : null}
      {claim.artifact_ids?.length ? <Typography.Text type="secondary">{t('run.graph.detail.artifacts')}: {claim.artifact_ids.join(', ')}</Typography.Text> : null}
    </div>
  )
}

function AttemptDetail({ attempt }: { attempt: IntentAttempt }) {
  const { t } = useI18n()
  return (
    <div className="exploration-detail-item">
      <div className="exploration-detail-item-head">
        <Tag color={attempt.phase === 'running' ? 'processing' : attempt.phase === 'failed' ? 'error' : 'default'}>{attempt.phase}</Tag>
        <Typography.Text code>{attempt.id}</Typography.Text>
      </div>
      {attempt.conclusion ? <Typography.Text type="secondary">{t('run.graph.detail.conclusion')}: {attempt.conclusion}</Typography.Text> : null}
      {attempt.started_at ? <Typography.Text type="secondary">{t('common.started')}: <TimeText value={attempt.started_at} /></Typography.Text> : null}
      {attempt.finished_at && !isZeroTime(attempt.finished_at) ? <Typography.Text type="secondary">{t('common.finished')}: <TimeText value={attempt.finished_at} /></Typography.Text> : null}
      {attempt.error ? <Typography.Text type="danger">{t('common.errorLabel')}: {attempt.error}</Typography.Text> : null}
    </div>
  )
}

function ObservationDetail({ observation }: { observation: Observation }) {
  const { t } = useI18n()
  return (
    <div className="exploration-detail-item">
      <div className="exploration-detail-item-head">
        <Tag>{observation.kind}</Tag>
        <Typography.Text code>{observation.id}</Typography.Text>
      </div>
      <Typography.Text>{observation.summary}</Typography.Text>
      {observation.target ? <Typography.Text type="secondary">{t('common.target')}: {observation.target}</Typography.Text> : null}
    </div>
  )
}

function ArtifactDetail({ artifact }: { artifact: Artifact }) {
  const { t } = useI18n()
  return (
    <div className="exploration-detail-item">
      <div className="exploration-detail-item-head">
        <Typography.Text strong>{artifact.name}</Typography.Text>
        <Typography.Text code>{artifact.id}</Typography.Text>
      </div>
      <Typography.Text type="secondary">{t('run.graph.detail.size')}: {formatBytes(artifact.size)}</Typography.Text>
      {artifact.sha256 ? <Typography.Text type="secondary" className="exploration-detail-hash">{t('run.graph.detail.hash')}: {artifact.sha256}</Typography.Text> : null}
    </div>
  )
}

function DetailSection<T extends { id: string }>({ title, items, render }: {
  title: string
  items: T[]
  render: (item: T) => ReactNode
}) {
  const { t } = useI18n()
  return (
    <section className="exploration-detail-section">
      <Typography.Title level={5}>{title}</Typography.Title>
      <div className="exploration-detail-list">
        {items.length === 0
          ? <Typography.Text type="secondary">{t('run.graph.detail.none')}</Typography.Text>
          : items.map((item) => <Fragment key={item.id}>{render(item)}</Fragment>)}
      </div>
    </section>
  )
}

type NodeStatus = 'origin' | 'active' | 'fact' | 'negative' | 'open' | 'frontier'

const NODE_TONE: Record<NodeStatus, string> = {
  origin: 'origin',
  active: 'frontier',
  fact: 'fact',
  negative: 'negative',
  open: 'open',
  frontier: 'frontier',
}

const NODE_COLOR: Record<NodeStatus, string> = {
  origin: 'blue',
  active: 'processing',
  fact: 'green',
  negative: 'red',
  open: 'default',
  frontier: 'default',
}

function nodeStatus(node: ExplorationGraphNode, active: boolean): NodeStatus {
  if (node.kind === 'origin') return 'origin'
  if (active) return 'active'
  if (node.resultClaim?.kind === 'finding') return 'fact'
  if (node.resultClaim?.kind === 'negative' || node.intent?.state === 'abandoned') return 'negative'
  return node.intent?.state === 'open' ? 'open' : 'frontier'
}

function claimColor(kind: Claim['kind']): string {
  if (kind === 'finding') return 'green'
  if (kind === 'negative') return 'red'
  return 'default'
}

function nodeTitle(node: ExplorationGraphNode, t: (key: MessageKey) => string): string {
  if (node.kind === 'origin') return node.origin?.description || t('run.graph.originFallback')
  return node.intent?.description || t('run.graph.frontier')
}

function edgeClass(root: boolean, planned: boolean): string {
  if (planned) return 'exploration-graph-edge exploration-graph-edge-planned'
  return root
    ? 'exploration-graph-edge exploration-graph-edge-root'
    : 'exploration-graph-edge exploration-graph-edge-derived'
}
