import { Card, Col, Empty, Row, Space, Tag, Typography } from 'antd'
import type { GraphResponse, Intent } from '@/api/types'

export function FactIntentGraph({ graph }: { graph: GraphResponse }) {
  const claimed = new Map<number, Intent>()
  for (const intent of graph.intents) {
    if (intent.state === 'claimed' && intent.slot) claimed.set(intent.slot, intent)
  }
  const attempts = new Map(graph.attempts.map((attempt) => [attempt.intent_id, attempt]))

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
                      <Typography.Text type="secondary">{attempt?.phase ?? 'claimed'}</Typography.Text>
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

      <Card title="Fact / Intent graph">
        {graph.facts.length === 0 ? (
          <Empty description="No facts yet" />
        ) : (
          <Space orientation="vertical" size="middle" className="full-width">
            {graph.facts.map((fact) => (
              <Card size="small" key={fact.id}>
                <Space orientation="vertical" size="small" className="full-width">
                  <Space wrap>
                    <Tag color={fact.kind === 'finding' ? 'green' : fact.kind === 'negative' ? 'red' : 'default'}>
                      {fact.kind}
                    </Tag>
                    <Typography.Text code>{fact.id}</Typography.Text>
                  </Space>
                  <Typography.Paragraph className="objective-text">{fact.description}</Typography.Paragraph>
                  {fact.observation_ids?.length ? (
                    <Typography.Text type="secondary">
                      evidence: {fact.observation_ids.join(', ')}
                    </Typography.Text>
                  ) : null}
                </Space>
              </Card>
            ))}
          </Space>
        )}
      </Card>

      <Card title="Intents">
        {graph.intents.length === 0 ? (
          <Empty description="No intents yet" />
        ) : (
          <Space orientation="vertical" size="middle" className="full-width">
            {graph.intents.map((intent) => (
              <Card size="small" key={intent.id}>
                <Space orientation="vertical" size="small" className="full-width">
                  <Space wrap>
                    <Tag color={intent.state === 'concluded' ? 'green' : intent.state === 'claimed' ? 'blue' : 'default'}>
                      {intent.state}
                    </Tag>
                    <Tag>{intent.kind}</Tag>
                    {intent.slot ? <Tag>slot-{intent.slot}</Tag> : null}
                    <Typography.Text code>{intent.id}</Typography.Text>
                  </Space>
                  <Typography.Text strong>{intent.description}</Typography.Text>
                  <Typography.Text type="secondary">from: {intent.from_fact_ids.join(', ')}</Typography.Text>
                  {intent.result_fact_id ? (
                    <Typography.Text type="secondary">result: {intent.result_fact_id}</Typography.Text>
                  ) : null}
                </Space>
              </Card>
            ))}
          </Space>
        )}
      </Card>

      {graph.hints.length > 0 ? (
        <Card title="Hints">
          <Space orientation="vertical" size="small" className="full-width">
            {graph.hints.map((hint) => (
              <Typography.Text key={hint.id}>{hint.content}</Typography.Text>
            ))}
          </Space>
        </Card>
      ) : null}
    </div>
  )
}
