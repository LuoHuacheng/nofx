import { expect, it, vi } from 'vitest'
import {
  accountPositions,
  closeAccountPosition,
  nativeMarket,
} from './frontierAccount'
import type { LiveRun } from './planning'

const position = {
  symbol: 'BTCUSDT',
  side: 'short' as const,
  quantity: 0.01,
  entry: 83000,
  mark: 82000,
  pnl: 10,
  leverage: 3,
}
const run = (status: string): LiveRun =>
  ({
    id: 'run',
    exchange_id: 'account',
    state: 'stopping',
    plan: { snapshot: { symbol: 'BTC', venue: 'hyperliquid' } },
    orders: [{ role: 'entry', status }],
  }) as LiveRun

it('reads native NOFX position fields for the entire account', () => {
  expect(
    accountPositions([
      {
        symbol: 'BTCUSDT',
        side: 'short',
        positionAmt: 0.01,
        entryPrice: 83000,
        markPrice: 82000,
        unRealizedProfit: 10,
        leverage: 3,
      },
    ])
  ).toEqual([position])
  expect(nativeMarket('xyz:GOOGLUSDT', 'hyperliquid')).toBe('xyz:GOOGL')
  expect(nativeMarket('1000PEPEUSDT', 'binance')).toBe('1000PEPEUSDT')
  expect(() => accountPositions({})).toThrow()
  expect(() => accountPositions([{ symbol: 'BTC', side: 'short' }])).toThrow()
})

it('confirms all remaining TWAP entries are stopped before sending a close', async () => {
  const calls: string[] = []
  const read = vi
    .fn()
    .mockResolvedValueOnce([run('NEW')])
    .mockResolvedValueOnce([run('CANCELED')])
  await closeAccountPosition(
    'account',
    'hyperliquid',
    position,
    [run('scheduled')],
    {
      stop: async () => {
        calls.push('stop')
      },
      read,
      wait: async () => {
        calls.push('wait')
      },
      close: async () => {
        calls.push('close')
      },
    }
  )
  expect(calls).toEqual(['stop', 'wait', 'close'])
})

it('never closes while an entry receipt or cancellation is unconfirmed', async () => {
  const close = vi.fn()
  await expect(
    closeAccountPosition('account', 'hyperliquid', position, [run('UNKNOWN')], {
      stop: vi.fn().mockResolvedValue(undefined),
      read: vi.fn().mockResolvedValue([run('UNKNOWN')]),
      wait: vi.fn().mockResolvedValue(undefined),
      close,
    })
  ).rejects.toThrow('No close was sent')
  expect(close).not.toHaveBeenCalled()
})

it('does not stop other accounts or repeat an ambiguous close', async () => {
  const stop = vi.fn(),
    close = vi.fn().mockRejectedValue(new Error('timeout'))
  await expect(
    closeAccountPosition(
      'other-account',
      'hyperliquid',
      position,
      [run('NEW')],
      {
        stop,
        read: vi.fn(),
        wait: vi.fn(),
        close,
      }
    )
  ).rejects.toThrow('timeout')
  expect(stop).not.toHaveBeenCalled()
  expect(close).toHaveBeenCalledTimes(1)
})
