import { useEffect, useRef, useState } from 'react'
import type { PlanningEvent } from '../../lib/planning'
import { validationSummary } from '../../lib/planningPresentation'

export interface ActivityItem {
  id: string
  label: string
  detail: string
  calculation?: string
  state: 'running' | 'done' | 'failed'
  started: number
  ended?: number
}
const titles: Record<string, string> = {
  get_candles: 'Read candlesticks',
  get_market: 'Read live order book',
  propose_plans: 'Validate trading candidates',
  draw_chart: 'Draw chart annotations',
  get_data_plugin: 'Read market data',
  get_entry_plugins: 'Inspect entry calculators',
  develop_entry_plugin: 'Build and test entry calculator',
  run_entry_plugin: 'Run entry calculator',
}
export const structureNames: Record<string, string> = {
  market: 'Market',
  limit: 'Single limit',
  ladder: 'Weighted ladder',
  twap: 'TWAP',
}
export function recordActivity(
  items: ActivityItem[],
  event: PlanningEvent,
  now = Date.now()
): ActivityItem[] {
  const d = event.data
  if (event.type === 'entry_policy')
    return [
      ...items.filter((item) => item.id !== 'entry_policy'),
      {
        id: 'entry_policy',
        label: 'AI500 · Long-only planning',
        detail: String(d.message),
        state: 'done' as const,
        started: now,
        ended: now,
      },
    ]
  if (event.type === 'planning_sizing')
    return [
      ...items.filter((item) => item.id !== 'planning_sizing'),
      {
        id: 'planning_sizing',
        label: 'Plan sizing · equity preview',
        detail: String(d.message),
        state: 'done' as const,
        started: now,
        ended: now,
      },
    ]
  if (event.type === 'model_transport')
    return [
      ...items,
      {
        id: 'model_transport',
        label: 'Model interface',
        detail:
          d.transport === 'text_tools'
            ? 'Structured text tool compatibility'
            : 'Native tool calls',
        state: 'done' as const,
        started: now,
        ended: now,
      },
    ]
  if (event.type === 'plugin_data') {
    const names: Record<string, string> = {
      funding: 'Funding rate',
      open_interest: 'Open interest',
      hyperdata_cost: 'Cost and liquidation',
      hyperdata_profit: 'Profit leaderboard',
      hyperdata_winrate: 'Holder win rate',
    }
    const metrics = Array.isArray(d.metrics)
      ? (d.metrics as { label: string; value: string; unit: string }[])
      : []
    const id = `plugin_${String(d.plugin)}`
    const previous = items.find((row) => row.id === id)
    const row: ActivityItem = {
      id,
      label: names[String(d.plugin)] || 'Market data plugin',
      state: d.error ? 'failed' : 'done',
      started: previous?.started || now,
      ended: now,
      detail: d.error
        ? String(d.error)
        : metrics
            .slice(0, 3)
            .map((metric) => {
              const value = Number(metric.value)
              const display = Number.isFinite(value)
                ? metric.unit === 'fraction'
                  ? `${(value * 100).toFixed(5)}%`
                  : `${value.toLocaleString('en-US', { maximumFractionDigits: 6 })} ${metric.unit}`
                : metric.value
              return `${metric.label}: ${display}`
            })
            .join(' · '),
    }
    return previous
      ? items.map((item) => (item.id === id ? row : item))
      : [...items, row].slice(-100)
  }
  const endModels = (rows: ActivityItem[]) =>
    rows.map((row) =>
      row.id.startsWith('model_') && row.state === 'running'
        ? {
            ...row,
            state: 'done' as const,
            ended: now,
            detail: 'Model response received',
          }
        : row
    )
  const upsert = (
    rows: ActivityItem[],
    item: Partial<ActivityItem> & {
      id: string
      label: string
      state: ActivityItem['state']
    }
  ) => {
    const index = rows.findIndex((row) => row.id === item.id)
    if (index < 0)
      return [...rows, { started: now, detail: '', ...item }].slice(-100)
    return rows.map((row, i) => (i === index ? { ...row, ...item } : row))
  }
  if (event.type === 'iteration')
    return upsert(endModels(items), {
      id: `model_${d.round}`,
      label: `Model planning · round ${d.round}`,
      detail: 'Evaluating the observed market and available tools',
      state: 'running',
    })
  if (event.type === 'status') {
    if (typeof d.elapsed_seconds !== 'number') return items
    return items.map((row) =>
      row.id.startsWith('model_') && row.state === 'running'
        ? {
            ...row,
            detail: `Waiting for model response · ${d.elapsed_seconds}s`,
          }
        : row
    )
  }
  if (event.type === 'tool_start')
    return upsert(endModels(items), {
      id: String(d.id),
      label: titles[String(d.name)] || 'Run planning tool',
      detail: 'In progress',
      state: 'running',
    })
  if (event.type === 'tool_progress')
    return upsert(items, {
      id: String(d.id),
      label: titles[String(d.name)] || 'Run planning tool',
      detail: `${d.interval} · ${d.count} / ${d.target_count} candles · ${d.scope}`,
      state: 'running',
    })
  if (event.type === 'tool_result') {
    const detail = [
      d.interval,
      typeof d.count === 'number'
        ? `${d.count}${typeof d.target_count === 'number' ? ` / ${d.target_count}` : ''} candles`
        : undefined,
      d.truncated ? 'Limited exchange history' : undefined,
      d.scope,
    ]
      .filter(Boolean)
      .join(' · ')
    return upsert(items, {
      id: String(d.id),
      label: titles[String(d.name)] || 'Run planning tool',
      state: d.ok ? 'done' : 'failed',
      ended: now,
      detail: d.reason
        ? validationSummary(String(d.reason))
        : detail || (d.ok ? 'Completed' : 'Could not complete this step'),
      calculation: d.reason ? String(d.reason) : undefined,
    })
  }
  if (event.type === 'candidate_start' || event.type === 'candidate_result')
    return upsert(endModels(items), {
      id: String(d.id),
      label:
        d.status === 'deferred'
          ? `${structureNames[String(d.kind)] || 'Entry'} · Wait`
          : `${structureNames[String(d.kind)] || 'Candidate'} · ${String(d.name)}`,
      state:
        event.type === 'candidate_start'
          ? 'running'
          : d.status === 'rejected'
            ? 'failed'
            : 'done',
      ...(event.type === 'candidate_result' ? { ended: now } : {}),
      calculation: d.reason ? String(d.reason) : undefined,
      detail: d.reason
        ? validationSummary(String(d.reason))
        : event.type === 'candidate_start'
          ? 'Checking costs, child orders and risk'
          : d.status === 'reused'
            ? 'Already saved; duplicate skipped'
            : 'Passed mathematical validation',
    })
  if (event.type === 'plans') {
    const count = Array.isArray(d.plans) ? d.plans.length : 0
    const deferred = Array.isArray(d.checks)
      ? d.checks.filter((check) => check.status === 'deferred').length
      : 0
    return upsert(items, {
      id: `save_${items.filter((row) => row.id.startsWith('save_')).length}`,
      label: d.waiting
        ? 'Waiting for entry conditions'
        : count
          ? 'Save validated drafts'
          : 'No new drafts',
      state: 'done',
      ended: now,
      detail: d.waiting
        ? `${deferred} entry types waiting · no orders submitted`
        : `${count} new drafts${deferred ? ` · ${deferred} entry types waiting` : ''} · ${Array.isArray(d.rejected) ? d.rejected.length : 0} rejected candidates · no orders submitted`,
    })
  }
  if (
    event.type === 'outcome' ||
    event.type === 'error' ||
    event.type === 'cancelled'
  ) {
    const ended = (event.type === 'outcome' ? endModels(items) : items).map(
      (row) =>
        row.state === 'running'
          ? {
              ...row,
              state: 'failed' as const,
              ended: now,
              detail:
                event.type === 'cancelled'
                  ? 'Stopped by you'
                  : 'Stopped before completion',
            }
          : row
    )
    return upsert(ended, {
      id: 'outcome',
      label:
        event.type === 'error'
          ? 'Planning interrupted'
          : event.type === 'cancelled'
            ? 'Planning stopped'
            : d.waiting && d.stop_reason === 'completed'
              ? 'Waiting for an entry'
              : 'Planning complete',
      state: event.type === 'error' ? 'failed' : 'done',
      ended: now,
      detail:
        event.type === 'error'
          ? String(d.message)
          : event.type === 'cancelled'
            ? 'Saved drafts remain available. No orders were submitted.'
            : Number(d.drafts) > 0
              ? `${d.drafts} drafts saved · awaiting your review`
              : d.waiting
                ? 'No order to approve. Review the entry conditions below.'
                : 'No drafts saved. Review the explanation; no orders submitted.',
    })
  }
  return items
}
export function PlanningActivity({
  items,
  busy,
}: {
  items: ActivityItem[]
  busy: boolean
}) {
  const [now, setNow] = useState(Date.now)
  const [expanded, setExpanded] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const follow = useRef(true)
  useEffect(() => {
    if (!busy) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [busy])
  useEffect(() => {
    if (follow.current && box.current)
      box.current.scrollTop = box.current.scrollHeight
  }, [items, now])
  if (!busy && !items.length) return null
  const rows = busy || expanded ? items : items.slice(-4)
  return (
    <section
      className="planning-live-activity"
      aria-label="Live planning activity"
    >
      <header>
        <strong>
          <i className={busy ? 'activity-live-dot' : ''} />
          {busy ? 'Live activity' : 'Activity'}
        </strong>
        <span>
          {items.filter((row) => row.state === 'done').length} completed
          {busy ? ' · streaming' : ''}
        </span>
        {!busy && items.length > 4 && (
          <button onClick={() => setExpanded(!expanded)}>
            {expanded ? 'Show less' : 'All steps'}
          </button>
        )}
      </header>
      <div
        className="planning-activity-list"
        ref={box}
        onScroll={() => {
          const node = box.current
          if (node)
            follow.current =
              node.scrollHeight - node.scrollTop - node.clientHeight < 40
        }}
        role="log"
        aria-live="polite"
        aria-relevant="additions text"
      >
        {rows.map((row) => (
          <article
            key={row.id}
            className={`planning-activity-item ${row.state}`}
          >
            <span className="activity-state" aria-label={row.state}>
              {row.state === 'running' ? (
                <i className="planning-spinner" />
              ) : row.state === 'done' ? (
                '✓'
              ) : (
                '!'
              )}
            </span>
            <div>
              <b>{row.label}</b>
              <p title={row.calculation}>{row.detail}</p>
            </div>
            <time>
              {Math.max(
                0,
                Math.floor(((row.ended || now) - row.started) / 1000)
              )}
              s
            </time>
          </article>
        ))}
      </div>
    </section>
  )
}
