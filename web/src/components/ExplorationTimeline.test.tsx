import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { renderWithProviders } from '@/test/renderTestUtils'
import { ExplorationTimeline } from './ExplorationTimeline'

function event(seq: number, type: string, data: unknown, sessionId = ''): EventRecord {
  return {
    seq,
    run_id: 'run_1',
    session_id: sessionId,
    source: 'control',
    type,
    time: `2026-10-21T07:00:0${seq}Z`,
    data,
  }
}

const initialized = {
  phase: 'initialized',
  objective: 'find the flag',
  brief: { challenge_code: 'c-07', challenge_description: '请对目标远程登录服务进行安全测试与漏洞挖掘。' },
  targets: [{ id: 'web', addresses: ['10.0.0.5:80'] }],
  progress: { progress: 0, total: 2 },
  facts: [],
  frontiers: [],
  policy: { rounds: 6, max_workers: 2 },
}

describe('ExplorationTimeline', () => {
  it('starts with a single origin node before any frontier exists', () => {
    renderWithProviders(<ExplorationTimeline events={[event(1, 'exploration.initialized', initialized)]} />)

    expect(screen.getByTestId('exploration-graph')).toBeInTheDocument()
    expect(screen.getByText('find the flag')).toBeInTheDocument()
    expect(screen.getByText('web · 10.0.0.5:80')).toBeInTheDocument()
    expect(screen.getByText('请对目标远程登录服务进行安全测试与漏洞挖掘。')).toBeInTheDocument()
    expect(screen.getByText('等待 Planner 规划第一个 Frontier…')).toBeInTheDocument()
  })

  it('publishes brief-derived seed frontiers at initialization', () => {
    renderWithProviders(
      <ExplorationTimeline
        events={[event(1, 'exploration.initialized', {
          ...initialized,
          frontiers: [{
            id: 'seed-1-1',
            question: '授权目标 c-07 的对外服务是否可达，并返回可识别的协议横幅或登录提示？',
            target_hint: 'c-07',
            probe_key: 'service.banner',
            params: ['10.0.169.88:23'],
            expected_evidence: '连接建立并返回任何应用层字节',
            failure_evidence: '连接被拒绝、超时，或建立后返回零字节',
            max_tools: 8,
            max_duration_seconds: 90,
            status: 'open',
            round: 1,
          }],
        })]}
      />,
    )

    expect(screen.getByText('授权目标 c-07 的对外服务是否可达，并返回可识别的协议横幅或登录提示？')).toBeInTheDocument()
    expect(screen.getByText('service.banner · 10.0.169.88:23')).toBeInTheDocument()
    expect(screen.getByText('8 tools · 90s')).toBeInTheDocument()
  })

  it('grows the board from the origin through frontiers to facts', () => {
    renderWithProviders(
      <ExplorationTimeline
        events={[
          event(1, 'exploration.initialized', initialized),
          event(2, 'exploration.frontiers.created', {
            round: 1,
            items: [{
              id: 'frontier-a',
              question: '根路径是否返回 HTTP 200？',
              target_hint: 'web',
              probe_key: 'http.get',
              params: ['/'],
              expected_evidence: 'HTTP 200 与响应头',
              failure_evidence: 'HTTP 404',
              max_tools: 8,
              max_duration_seconds: 90,
              status: 'open',
              round: 1,
              fingerprint: 'abc',
            }],
          }),
          event(3, 'exploration.item.assigned', {
            round: 1,
            item_id: 'frontier-a',
            executor_id: 'executor-1',
          }),
          event(4, 'tool_execution_start', {
            type: 'tool_execution_start',
            toolName: 'bash',
            args: { command: 'curl -i http://10.0.0.5:80/' },
          }, 'executor-1'),
          event(5, 'exploration.report.received', {
            round: 1,
            item_id: 'frontier-a',
            verdict: 'supported',
          }),
          event(6, 'exploration.facts.merged', {
            round: 1,
            item_id: 'frontier-a',
            added: ['f-1-1'],
            corroborated: [],
            contested: [],
            facts: [{
              id: 'f-1-1',
              claim: '根路径返回 HTTP 200 且暴露登录表单',
              evidence: 'curl -i 输出 HTTP/1.1 200 OK',
              trust: 'observed',
              contested: false,
              targets: ['web'],
            }],
          }),
          event(7, 'backend.progress', { progress: 1, total: 2 }),
        ]}
      />,
    )

    expect(screen.getByText('探索起点')).toBeInTheDocument()
    expect(screen.getByText('根路径是否返回 HTTP 200？')).toBeInTheDocument()
    expect(screen.getByText('http.get · /')).toBeInTheDocument()
    expect(screen.getByText('8 tools · 90s')).toBeInTheDocument()
    expect(screen.getByText('根路径返回 HTTP 200 且暴露登录表单')).toBeInTheDocument()
    expect(screen.getByText('已观察')).toBeInTheDocument()
    expect(screen.getByText('点击任意节点查看该实验的完整契约与执行器正在做什么。')).toBeInTheDocument()
  })

  it('shows executor activity on the frontier node while it runs', () => {
    renderWithProviders(
      <ExplorationTimeline
        events={[
          event(1, 'exploration.initialized', initialized),
          event(2, 'exploration.frontiers.created', {
            round: 1,
            items: [{
              id: 'frontier-a',
              question: '端口 23 是否为 Telnet？',
              target_hint: 'web',
              probe_key: 'service.banner',
              params: ['10.0.0.5:23'],
              expected_evidence: 'IAC 协商字节',
              failure_evidence: '连接被拒绝',
              status: 'open',
              round: 1,
            }],
          }),
          event(3, 'exploration.executor.started', {
            round: 1,
            item_id: 'frontier-a',
            executor_id: 'executor-1',
            fresh_context: true,
          }),
          event(4, 'tool_execution_start', {
            type: 'tool_execution_start',
            toolName: 'bash',
            args: { command: 'ncat -v -w 8 10.0.0.5 23' },
          }, 'executor-1'),
        ]}
      />,
    )

    expect(screen.getByText('ncat -v -w 8 10.0.0.5 23')).toBeInTheDocument()
    expect(screen.getByText('bash')).toBeInTheDocument()
  })
})
