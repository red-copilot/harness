import { Drawer, Empty, Space, Tag, Typography } from 'antd'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '@/i18n/I18nContext'
import {
  buildExplorationGraph,
  edgePath,
  factOf,
  frontierOf,
  ORIGIN_NODE_ID,
} from '@/utils/explorationGraph'
import type { GraphNode } from '@/utils/explorationGraph'
import type { HypothesisView } from '@/utils/exploration'

const EDGE_TONES = ['root', 'derived', 'planned', 'fact'] as const

export function ExplorationGraph({ view }: { view: HypothesisView }) {
  const { t } = useI18n()
  const graph = useMemo(() => buildExplorationGraph(view), [view])
  const [selected, setSelected] = useState<string | undefined>(undefined)
  const scrollRef = useRef<HTMLDivElement>(null)
  const nodeCount = graph.nodes.length
  useEffect(() => {
    const element = scrollRef.current
    if (element) element.scrollLeft = element.scrollWidth
  }, [nodeCount])
  const byId = useMemo(() => new Map(graph.nodes.map((node) => [node.id, node])), [graph.nodes])
  const selectedNode = selected ? byId.get(selected) : undefined

  return (
    <section className="exploration-graph" data-testid="exploration-graph">
      <div className="exploration-graph-legend">
        <Space wrap size={[6, 6]}>
          {EDGE_TONES.map((tone) => (
            <span key={tone} className={`exploration-graph-legend-item exploration-graph-legend-${tone}`}>
              <span className="exploration-graph-legend-line" aria-hidden="true" />
              {t(`run.graph.edge.${tone}` as 'run.graph.edge.root')}
            </span>
          ))}
        </Space>
        <Typography.Text type="secondary" className="exploration-graph-hint">
          {t('run.graph.hint')}
        </Typography.Text>
      </div>
      <div className="exploration-graph-scroll" ref={scrollRef}>
        <div
          className="exploration-graph-board"
          style={{ width: graph.width, height: graph.height }}
        >
          <svg
            className="exploration-graph-edges"
            width={graph.width}
            height={graph.height}
            aria-hidden="true"
          >
            {graph.edges.map((edge) => {
              const from = byId.get(edge.from)
              const to = byId.get(edge.to)
              if (!from || !to) return null
              return (
                <path
                  key={edge.id}
                  className={`exploration-graph-edge exploration-graph-edge-${edge.tone}`}
                  d={edgePath(from, to)}
                />
              )
            })}
          </svg>
          {graph.nodes.map((node) => (
            <button
              type="button"
              key={node.id}
              className={nodeClassName(node, selected === node.id)}
              style={{ left: node.x, top: node.y, width: node.width, minHeight: node.height }}
              onClick={() => setSelected(node.id)}
            >
              <GraphNodeBody node={node} view={view} />
            </button>
          ))}
          {graph.nodes.length === 1 ? (
            <div className="exploration-graph-waiting">{t('run.graph.waiting')}</div>
          ) : null}
        </div>
      </div>
      <Drawer
        open={Boolean(selectedNode)}
        onClose={() => setSelected(undefined)}
        size="large"
        title={t('run.graph.detailTitle')}
      >
        {selectedNode ? <GraphNodeDetail node={selectedNode} view={view} /> : <Empty />}
      </Drawer>
    </section>
  )
}

const BRIEF_KEYS = ['challenge_description', 'description', 'brief'] as const

function briefLine(view: HypothesisView): string {
  for (const key of BRIEF_KEYS) {
    const value = view.brief[key]?.trim()
    if (value) return value
  }
  return ''
}

function briefEntries(view: HypothesisView): [string, string][] {
  return Object.entries(view.brief)
    .filter(([, value]) => value.trim())
    .sort(([left], [right]) => left.localeCompare(right))
}

function nodeClassName(node: GraphNode, selected: boolean): string {
  const classes = ['exploration-graph-node', `exploration-graph-node-${node.kind}`]
  if (node.active) classes.push('exploration-graph-node-active')
  if (selected) classes.push('exploration-graph-node-selected')
  return classes.join(' ')
}

function GraphNodeBody({ node, view }: { node: GraphNode; view: HypothesisView }) {
  const { t } = useI18n()
  if (node.kind === 'origin') {
    return (
      <>
        <span className="exploration-graph-node-kind">{t('run.graph.origin')}</span>
        <Typography.Text strong className="exploration-graph-node-title">
          {view.objective || '—'}
        </Typography.Text>
        {briefLine(view) ? (
          <Typography.Text type="secondary" className="exploration-graph-node-brief" ellipsis={{ tooltip: briefLine(view) }}>
            {briefLine(view)}
          </Typography.Text>
        ) : null}
        <Space wrap size={[4, 4]}>
          {(view.targets ?? []).map((target) => (
            <Tag key={target.id}>{target.id} · {target.addresses.join(', ')}</Tag>
          ))}
          <Tag color="processing">
            {view.progress ? `${view.progress.current}/${view.progress.total}` : '0/0'}
          </Tag>
        </Space>
      </>
    )
  }
  if (node.kind === 'fact') {
    const fact = factOf(view, node.factId ?? '')
    return (
      <>
        <span className="exploration-graph-node-head">
          <span className="exploration-graph-node-kind">{t('run.graph.fact')}</span>
          <Tag color={trustColor(fact?.trust)}>
            {t(`run.graph.trust.${fact?.trust ?? 'observed'}` as 'run.graph.trust.observed')}
          </Tag>
        </span>
        <Typography.Text strong className="exploration-graph-node-title">
          {fact?.claim || fact?.id || node.factId}
        </Typography.Text>
        <Typography.Text code className="exploration-graph-node-code">
          {fact?.id ?? node.factId}
        </Typography.Text>
      </>
    )
  }
  const frontier = frontierOf(view, node.frontierId ?? '')
  const tools = view.activity[node.frontierId ?? '']?.tools ?? []
  const running = tools.filter((tool) => tool.running).at(-1) ?? tools.at(-1)
  return (
    <>
      <span className="exploration-graph-node-head">
        <span className="exploration-graph-node-kind">{t('run.graph.frontier')}</span>
        <Tag color={statusColor(frontier?.status)}>{statusLabel(t, frontier?.status)}</Tag>
      </span>
      <Typography.Text strong className="exploration-graph-node-title">
        {frontier?.question || frontier?.id || node.frontierId}
      </Typography.Text>
      <Space wrap size={[4, 4]}>
        <Tag color="blue">
          {`${frontier?.probeKey || 'probe'}${frontier?.params.length ? ` · ${frontier.params.join(', ')}` : ''}`}
        </Tag>
        <Tag>{`${frontier?.maxTools ?? 0} tools · ${frontier?.maxDurationSeconds ?? 0}s`}</Tag>
        {frontier?.executorId ? <Tag>{frontier.executorId}</Tag> : null}
      </Space>
      {running ? (
        <span className="exploration-graph-node-live">
          <Tag color={running.running ? 'processing' : 'default'}>{running.tool}</Tag>
          <Typography.Text type="secondary" ellipsis={{ tooltip: running.detail }}>
            {running.detail || t('run.graph.liveTool')}
          </Typography.Text>
        </span>
      ) : (
        <Typography.Text type="secondary" className="exploration-graph-node-live">
          {t('run.graph.tools', { count: tools.length })}
        </Typography.Text>
      )}
    </>
  )
}

function GraphNodeDetail({ node, view }: { node: GraphNode; view: HypothesisView }) {
  const { t } = useI18n()
  if (node.kind === 'origin') {
    return (
      <div className="exploration-detail">
        <DetailRow label={t('run.graph.detail.id')} value={ORIGIN_NODE_ID} code />
        <DetailRow label={t('run.graph.detail.objective')} value={view.objective || '—'} />
        {briefEntries(view).length ? (
          <div className="exploration-detail-row">
            <span className="exploration-detail-label">{t('run.graph.detail.brief')}</span>
            <div className="exploration-detail-value">
              {briefEntries(view).map(([key, value]) => (
                <div key={key}>
                  <Typography.Text code>{key}</Typography.Text>
                  <Typography.Paragraph>{value}</Typography.Paragraph>
                </div>
              ))}
            </div>
          </div>
        ) : null}
        <DetailRow
          label={t('run.graph.detail.targets')}
          value={(view.targets ?? []).map((target) => `${target.id} · ${target.addresses.join(', ')}`).join(' | ') || '—'}
        />
        <DetailRow
          label={t('run.graph.detail.authority')}
          value={view.progress ? `${view.progress.current}/${view.progress.total}` : '0/0'}
        />
        <Typography.Paragraph type="secondary">{t('run.hypothesis.authorityNote')}</Typography.Paragraph>
      </div>
    )
  }
  if (node.kind === 'fact') {
    const fact = factOf(view, node.factId ?? '')
    const trust = fact?.contested ? 'contested' : fact?.trust ?? 'observed'
    return (
      <div className="exploration-detail">
        <DetailRow label={t('run.graph.detail.id')} value={fact?.id ?? node.factId ?? ''} code />
        <DetailRow
          label={t('run.graph.detail.trust')}
          value={t(`run.graph.trust.${trust}` as 'run.graph.trust.observed')}
        />
        <DetailRow label={t('run.graph.detail.claim')} value={fact?.claim || '—'} />
        <DetailRow label={t('run.graph.detail.evidence')} value={fact?.evidence || '—'} />
        <DetailRow
          label={t('run.graph.detail.targets')}
          value={fact?.targets.join(', ') || '—'}
        />
        <DetailRow label={t('run.graph.detail.source')} value={fact?.sourceItemId || '—'} code />
      </div>
    )
  }
  const frontier = frontierOf(view, node.frontierId ?? '')
  const tools = view.activity[node.frontierId ?? '']?.tools ?? []
  const attempt = view.attempts.find((item) => item.itemId === node.frontierId)
  const produced = view.facts.filter((fact) => fact.sourceItemId === node.frontierId)
  return (
    <div className="exploration-detail">
      <DetailRow label={t('run.graph.detail.id')} value={frontier?.id ?? node.frontierId ?? ''} code />
      {frontier?.round ? (
        <DetailRow label={t('run.graph.detail.round', { round: frontier.round })} value={statusLabel(t, frontier.status)} />
      ) : null}
      <DetailRow label={t('run.graph.detail.hypothesis')} value={frontier?.question || '—'} />
      <DetailRow label={t('run.graph.detail.targets')} value={frontier?.targetHint || '—'} />
      <DetailRow
        label={t('run.graph.detail.probe')}
        value={`${frontier?.probeKey || '—'}${frontier?.params.length ? ` · ${frontier.params.join(', ')}` : ''}`}
        code
      />
      <DetailRow label={t('run.graph.detail.expected')} value={frontier?.expectedEvidence || '—'} />
      <DetailRow label={t('run.graph.detail.failure')} value={frontier?.failureEvidence || '—'} />
      <DetailRow
        label={t('run.graph.detail.budget')}
        value={`${frontier?.maxTools ?? 0} tools · ${frontier?.maxDurationSeconds ?? 0}s`}
      />
      {frontier?.parentId ? (
        <DetailRow label={t('run.graph.detail.parent')} value={frontier.parentId} code />
      ) : null}
      <DetailRow label={t('run.graph.detail.executor')} value={frontier?.executorId || '—'} />
      <DetailRow label={t('run.graph.detail.verdict')} value={attempt?.verdict || attempt?.status || '—'} />
      {attempt?.error ? <Typography.Text type="danger">{attempt.error}</Typography.Text> : null}
      <Typography.Title level={5}>{t('run.graph.live')}</Typography.Title>
      {tools.length ? (
        <ol className="exploration-activity">
          {tools.map((tool) => (
            <li key={`${tool.seq}-${tool.tool}`}>
              <Space size={4} wrap>
                <Tag color={tool.error ? 'error' : tool.running ? 'processing' : 'default'}>{tool.tool}</Tag>
                <Typography.Text type="secondary">#{tool.seq}</Typography.Text>
              </Space>
              {tool.detail ? (
                <Typography.Paragraph className="exploration-activity-detail">
                  {tool.detail}
                </Typography.Paragraph>
              ) : null}
            </li>
          ))}
        </ol>
      ) : (
        <Typography.Paragraph type="secondary">{t('run.graph.liveEmpty')}</Typography.Paragraph>
      )}
      <Typography.Title level={5}>{t('run.graph.detail.facts')}</Typography.Title>
      {produced.length ? (
        <ul className="exploration-detail-facts">
          {produced.map((fact) => (
            <li key={fact.id}>
              <Typography.Text code>{fact.id}</Typography.Text>
              <Typography.Paragraph>{fact.claim || '—'}</Typography.Paragraph>
            </li>
          ))}
        </ul>
      ) : (
        <Typography.Paragraph type="secondary">—</Typography.Paragraph>
      )}
    </div>
  )
}

function DetailRow({ label, value, code = false }: { label: string; value: string; code?: boolean }) {
  return (
    <div className="exploration-detail-row">
      <span className="exploration-detail-label">{label}</span>
      {code ? (
        <Typography.Text code className="exploration-detail-value">{value}</Typography.Text>
      ) : (
        <span className="exploration-detail-value">{value}</span>
      )}
    </div>
  )
}

function statusColor(status?: string): string | undefined {
  if (status === 'failed') return 'error'
  if (status === 'done') return 'success'
  if (status === 'assigned') return 'processing'
  return undefined
}

function statusLabel(t: ReturnType<typeof useI18n>['t'], status?: string): string {
  if (status === 'failed') return t('run.hypothesis.status.failed')
  if (status === 'done') return t('run.hypothesis.status.done')
  if (status === 'assigned') return t('run.hypothesis.status.assigned')
  return status ?? 'open'
}

function trustColor(trust?: string): string | undefined {
  if (trust === 'corroborated') return 'success'
  if (trust === 'refuted') return 'error'
  if (trust === 'contested') return 'warning'
  return 'default'
}
