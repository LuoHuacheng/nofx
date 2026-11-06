import type { LiveRun } from './planning'

export interface AccountPosition {
  symbol: string
  side: 'long' | 'short'
  quantity: number
  entry: number
  mark: number
  pnl: number
  leverage: number
}

// NOFX native traders and normalized API views use different field names.
export function accountPositions(value: unknown): AccountPosition[] {
  if (!Array.isArray(value)) throw new Error('Position response is incomplete')
  return value
    .map((item) => {
      const p = item as Record<string, unknown>
      if (!p || typeof p.symbol !== 'string')
        throw new Error('Invalid position symbol')
      const side = String(p.side).toLowerCase()
      const number = (...keys: string[]) => {
        const raw = keys
          .map((key) => p[key])
          .find((v) => v !== undefined && v !== null)
        if ((typeof raw !== 'string' && typeof raw !== 'number') || raw === '')
          throw new Error('Incomplete position values')
        const parsed = Number(raw)
        if (!Number.isFinite(parsed)) throw new Error('Invalid position value')
        return parsed
      }
      if (side !== 'long' && side !== 'short')
        throw new Error('Invalid position direction')
      const position: AccountPosition = {
        symbol: p.symbol,
        side,
        quantity: Math.abs(number('quantity', 'positionAmt', 'size')),
        entry: number('entry_price', 'entryPrice'),
        mark: number('mark_price', 'markPrice'),
        pnl: number('unrealized_pnl', 'unRealizedProfit'),
        leverage: number('leverage'),
      }
      if (position.entry <= 0 || position.mark <= 0 || position.leverage <= 0)
        throw new Error('Invalid position prices or leverage')
      return position
    })
    .filter((p) => p.quantity > 0)
}

// Expand JavaScript scientific notation without rounding exchange observations.
export function positionDecimal(value: number): string {
  if (!Number.isFinite(value) || value <= 0)
    throw new Error('Invalid position value; no close was sent.')
  const [coefficient, exponent] = String(value).split('e')
  if (exponent === undefined) return coefficient
  const [whole, fraction = ''] = coefficient.split('.')
  const digits = whole + fraction
  const point = whole.length + Number(exponent)
  if (point <= 0) return '0.' + '0'.repeat(-point) + digits
  if (point >= digits.length) return digits + '0'.repeat(point - digits.length)
  return digits.slice(0, point) + '.' + digits.slice(point)
}

export function nativeMarket(symbol: string, venue: string): string {
  const s = symbol.toUpperCase()
  if (venue === 'okx' && !s.endsWith('-USDT-SWAP'))
    return `${s.replace(/USDT$/, '')}-USDT-SWAP`
  const stripped = venue === 'hyperliquid' ? s.replace(/USDT$/, '') : s
  return stripped.replace(/^XYZ:/, 'xyz:')
}

const terminal = new Set(['completed', 'cancelled', 'expired'])
export function pendingEntry(run: LiveRun): boolean {
  return run.orders.some(
    (o) =>
      o.role === 'entry' &&
      !['FILLED', 'CANCELED', 'REJECTED', 'EXPIRED'].includes(o.status)
  )
}

// Explicit manual close only. Cancel the matching finite basket first so a
// delayed TWAP slice cannot reopen the position after the user's close.
export async function closeAccountPosition(
  accountID: string,
  venue: string,
  position: AccountPosition,
  runs: LiveRun[],
  actions: {
    stop: (run: LiveRun) => Promise<unknown>
    read: () => Promise<LiveRun[]>
    wait: () => Promise<void>
    close: () => Promise<unknown>
  }
) {
  const matching = runs.filter(
    (r) =>
      r.exchange_id === accountID &&
      nativeMarket(r.plan.snapshot.symbol, venue) ===
        nativeMarket(position.symbol, venue) &&
      !terminal.has(r.state)
  )
  for (const run of matching) await actions.stop(run)
  if (matching.length) {
    let cleared = false
    for (let attempt = 0; attempt < 20; attempt++) {
      const current = await actions.read()
      cleared = matching.every((old) => {
        const run = current.find(
          (r) => r.id === old.id && r.exchange_id === accountID
        )
        return (
          !!run &&
          run.state !== 'approved' &&
          run.state !== 'running' &&
          !pendingEntry(run) &&
          (run.orders.length > 0 || terminal.has(run.state))
        )
      })
      if (cleared) break
      await actions.wait()
    }
    if (!cleared)
      throw new Error(
        'Entry cancellation is not confirmed. No close was sent. Check the exchange before retrying.'
      )
  }
  // Never retry a close automatically, including after an ambiguous timeout.
  await actions.close()
}
