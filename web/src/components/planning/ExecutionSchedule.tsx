import type { Plan } from '../../lib/planning'

const number = (value: string) =>
  Number(value).toLocaleString('en-US', { maximumFractionDigits: 8 })
export function ExecutionSchedule({ plan }: { plan: Plan }) {
  const kind = plan.idea.execution.kind
  if (kind === 'market' || kind === 'limit' || !plan.orders.length) return null
  const timed = kind === 'twap'
  const total = Number(plan.quantity)
  return (
    <section
      className="planning-execution-band"
      aria-label={timed ? 'TWAP release schedule' : 'Ladder entry levels'}
    >
      <header>
        <div>
          <strong>
            {timed ? 'TWAP · timed releases' : 'Ladder · price levels'}
          </strong>
          <span>
            {plan.orders.length} child orders ·{' '}
            {timed ? 'one shared limit price' : 'one shared risk budget'}
          </span>
        </div>
        <small>Preview · starts only after approval</small>
      </header>
      <ol className="planning-execution-steps">
        {plan.orders.map((order) => (
          <li key={order.index}>
            <span className="planning-step-dot">{order.index}</span>
            <div>
              <b>
                {timed
                  ? `T+${order.offset_seconds}s`
                  : `Limit ${number(order.price)}`}
              </b>
              <span>
                {number(order.quantity)} {plan.snapshot.base_asset}{' '}
                <small>
                  ·{' '}
                  {total > 0
                    ? ((Number(order.quantity) / total) * 100).toFixed(0)
                    : '0'}
                  %
                </small>
              </span>
              <small>
                {timed
                  ? `Limit ${number(order.price)}`
                  : 'Placed together at start'}
              </small>
            </div>
          </li>
        ))}
      </ol>
      <p>
        {timed
          ? 'This seconds-based release axis is separate from candle time. T+0 is execution start, not a past candle. Each slice remains a limit order and may not fill.'
          : 'Each price level is a separate limit order. A level may never fill. All children share the displayed stop and target.'}
      </p>
    </section>
  )
}
