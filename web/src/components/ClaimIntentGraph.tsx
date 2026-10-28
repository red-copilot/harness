import { Card, Col, Empty, Row, Space, Tag, Typography } from 'antd'
import type { BoardResponse } from '@/api/types'

export function ClaimIntentGraph({ graph }: { graph: BoardResponse }) {
  const claims = graph.claims ?? []
  const intents = graph.intents ?? []
  const hints = graph.hints ?? []
  const claimed = new Map<number, BoardResponse["intents"][number]>()
  for (const intent of intents) {
    if (intent.state === 'claimed' && intent.slot) claimed.set(intent.slot, intent)
  }
  const attempts = new Map((graph.attempts ?? []).map((attempt) => [String(attempt.intent_id ?? ''), attempt]))

  return (
    <div className="page-stack">
      <Card title="Execution slots">
        <Row gutter={[16, 16]}>
          {[1, 2].map((slot) => {
            const intent = claimed.get(slot)
            const attempt = intent ? attempts.get(intent.id) : undefined
            return (
              <Col xs={24} md={12} key={slot}>
                <Card size="small" title={`slot-${slot}`}>
                  {intent ? (
                    <Space orientation="vertical" size="small">
                      <Tag color="blue">{intent.executor_id ?? 'executor'}</Tag>
                      <Typography.Text strong>{intent.description}</Typography.Text>
                      <Typography.Text type="secondary">{String(attempt?.phase ?? 'claimed')}</Typography.Text>
                    </Space>
                  ) : (
                    <Typography.Text type="secondary">Idle</Typography.Text>
                  )}
                </Card>
              </Col>
            )
          })}
        </Row>
      </Card>

      <Card title="Claim / Intent graph">
        {claims.length === 0 ? (
          <Empty description="No claims yet" />
        ) : (
          <Space orientation="vertical" size="middle" className="full-width">
            {claims.map((claim) => (
              <Card size="small" key={claim.id}>
                <Space orientation="vertical" size="small" className="full-width">
                  <Space wrap>
                    <Tag color={claim.kind === 'finding' ? 'green' : claim.kind === 'negative' ? 'red' : 'default'}>
                      {claim.kind}
                    </Tag>
                    <Typography.Text code>{claim.id}</Typography.Text>
                  </Space>
                  <Typography.Paragraph className="objective-text">{claim.description}</Typography.Paragraph>
                  {claim.observation_ids?.length ? (
                    <Typography.Text type="secondary">
                      evidence: {claim.observation_ids.join(', ')}
                    </Typography.Text>
                  ) : null}
                </Space>
              </Card>
            ))}
          </Space>
        )}
      </Card>

      <Card title="Intents">
        {intents.length === 0 ? (
          <Empty description="No intents yet" />
        ) : (
          <Space orientation="vertical" size="middle" className="full-width">
            {intents.map((intent) => (
              <Card size="small" key={intent.id}>
                <Space orientation="vertical" size="small" className="full-width">
                  <Space wrap>
                    <Tag color={intent.state === 'concluded' ? 'green' : intent.state === 'claimed' ? 'blue' : 'default'}>
                      {intent.state}
                    </Tag>
                    <Tag>{String(intent.kind ?? 'explore')}</Tag>
                    {intent.slot ? <Tag>slot-{intent.slot}</Tag> : null}
                    <Typography.Text code>{intent.id}</Typography.Text>
                  </Space>
                  <Typography.Text strong>{intent.description}</Typography.Text>
                  <Typography.Text type="secondary">from: {intent.from_claim_ids?.join(', ') ?? ''}</Typography.Text>
                  {intent.result_claim_id ? (
                    <Typography.Text type="secondary">result: {intent.result_claim_id}</Typography.Text>
                  ) : null}
                </Space>
              </Card>
            ))}
          </Space>
        )}
      </Card>

      {hints.length > 0 ? (
        <Card title="Hints">
          <Space orientation="vertical" size="small" className="full-width">
            {hints.map((hint) => (
              <Typography.Text key={String(hint.id ?? '')}>{String(hint.content ?? '')}</Typography.Text>
            ))}
          </Space>
        </Card>
      ) : null}
    </div>
  )
}
