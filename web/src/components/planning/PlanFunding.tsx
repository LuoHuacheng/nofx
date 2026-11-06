import type { Plan } from '../../lib/planning'

const money = (value: number) =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })

export function planNotional(plan: Plan) {
  return plan.orders?.length
    ? plan.orders.reduce(
        (sum, order) => sum + Number(order.price) * Number(order.quantity),
        0
      )
    : Number(plan.idea.entry) * Number(plan.quantity)
}

export function PlanFunding({
  plan,
  compact = false,
}: {
  plan: Plan
  compact?: boolean
}) {
  const notional = planNotional(plan)
  const leverage = plan.constraints.leverage || 1
  const margin = notional / leverage
  const quote = plan.snapshot.quote_asset
  const available = Number(plan.constraints.available_margin)
  if (compact)
    return (
      <div className="frontier-funding-compact">
        <span>
          Position value <b>{leverage}x</b>
        </span>
        <strong>
          {money(notional)} <small>{quote}</small>
        </strong>
        <small>
          Est. margin {money(margin)} {quote}
        </small>
        {plan.constraints.planning_only && (
          <small>Equity preview · refresh before trading</small>
        )}
        {plan.constraints.entry_policy === 'ai500_trend_long' && (
          <small>AI500 · Long only</small>
        )}
      </div>
    )
  return (
    <section className="frontier-funding" aria-label="Plan funding">
      <div className="frontier-funding-equation">
        <div>
          <span>Est. margin</span>
          <strong>
            {money(margin)} <small>{quote}</small>
          </strong>
        </div>
        <span aria-hidden="true">×</span>
        <div>
          <span>Leverage</span>
          <strong>{leverage}x</strong>
        </div>
        <span aria-hidden="true">=</span>
        <div>
          <span>Position value</span>
          <strong>
            {money(notional)} <small>{quote}</small>
          </strong>
        </div>
      </div>
      {plan.constraints.entry_policy === 'ai500_trend_long' && (
        <p>
          AI500 · Long entries only. Each entry method is assessed separately.
        </p>
      )}
      {plan.constraints.planning_only ? (
        <p>
          Equity preview only: no free margin was available. Amounts use account
          equity as a reference, not available funds. Refresh and review with
          available margin before trading.
        </p>
      ) : Number.isFinite(available) && available > 0 ? (
        <p>
          Available margin at sizing: {money(available)} {quote} · This plan
          uses {money((margin / available) * 100)}% before costs. Fees and
          slippage are reserved separately.
        </p>
      ) : (
        <p>
          Saved plan size. Review again to size a new order from current
          available margin.
        </p>
      )}
      {plan.sizing_limit === 'liquidity' && (
        <p className="stop-text">
          This saved plan used the previous top-of-book size cap. New plans use
          available margin × leverage, with execution costs reserved.
        </p>
      )}
      {plan.sizing_limit === 'risk' && (
        <p>Size reduced by this plan’s maximum loss budget.</p>
      )}
    </section>
  )
}
