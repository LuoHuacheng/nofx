import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import { expect, it } from 'vitest'

const runtime = {
  window: {} as {
    WorkspaceChart: {
      prototype: {
        query: (info: { ticker: string }, resolution: string) => string
      }
    }
  },
  URLSearchParams,
}
runInNewContext(readFileSync('public/planning/chart.js', 'utf8'), runtime)

it.each([
  ['OKX:BTC-USDT-SWAP', 'okx', 'BTC-USDT-SWAP'],
  ['BITGET:BTCUSDT', 'bitget', 'BTCUSDT'],
  ['BINANCE:BTCUSDT', 'binance', 'BTCUSDT'],
  ['HYPERLIQUID:xyz:GOOGL', 'hyperliquid', 'xyz:GOOGL'],
])('routes %s chart history to its own venue', (ticker, venue, symbol) => {
  const query = new URLSearchParams(
    runtime.window.WorkspaceChart.prototype.query({ ticker }, '60')
  )
  expect(query.get('venue')).toBe(venue)
  expect(query.get('symbol')).toBe(symbol)
  expect(query.get('interval')).toBe('1h')
})

it('rejects unsupported venues and path-like symbols before fetching', () => {
  for (const ticker of ['UNSUPPORTED:BTCUSDT', 'BITGET:../secret']) {
    expect(() =>
      runtime.window.WorkspaceChart.prototype.query({ ticker }, '60')
    ).toThrow('Invalid symbol format')
  }
})
