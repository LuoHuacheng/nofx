import type { LiveRun } from '../../lib/planning'
import type { AccountObservation } from '../../lib/frontierWorkspace'
import { nativeMarket } from '../../lib/frontierAccount'

export function FrontierOrderBlocker({
  run,
  now,
  observation,
  checkError,
  busy,
  onCheck,
  onView,
  onRevoke,
  onAccount,
}: {
  run: LiveRun
  now: number
  observation?: AccountObservation | null
  checkError?: string
  busy: boolean
  onCheck: () => void
  onView: () => void
  onRevoke: () => void
  onAccount?: () => void
}) {
  const venue = run.plan.snapshot.venue
  const symbol = run.plan.snapshot.symbol
  const fresh =
    observation?.accountID === run.exchange_id &&
    Math.abs(now - observation.checkedAt) < 30000
  const position = fresh
    ? observation.positions.find(
        (p) => nativeMarket(p.symbol, venue) === nativeMarket(symbol, venue)
      )
    : undefined
  const approved = run.state === 'approved'
  const active = ['starting', 'running', 'stopping'].includes(run.state)
  const needsCheck = !active && !approved
  const title = position
    ? `${symbol} ${position.side === 'long' ? 'Long' : 'Short'} open · ${position.leverage}x`
    : approved
      ? `${symbol} order not submitted`
      : active
        ? run.state === 'stopping'
          ? 'Stopping remaining entries'
          : 'Entry orders in progress'
        : fresh
          ? `No open ${symbol} position · previous orders need verification`
          : `${symbol} trade status needs verification`
  const detail = position
    ? `${(position.quantity * position.mark).toLocaleString('en-US', { maximumFractionDigits: 2 })} ${run.plan.snapshot.quote_asset} position · You can keep exploring and generating plans.`
    : approved
      ? 'No order was sent from this approval. You can review any new plan; confirming it replaces this unused approval.'
      : active
        ? 'Track fills and scheduled entries in your account below.'
        : fresh
          ? 'Your latest account snapshot has no position in this market. Verify the previous entry and exit orders before placing another trade. You can still generate plans.'
          : 'Verify the previous trade before placing another order. You can still explore the market and generate plans.'
  return (
    <section
      className={`frontier-order-blocker ${(position && run.state !== 'needs_attention') || active ? 'is-active' : ''}`}
      aria-label="Current trade status"
    >
      <div>
        <strong>{title}</strong>
        <p>{detail}</p>
        {position && run.state === 'needs_attention' && (
          <small>
            Exit order status needs verification. Check the live position and
            protection below before placing another trade.
          </small>
        )}
        {checkError && <p role="alert">{checkError}</p>}
      </div>
      <div className="frontier-blocker-actions">
        {position ? (
          <button onClick={onAccount}>View position & orders</button>
        ) : (
          <button disabled={busy} onClick={onView}>
            View trade
          </button>
        )}
        {needsCheck && (
          <button disabled={busy} onClick={onCheck}>
            Verify status
          </button>
        )}
        {approved && (
          <button disabled={busy} onClick={onRevoke}>
            Cancel approval
          </button>
        )}
      </div>
    </section>
  )
}
