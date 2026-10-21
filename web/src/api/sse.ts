export interface SseFrame {
  id?: string
  event?: string
  data: string
}

export function parseSseFrame(raw: string): SseFrame | null {
  const normalized = raw.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
  const data: string[] = []
  let id: string | undefined
  let event: string | undefined
  for (const line of normalized.split('\n')) {
    if (line === '' || line.startsWith(':')) continue
    const separator = line.indexOf(':')
    const field = separator === -1 ? line : line.slice(0, separator)
    let value = separator === -1 ? '' : line.slice(separator + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'data') data.push(value)
    if (field === 'id') id = value
    if (field === 'event') event = value
  }
  if (data.length === 0) return null
  return { id, event, data: data.join('\n') }
}

export async function* parseSseStream(
  stream: ReadableStream<Uint8Array>,
): AsyncGenerator<SseFrame> {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      buffer = normalizeNewlines(buffer)
      let boundary = buffer.indexOf('\n\n')
      while (boundary !== -1) {
        const raw = buffer.slice(0, boundary)
        buffer = buffer.slice(boundary + 2)
        const frame = parseSseFrame(raw)
        if (frame) yield frame
        boundary = buffer.indexOf('\n\n')
      }
    }
    buffer += decoder.decode()
    buffer = normalizeNewlines(buffer)
    if (buffer.trim() !== '') {
      const frame = parseSseFrame(buffer)
      if (frame) yield frame
    }
  } finally {
    reader.releaseLock()
  }
}

function normalizeNewlines(value: string): string {
  const trailingCarriageReturn = value.endsWith('\r')
  const body = trailingCarriageReturn ? value.slice(0, -1) : value
  const normalized = body.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
  return trailingCarriageReturn ? `${normalized}\r` : normalized
}
