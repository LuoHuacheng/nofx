import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { planningAPI, CloseNotSubmittedError } from './planning'

vi.mock('./api/helpers', () => ({
  getAuthHeaders: () => ({}),
  handleJSONResponse: async (r: Response) => {
    if (!r.ok) throw new Error('Server rejected the request')
    return r.json()
  },
}))

beforeEach(() => vi.useFakeTimers())
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function stallRequest() {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (_url, options: RequestInit) =>
        new Promise((_resolve, reject) => {
          options.signal?.addEventListener('abort', () =>
            reject(new DOMException('Aborted', 'AbortError'))
          )
        })
    )
  )
}

describe('planning request deadlines', () => {
  it('releases an unresponsive read without retrying', async () => {
    stallRequest()
    const result = expect(planningAPI('state')).rejects.toThrow(
      'Request timed out'
    )
    await vi.advanceTimersByTimeAsync(35000)
    await result
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })
  it.each(['approve', 'execute', 'stop', 'reconcile', 'close-position'])(
    'keeps %s timeouts explicitly unconfirmed and never replays the action',
    async (action) => {
      stallRequest()
      const result = expect(
        planningAPI(action, { id: 'existing-run' })
      ).rejects.toThrow('Its result is unconfirmed. No automatic retry')
      await vi.advanceTimersByTimeAsync(45000)
      await result
      expect(fetch).toHaveBeenCalledTimes(1)
    }
  )
  it('does not imply an unpaid result when a GET data purchase times out', async () => {
    stallRequest()
    const result = expect(planningAPI('plugin-data?id=cost')).rejects.toThrow(
      'Payment result is unconfirmed'
    )
    await vi.advanceTimersByTimeAsync(35000)
    await result
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('keeps the deadline until the response body is received', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, options: RequestInit) => ({
        ok: true,
        json: () =>
          new Promise((_resolve, reject) =>
            options.signal?.addEventListener('abort', () =>
              reject(new DOMException('Aborted', 'AbortError'))
            )
          ),
      }))
    )
    const result = expect(
      planningAPI('check-status', { id: 'old' })
    ).rejects.toThrow('previous trade remains unverified')
    await vi.advanceTimersByTimeAsync(35000)
    await result
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('clears the timer on success and preserves server errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(new Response('{"state":"completed"}'))
        .mockResolvedValueOnce(new Response('', { status: 409 }))
    )
    await expect(planningAPI('check-status', { id: 'old' })).resolves.toEqual({
      state: 'completed',
    })
    expect(vi.getTimerCount()).toBe(0)
    await expect(planningAPI('execute', { id: 'old' })).rejects.toThrow(
      'Server rejected the request'
    )
    expect(vi.getTimerCount()).toBe(0)
    expect(fetch).toHaveBeenCalledTimes(2)
  })
})

it('preserves explicit close rejection without treating every HTTP error as unsent', async () => {
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: 'Position changed',
            close_state: 'not_submitted',
          }),
          { status: 400 }
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: 'Close is unconfirmed',
            close_state: 'unconfirmed',
          }),
          { status: 400 }
        )
      )
      .mockResolvedValueOnce(
        new Response('gateway unavailable', { status: 502 })
      )
  )
  await expect(planningAPI('close-position', {})).rejects.toBeInstanceOf(
    CloseNotSubmittedError
  )
  await expect(planningAPI('close-position', {})).rejects.not.toBeInstanceOf(
    CloseNotSubmittedError
  )
  await expect(planningAPI('close-position', {})).rejects.not.toBeInstanceOf(
    CloseNotSubmittedError
  )
  expect(fetch).toHaveBeenCalledTimes(3)
})
