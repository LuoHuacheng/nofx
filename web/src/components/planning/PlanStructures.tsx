import type { Plan } from '../../lib/planning'
import { validationSummary } from '../../lib/planningPresentation'
import { structureNames } from './PlanningActivity'
import { PlanFunding } from './PlanFunding'
export interface PlanCheck {
  id: string
  kind: string
  name: string
  status: string
  reason?: string
  plan_id?: string
}
export function PlanStructures({
  checks,
  plans,
  busy,
  selected,
  onSelect,
  onReview,
  reviewDisabled = false,
  connected = true,
  now = 0,
  showAllTypes = true,
}: {
  checks: PlanCheck[]
  plans: Plan[]
  busy: boolean
  selected: string
  onSelect: (plan: Plan) => void
  onReview?: (plan: Plan) => void
  reviewDisabled?: boolean
  connected?: boolean
  showAllTypes?: boolean
  now?: number
}) {
  const price = (value: string) =>
    Number(value).toLocaleString('en-US', { maximumFractionDigits: 8 })
  const money = (value: string) =>
    Number(value).toLocaleString('en-US', {
      maximumFractionDigits: Math.abs(Number(value)) < 0.01 ? 4 : 2,
    })
  if (!checks.length && !plans.length && !busy) return null
  return (
    <section
      className="planning-structures"
      aria-label="Entry structure comparison"
    >
      {['market', 'limit', 'ladder', 'twap']
        .filter(
          (kind) =>
            showAllTypes ||
            checks.some((check) => check.kind === kind) ||
            plans.some((plan) => plan.idea.execution.kind === kind)
        )
        .map((kind) => {
          const plan = plans.find((p) => p.idea.execution.kind === kind)
          const stale = plan ? Date.parse(plan.expires) <= now : false
          const check = checks.filter((row) => row.kind === kind).slice(-1)[0]
          return (
            <article
              key={kind}
              data-empty={!plan}
              className={plan?.id === selected ? 'selected' : ''}
            >
              <header>
                <strong>{structureNames[kind]}</strong>
                <span
                  className={
                    plan
                      ? stale
                        ? 'frontier-draft-stale'
                        : 'target-text'
                      : check?.status === 'rejected'
                        ? 'stop-text'
                        : ''
                  }
                >
                  {plan
                    ? stale
                      ? 'Refresh before trading'
                      : 'Validated draft'
                    : check?.status === 'deferred'
                      ? 'Wait for entry'
                      : check?.status === 'rejected'
                        ? 'Not feasible'
                        : busy
                          ? 'Evaluating'
                          : 'Not proposed'}
                </span>
              </header>
              {plan ? (
                <>
                  <b>{plan.idea.name}</b>
                  <span
                    className={`frontier-plan-side ${plan.idea.side === 'long' ? 'target-text' : 'stop-text'}`}
                  >
                    {plan.idea.side === 'long' ? 'Long' : 'Short'} ·{' '}
                    {plan.snapshot.symbol}
                  </span>
                  <p>
                    {plan.orders.length}{' '}
                    {kind === 'twap'
                      ? 'timed slices'
                      : kind === 'ladder'
                        ? 'price levels'
                        : 'entry'}{' '}
                    · Net R:R {Number(plan.reward_risk).toFixed(2)} : 1
                  </p>
                  <dl className="frontier-plan-levels">
                    <div>
                      <dt>{kind === 'market' ? 'Est. entry' : 'Entry'}</dt>
                      <dd>{price(plan.idea.entry)}</dd>
                    </div>
                    <div>
                      <dt>Stop</dt>
                      <dd className="stop-text">{price(plan.idea.stop)}</dd>
                    </div>
                    <div>
                      <dt>Target</dt>
                      <dd className="target-text">{price(plan.idea.target)}</dd>
                    </div>
                  </dl>
                  <p>
                    {kind === 'market'
                      ? 'Enters at market after approval. Actual fill may differ.'
                      : kind === 'twap'
                        ? `${plan.orders.length} slices · ${plan.idea.execution.interval_seconds || 0}s apart`
                        : kind === 'ladder'
                          ? 'Orders placed together at different prices.'
                          : 'Waits for your entry price.'}
                  </p>
                  {!connected && (
                    <div className="frontier-illustrative">
                      Illustrative size · connect an account to size this trade
                    </div>
                  )}
                  <PlanFunding plan={plan} compact />
                  <div className="frontier-plan-outcomes">
                    <span>
                      Planned loss{' '}
                      <b className="stop-text">−{money(plan.risk)}</b>
                    </span>
                    <span>
                      Target return{' '}
                      <b className="target-text">+{money(plan.reward)}</b>
                    </span>
                    <small>{plan.snapshot.quote_asset} · costs included</small>
                  </div>
                  <div className="frontier-plan-actions">
                    <button
                      onClick={() => onSelect(plan)}
                      aria-label={`View on chart: ${plan.idea.name}`}
                    >
                      {plan.id === selected ? 'On chart ✓' : 'View on chart'}
                    </button>
                    {onReview && (
                      <button
                        className="planning-primary"
                        disabled={reviewDisabled}
                        onClick={() => onReview(plan)}
                        aria-label={`Review ${structureNames[kind]}`}
                      >
                        {!connected
                          ? 'Connect to trade'
                          : stale
                            ? 'Refresh & review'
                            : 'Review order'}
                      </button>
                    )}
                  </div>
                </>
              ) : (
                <p>
                  {(check?.reason ? validationSummary(check.reason) : '') ||
                    (busy
                      ? 'Reading market context and checking this structure.'
                      : 'No candidate of this type was proposed.')}
                </p>
              )}
            </article>
          )
        })}
    </section>
  )
}
