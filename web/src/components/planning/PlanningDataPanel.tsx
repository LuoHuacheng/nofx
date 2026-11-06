import { memo, useEffect, useRef, useState } from 'react'
import { planningAPI } from '../../lib/planning'
import { formatRequestError } from '../../lib/requestErrors'

export interface PluginManifest {
  id: string
  name: string
  description: string
  enabled: boolean
}
interface HeatTotal {
  count: number
  notional: string
}
export interface PluginVisual {
  kind: string
  state: string
  coverage: string
  data_at: string
  window_start?: string
  mark: number
  bins?: {
    px: number
    longCost: number
    shortCost: number
    longLiq: number
    shortLiq: number
  }[]
  cells?: { row: number; column: number; long: HeatTotal; short: HeatTotal }[]
  rows: number
  columns: number
  win_min: number
  win_max: number
  cost_min: number
  cost_max: number
  entries?: {
    rank: number
    address: string
    netPnl: string
    realizedPnl: string
    fees: string
    funding: string
    volume: string
  }[]
}
export interface PluginResult {
  plugin: string
  venue: string
  symbol: string
  source: string
  observed: string
  notice: string
  metrics: { key: string; label: string; value: string; unit: string }[]
  visual?: PluginVisual
}
const num = (n: string | number) =>
  Number(n).toLocaleString('zh-CN', { maximumFractionDigits: 4 })
const money = (n: string | number) =>
  Number(n).toLocaleString('zh-CN', { maximumFractionDigits: 2 })
const series = [
  ['longCost', 'Long cost', '#2E8B57'],
  ['shortCost', 'Short cost', '#3575a8'],
  ['longLiq', 'Long estimated liquidation', '#D6433A'],
  ['shortLiq', 'Short estimated liquidation', '#9b6a14'],
] as const

export function PluginVisualization({ visual: v }: { visual: PluginVisual }) {
  const hover = useRef<HTMLDivElement>(null)
  const show = (label: string) => {
    if (hover.current) hover.current.textContent = label
  }

  if (v.kind === 'profit-leaderboard')
    return (
      <>
        <p>
          Data state：{v.state} · {v.window_start} to {v.data_at}
        </p>
        {v.state !== 'ready' ? (
          <p>The window is not ready; rankings are unavailable.</p>
        ) : (
          <div className="planning-plugin-table">
            <table>
              <thead>
                <tr>
                  <th scope="col">Rank</th>
                  <th scope="col">addresses</th>
                  <th scope="col">Cash net PnL / USDC</th>
                  <th scope="col">Realized PnL</th>
                  <th scope="col">Fees</th>
                  <th scope="col">Funding</th>
                </tr>
              </thead>
              <tbody>
                {v.entries?.map((row) => (
                  <tr key={row.address}>
                    <td>{row.rank}</td>
                    <td title={row.address}>
                      {row.address.slice(0, 8)}…{row.address.slice(-6)}
                    </td>
                    <td>{money(row.netPnl)}</td>
                    <td>{money(row.realizedPnl)}</td>
                    <td>{money(row.fees)}</td>
                    <td>{money(row.funding)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </>
    )
  if (v.kind === 'riskbins') {
    const bins = v.bins || [],
      min = bins[0]?.px,
      max = bins[bins.length - 1]?.px
    if (!bins.length)
      return <p>No price bins returned; no estimated data is drawn.</p>
    const peak = Math.max(
      1,
      ...bins.flatMap((b) => series.map(([key]) => b[key]))
    )
    const x = (p: number) => 70 + (860 * (p - min)) / (max - min || 1)
    return (
      <>
        <div className="planning-plugin-legend">
          {series.map(([key, label, color]) => (
            <span key={key}>
              <i style={{ background: color }} />
              {label}
            </span>
          ))}
        </div>
        <svg
          role="img"
          aria-label="Cost basis and estimated liquidation value by price"
          viewBox="0 0 1000 270"
        >
          <text x="10" y="15" fill="#637087">
            Value / USDC
          </text>
          {[0, 0.5, 1].map((t) => (
            <g key={t}>
              <line
                x1="70"
                x2="930"
                y1={220 - t * 180}
                y2={220 - t * 180}
                stroke="#ddd7cb"
              />
              <text x="2" y={224 - t * 180} fill="#637087" fontSize="11">
                {money(peak * t)}
              </text>
            </g>
          ))}
          {bins.map((b) =>
            series.map(([key, label, color], i) => (
              <line
                key={b.px + key}
                x1={x(b.px) + (i - 1.5) * 1.5}
                x2={x(b.px) + (i - 1.5) * 1.5}
                y1="220"
                y2={220 - (b[key] / peak) * 180}
                stroke={color}
                strokeWidth="1.5"
              >
                <title>
                  {label} · Price {num(b.px)} · {money(b[key])} USDC
                </title>
              </line>
            ))
          )}
          {[0, 0.25, 0.5, 0.75, 1].map((t) => (
            <text
              key={t}
              x={70 + t * 860}
              y="244"
              textAnchor="middle"
              fill="#637087"
              fontSize="11"
            >
              {num(min + t * (max - min))}
            </text>
          ))}
          {v.mark >= min && v.mark <= max && (
            <g>
              <line
                x1={x(v.mark)}
                x2={x(v.mark)}
                y1="30"
                y2="220"
                stroke="#9b6a14"
                strokeDasharray="4"
              />
              <text x={x(v.mark)} y="25" fill="#9b6a14" fontSize="12">
                Reference price {num(v.mark)}
              </text>
            </g>
          )}
          <text x="930" y="266" textAnchor="end" fill="#637087" fontSize="11">
            Price / USDC →
          </text>
        </svg>
      </>
    )
  }
  if (v.kind !== 'winrate-map') return <p>No chart data available.</p>
  if (
    !(
      v.rows > 0 &&
      v.rows <= 40 &&
      v.columns >= 3 &&
      v.columns <= 42 &&
      v.win_max > v.win_min &&
      v.cost_max > v.cost_min &&
      v.mark > 0
    )
  )
    return <p>Incomplete heatmap coordinates; visualization is unavailable.</p>
  const cells = v.cells || [],
    actualPeak = Math.max(
      0,
      ...cells.flatMap((c) => [
        Number(c.long.notional),
        Number(c.short.notional),
      ])
    ),
    peak = actualPeak || 1,
    lookup = new Map(cells.map((c) => [c.row + ':' + c.column, c]))
  const bounds = (row: number, col: number) => {
    const step = (v.cost_max - v.cost_min) / (v.columns - 2),
      lo = v.win_min + (row * (v.win_max - v.win_min)) / v.rows,
      hi = v.win_min + ((row + 1) * (v.win_max - v.win_min)) / v.rows
    const cost =
      col === 0
        ? '＜' + num((v.mark * v.cost_min) / 100)
        : col === v.columns - 1
          ? '≥' + num((v.mark * v.cost_max) / 100)
          : num((v.mark * (v.cost_min + (col - 1) * step)) / 100) +
            '–' +
            num((v.mark * (v.cost_min + col * step)) / 100)
    return { lo, hi, cost }
  }
  return (
    <>
      <p>
        Brighter cells indicate larger current positions; both sides share one
        scale. Historical win rate is not a forecast. Reference price{' '}
        {num(v.mark)} USDC。
      </p>
      <div className="planning-plugin-heat-pair">
        {(['long', 'short'] as const).map((side) => (
          <section key={side}>
            <b>
              {side === 'long' ? 'Longs' : 'Shorts'} · Current positions grouped
              by historical win rate and entry cost
            </b>
            <small>↑ Historical closed-round win rate / %</small>
            <svg
              role="img"
              aria-label={
                (side === 'long' ? 'Longs' : 'Shorts') +
                'historical win-rate position heatmap'
              }
              viewBox="0 0 620 310"
            >
              {[0, 0.5, 1].map((t) => (
                <text
                  key={t}
                  x="2"
                  y={250 - t * 220}
                  fill="#637087"
                  fontSize="12"
                >
                  {num(v.win_min + t * (v.win_max - v.win_min))}%
                </text>
              ))}
              {Array.from({ length: v.rows }, (_, r) =>
                Array.from({ length: v.columns }, (_, c) => {
                  const datum = lookup.get(r + ':' + c)?.[side],
                    b = bounds(r, c),
                    label = `${side === 'long' ? 'Longs' : 'Shorts'} · Historical win rate ${num(b.lo)}–${num(b.hi)}% · Entry cost ${b.cost} USDC · ${datum ? money(datum.notional) + ' USDC · ' + datum.count + ' addresses' : 'No data returned for this cell; do not treat it as zero'}`
                  return (
                    <rect
                      key={r + ':' + c}
                      role="button"
                      tabIndex={0}
                      aria-label={label}
                      x={60 + (c * 550) / v.columns}
                      y={25 + ((v.rows - r - 1) * 220) / v.rows}
                      width={Math.max(1, 550 / v.columns - 1)}
                      height={Math.max(1, 220 / v.rows - 1)}
                      fill={
                        datum
                          ? `rgba(${side === 'long' ? '120,182,251' : '242,140,140'},${0.06 + 0.94 * Math.sqrt(Number(datum.notional) / peak)})`
                          : '#e8e2d7'
                      }
                      onMouseEnter={() => show(label)}
                      onFocus={() => show(label)}
                      onClick={() => show(label)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault()
                          show(label)
                        }
                      }}
                    >
                      <title>{label}</title>
                    </rect>
                  )
                })
              )}
              {[0, 0.5, 1].map((t) => (
                <text
                  key={t}
                  x={60 + ((1 + t * (v.columns - 2)) * 550) / v.columns}
                  y="270"
                  textAnchor="middle"
                  fill="#637087"
                  fontSize="11"
                >
                  {t === 0 ? '＜' : t === 1 ? '≥' : ''}
                  {num(v.cost_min + t * (v.cost_max - v.cost_min))}%
                </text>
              ))}
              <text
                x="610"
                y="300"
                textAnchor="end"
                fill="#637087"
                fontSize="12"
              >
                Entry cost / reference price →
              </text>
            </svg>
          </section>
        ))}
      </div>
      <p>
        Shared amount scale: dark 0 → brightest {money(actualPeak)} USDC. Outer
        columns contain positions outside the cost range. 100% is the snapshot
        reference price.
      </p>
      <div ref={hover} className="planning-plugin-hover" role="status">
        Hover or select a cell to inspect historical win rate, entry cost and
        current position value.
      </div>
    </>
  )
}

export const PlanningDataPanel = memo(function PlanningDataPanel({
  exchangeID,
  symbol,
  venue,
  onWindowChange,
}: {
  exchangeID: string
  symbol: string
  venue: string
  onWindowChange?: (window: string) => void
}) {
  const [opened, setOpened] = useState(false),
    [items, setItems] = useState<PluginManifest[]>([]),
    [error, setError] = useState(''),
    [result, setResult] = useState<PluginResult | null>(null),
    [busy, setBusy] = useState(false),
    [windowDays, setWindow] = useState('7d')
  const serial = useRef(0)
  useEffect(() => {
    serial.current++
    setResult(null)
    setError('')
    setBusy(false)
    return () => {
      serial.current++
    }
  }, [exchangeID, symbol, windowDays])
  useEffect(() => {
    if (!opened) return
    let alive = true
    planningAPI<PluginManifest[]>('data-plugins')
      .then((rows) => {
        if (alive) setItems(rows)
      })
      .catch((e) => alive && setError(formatRequestError(e, 'en')))
    return () => {
      alive = false
    }
  }, [opened])
  async function toggle(item: PluginManifest) {
    const version = ++serial.current
    setBusy(true)
    setError('')
    setResult(null)
    try {
      const rows = await planningAPI<PluginManifest[]>('data-plugins', {
        id: item.id,
        enabled: !item.enabled,
      })
      if (version === serial.current) setItems(rows)
    } catch (e) {
      if (version === serial.current) setError(formatRequestError(e, 'en'))
    } finally {
      if (version === serial.current) setBusy(false)
    }
  }
  async function read(item: PluginManifest) {
    const version = ++serial.current
    setBusy(true)
    setError('')
    setResult(null)
    try {
      const q = new URLSearchParams({
        exchange_id: exchangeID,
        venue,
        symbol,
        plugin: item.id,
        window: windowDays,
      })
      const r = await planningAPI<PluginResult>('plugin-data?' + q)
      if (r.symbol !== symbol || r.venue !== venue)
        throw new Error('Component data does not match the selected market')
      if (version === serial.current) setResult(r)
    } catch (e) {
      if (version === serial.current) setError(formatRequestError(e, 'en'))
    } finally {
      if (version === serial.current) setBusy(false)
    }
  }
  return (
    <details
      className="planning-tools planning-data-panel"
      onToggle={(e) => setOpened(e.currentTarget.open)}
    >
      <summary>Data plugins · Funding, OI and holder analytics</summary>
      {opened && (
        <>
          <p>
            Enable a source to make it available to the planning assistant.
            These reads cannot place orders. Claw402 holder analytics only cover
            matching Hyperliquid markets. Paid reads use your NOFX fee wallet
            and are cached for five minutes.
          </p>
          <label>
            Time window
            <select
              aria-label="Plugin time window"
              value={windowDays}
              onChange={(e) => {
                setWindow(e.target.value)
                onWindowChange?.(e.target.value)
              }}
            >
              <option value="7d">Last 7 days</option>
              <option value="30d">Last 30 days</option>
            </select>
          </label>
          <div className="planning-plugin-switches">
            {items.map((item) => (
              <div key={item.id}>
                <label>
                  <input
                    type="checkbox"
                    checked={item.enabled}
                    disabled={busy}
                    onChange={() => toggle(item)}
                  />
                  {item.name}
                </label>
                <small>{item.description}</small>
                <button
                  disabled={
                    busy ||
                    !item.enabled ||
                    (item.id.startsWith('hyperdata_') &&
                      venue !== 'hyperliquid')
                  }
                  onClick={() => read(item)}
                >
                  Read and display
                </button>
              </div>
            ))}
          </div>
          {busy && <p role="status">Loading component…</p>}
          {error && (
            <p role="alert" className="stop-text">
              {error}
            </p>
          )}
          {result && (
            <section aria-label="Current plugin data">
              <div className="planning-plugin-metrics">
                {result.metrics.map((m) => (
                  <div key={m.key}>
                    <small>{m.label}</small>
                    <b>
                      {m.unit === 'fraction'
                        ? num(Number(m.value) * 100) + '%'
                        : num(m.value) + ' ' + m.unit}
                    </b>
                  </div>
                ))}
              </div>
              {result.visual && <PluginVisualization visual={result.visual} />}
              <p>{result.notice}</p>
              <small>
                Source {result.source} · Received at{' '}
                {new Date(result.observed).toLocaleString()}{' '}
                {result.visual &&
                  ' · Data as of ' +
                    result.visual.data_at +
                    ' · Coverage ' +
                    result.visual.coverage}
              </small>
            </section>
          )}
        </>
      )}
    </details>
  )
})
