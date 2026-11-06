import { afterEach, describe, it, expect, vi } from 'vitest'
import { streamPlanning } from './planning'
vi.mock('./api/helpers', () => ({
  getAuthHeaders: () => ({}),
  handleJSONResponse: async (r: Response) => {
    if (!r.ok) throw new Error('HTTP error')
    return r.json()
  },
}))
afterEach(() => vi.unstubAllGlobals())
function reply(chunks: string[]) {
  const encoder = new TextEncoder()
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          new ReadableStream({
            start(c) {
              for (const part of chunks) c.enqueue(encoder.encode(part))
              c.close()
            },
          }),
          { status: 200 }
        )
    )
  )
}
describe('planning stream completion', () => {
  it('rejects silent EOF without a terminal event and never retries', async () => {
    reply([
      'data: {"type":"tool_result","data":{"name":"get_candles","ok":true}}\n\n',
    ])
    await expect(
      streamPlanning({}, new AbortController().signal, () => {})
    ).rejects.toThrow('ended without a result')
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('accepts a CRLF terminal frame split across chunks', async () => {
    reply(['data: {"type":"done","data":{"ok":true}}\r', '\n\r\n'])
    const events: string[] = []
    await streamPlanning({}, new AbortController().signal, (e) =>
      events.push(e.type)
    )
    expect(events).toEqual(['done'])
  })
  it('surfaces a timeout error without making a second paid request', async () => {
    reply([
      'data: {"type":"error","data":{"message":"Planning reached its time limit"}}\n\n',
      'data: {"type":"done","data":{"ok":false}}\n\n',
    ])
    const events: unknown[] = []
    await streamPlanning({}, new AbortController().signal, (e) =>
      events.push(e)
    )
    expect(events[0]).toMatchObject({
      type: 'error',
      data: { message: 'Planning reached its time limit' },
    })
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
