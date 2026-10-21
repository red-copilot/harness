import { describe, expect, it } from 'vitest'
import { parseSseFrame, parseSseStream } from './sse'

describe('parseSseFrame', () => {
  it('parses named events and multiline data', () => {
    const frame = parseSseFrame('id: 7\nevent: rpc.message\ndata: {"a":1}\ndata: {"b":2}')
    expect(frame).toEqual({ id: '7', event: 'rpc.message', data: '{"a":1}\n{"b":2}' })
  })

  it('handles CRLF and comments', () => {
    const frame = parseSseFrame(': ping\r\nevent: run.started\r\ndata: {"ok":true}\r\n')
    expect(frame).toEqual({ id: undefined, event: 'run.started', data: '{"ok":true}' })
  })

  it('ignores frames without data', () => {
    expect(parseSseFrame('event: ping\n')).toBeNull()
  })
})

describe('parseSseStream', () => {
  it('parses frames split across chunks', async () => {
    const encoder = new TextEncoder()
    const chunks = ['id: 1\nevent: run.started\ndata: {"a"', ':1}\n\nid: 2\nevent: run.finalized\ndata: {"b":2}\n\n']
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
        controller.close()
      },
    })
    const frames = []
    for await (const frame of parseSseStream(stream)) frames.push(frame)
    expect(frames).toHaveLength(2)
    expect(frames[0].event).toBe('run.started')
    expect(frames[0].data).toBe('{"a":1}')
    expect(frames[1].event).toBe('run.finalized')
  })
})
