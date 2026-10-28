import { Card, Col, Row, Statistic, Tag, Typography } from 'antd'
import type { BranchView } from '@/utils/explorationTree'

export function BranchTreeTimeline({ view }: { view: BranchView }) {
  const active = view.branches.filter((branch) => branch.phase === 'open' || branch.phase === 'active').length
  const resolved = view.branches.filter((branch) => branch.outcome === 'supported' || branch.outcome === 'resolved').length
  const refuted = view.branches.filter((branch) => branch.outcome === 'refuted' || branch.outcome === 'blocked').length
  return (
    <div className="page-stack" data-testid="branch-tree-timeline">
      <Row gutter={[16, 16]}>
        <Col xs={12} md={6}><Card><Statistic title="Progress" value={view.progress?.current ?? 0} suffix={`/ ${view.progress?.total ?? 0}`} /></Card></Col>
        <Col xs={12} md={6}><Card><Statistic title="Active branches" value={active} /></Card></Col>
        <Col xs={12} md={6}><Card><Statistic title="Resolved branches" value={resolved} /></Card></Col>
        <Col xs={12} md={6}><Card><Statistic title="Refuted / blocked" value={refuted} /></Card></Col>
      </Row>
      <Card title="Branch tree">
        <div className="branch-tree-list">
          {view.branches.map((branch) => (
            <article key={branch.id} className="branch-tree-node">
              <div className="branch-tree-node-head">
                <Tag color={branch.outcome === 'supported' || branch.outcome === 'resolved' ? 'success' : branch.outcome === 'refuted' || branch.outcome === 'blocked' ? 'error' : 'processing'}>
                  {branch.outcome ?? branch.phase}
                </Tag>
                <Typography.Text code>{branch.topic}</Typography.Text>
                <Typography.Text type="secondary">{branch.kind ?? 'branch'} · depth {branch.depth} · {branch.scope}</Typography.Text>
              </div>
              {branch.goal ? <Typography.Text>{branch.goal}</Typography.Text> : null}
              {branch.hypothesis ? <Typography.Text type="secondary">{branch.hypothesis}</Typography.Text> : null}
            </article>
          ))}
        </div>
      </Card>
      <Card title="Durable knowledge">
        {view.knowledge.length === 0 ? (
          <Typography.Text type="secondary">No durable knowledge yet</Typography.Text>
        ) : (
          <div className="branch-tree-list">
            {view.knowledge.map((item) => (
              <article key={item.id} className="branch-tree-node">
                <div className="branch-tree-node-head">
                  <Tag>{item.trust}</Tag>
                  <Tag>{item.scope}</Tag>
                  <Typography.Text code>{item.topic}</Typography.Text>
                  <Typography.Text type="secondary">{item.kind}{item.version ? ` · v${item.version}` : ''}</Typography.Text>
                </div>
                <Typography.Text>{item.statement}</Typography.Text>
              </article>
            ))}
          </div>
        )}
      </Card>
      <Card title="Artifacts">
        {view.artifacts.length === 0 ? (
          <Typography.Text type="secondary">No archived artifacts yet</Typography.Text>
        ) : (
          <div className="branch-tree-list">
            {view.artifacts.map((artifact) => (
              <article key={`${artifact.id}:${artifact.seq}`} className="branch-tree-node">
                <div className="branch-tree-node-head">
                  <Tag>{artifact.mediaType ?? 'artifact'}</Tag>
                  <Typography.Text code>{artifact.name}</Typography.Text>
                  <Typography.Text type="secondary">{artifact.size ?? 0} bytes{artifact.redacted ? ' · redacted' : ''}</Typography.Text>
                </div>
                <Typography.Text type="secondary">{artifact.sha256 ?? artifact.id}</Typography.Text>
              </article>
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}
