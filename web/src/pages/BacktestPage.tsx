import { useCallback, useEffect, useState } from 'react'
import {
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { toast } from 'sonner'
import {
  backtestApi,
  type BacktestMetrics,
  type BacktestRun,
  type BacktestTrade,
} from '../lib/api/backtest'
import { configApi } from '../lib/api/config'
import { strategyApi } from '../lib/api/strategies'
import type { AIModel, Exchange } from '../types'
import type { Strategy } from '../types/strategy'

const TIMEFRAMES = [
  '1m',
  '3m',
  '5m',
  '15m',
  '30m',
  '1h',
  '2h',
  '4h',
  '6h',
  '8h',
  '12h',
  '1d',
]

const STATUS_COLORS: Record<string, string> = {
  pending: '#8b8b8b',
  running: '#2563eb',
  paused: '#d97706',
  completed: '#16a34a',
  failed: '#dc2626',
  cancelled: '#8b8b8b',
}

function toLocalInputValue(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function MetricCard({
  label,
  value,
  hint,
}: {
  label: string
  value: string
  hint?: string
}) {
  return (
    <div className="rounded-lg border border-[#E5E1D8] bg-white p-4">
      <div className="text-xs text-[#8b8b8b]">{label}</div>
      <div className="mt-1 text-xl font-semibold" style={{ color: '#1A1813' }}>
        {value}
      </div>
      {hint ? (
        <div className="mt-0.5 text-xs text-[#8b8b8b]">{hint}</div>
      ) : null}
    </div>
  )
}

export default function BacktestPage() {
  const [symbols, setSymbols] = useState('BTCUSDT')
  const [timeframe, setTimeframe] = useState('5m')
  const [startTime, setStartTime] = useState(() => {
    const d = new Date()
    d.setDate(d.getDate() - 7)
    return toLocalInputValue(d.toISOString())
  })
  const [endTime, setEndTime] = useState(() =>
    toLocalInputValue(new Date().toISOString())
  )
  const [initialBalance, setInitialBalance] = useState('1000')
  const [strategyId, setStrategyId] = useState('')
  const [modelId, setModelId] = useState('')
  const [exchangeId, setExchangeId] = useState('')

  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [models, setModels] = useState<AIModel[]>([])
  const [exchanges, setExchanges] = useState<Exchange[]>([])
  const [runs, setRuns] = useState<BacktestRun[]>([])
  const [starting, setStarting] = useState(false)

  // Selected run detail
  const [selectedRun, setSelectedRun] = useState<BacktestRun | null>(null)
  const [metrics, setMetrics] = useState<BacktestMetrics | null>(null)
  const [equity, setEquity] = useState<{ time: string; equity: number }[]>([])
  const [trades, setTrades] = useState<BacktestTrade[]>([])
  const [trace, setTrace] = useState<string[]>([])

  const refreshRuns = useCallback(async () => {
    try {
      const { runs } = await backtestApi.list(1, 50)
      setRuns(runs)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to load runs')
    }
  }, [])

  useEffect(() => {
    Promise.all([
      strategyApi.getStrategies().catch(() => []),
      configApi.getModelConfigs().catch(() => []),
      configApi.getExchangeConfigs().catch(() => []),
    ]).then(([sts, mds, exs]) => {
      setStrategies(sts)
      setModels(mds)
      setExchanges(exs)
      if (sts.length > 0) setStrategyId(sts[0].id)
      if (mds.length > 0) setModelId(mds[0].id)
      if (exs.length > 0) setExchangeId(exs[0].id)
    })
    refreshRuns()
  }, [refreshRuns])

  // Poll running runs every 3s so statuses stay fresh.
  useEffect(() => {
    const hasActive = runs.some(
      (r) => r.status === 'running' || r.status === 'pending'
    )
    if (!hasActive) return
    const id = setInterval(refreshRuns, 3000)
    return () => clearInterval(id)
  }, [runs, refreshRuns])

  const loadDetail = useCallback(async (run: BacktestRun) => {
    setSelectedRun(run)
    setMetrics(null)
    setEquity([])
    setTrades([])
    setTrace([])
    if (run.status === 'running' || run.status === 'pending') return
    try {
      const [m, eq, tr, tc] = await Promise.all([
        backtestApi.metrics(run.id),
        backtestApi.equity(run.id, 5000),
        backtestApi.trades(run.id, 1000),
        backtestApi.trace(run.id, 100),
      ])
      setMetrics(m)
      setEquity(
        eq.map((p) => ({
          time: new Date(p.time).toLocaleString(),
          equity: p.equity,
        }))
      )
      setTrades(tr)
      setTrace(tc.map((e) => `[${e.kind}] ${e.symbol} ${e.message}`))
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to load run detail'
      )
    }
  }, [])

  const handleStart = async () => {
    const syms = symbols
      .split(',')
      .map((s) => s.trim().toUpperCase())
      .filter(Boolean)
    if (syms.length === 0) return toast.error('Enter at least one symbol')
    if (syms.length > 20) return toast.error('At most 20 symbols per backtest')
    if (!strategyId) return toast.error('Select a strategy')
    if (!modelId) return toast.error('Select an AI model')
    const start = new Date(startTime)
    const end = new Date(endTime)
    if (isNaN(start.getTime()) || isNaN(end.getTime()) || end <= start) {
      return toast.error('End time must be after start time')
    }
    if (end.getTime() - start.getTime() > 90 * 24 * 3600 * 1000) {
      return toast.error('Backtest span is limited to 90 days')
    }
    setStarting(true)
    try {
      await backtestApi.start({
        name: `backtest-${Date.now()}`,
        symbols: syms,
        timeframe,
        start_time: start.toISOString(),
        end_time: end.toISOString(),
        initial_balance: parseFloat(initialBalance) || 1000,
        strategy_id: strategyId,
        model_id: modelId,
        exchange_id: exchangeId || undefined,
      })
      toast.success('Backtest started')
      refreshRuns()
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to start backtest'
      )
    } finally {
      setStarting(false)
    }
  }

  const handleLifecycle = async (
    action: 'pause' | 'resume' | 'stop' | 'delete',
    run: BacktestRun
  ) => {
    try {
      await backtestApi.lifecycle(action, run.id)
      toast.success(`${action} ok`)
      refreshRuns()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : `Failed to ${action}`)
    }
  }

  return (
    <div
      className="mx-auto max-w-6xl px-4 py-6"
      style={{ background: '#F1ECE2' }}
    >
      <h1 className="mb-4 text-2xl font-bold" style={{ color: '#1A1813' }}>
        Backtest Studio
      </h1>

      {/* Config form */}
      <div className="mb-6 rounded-xl border border-[#E5E1D8] bg-white p-5">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">
              Symbols (comma separated)
            </span>
            <input
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={symbols}
              onChange={(e) => setSymbols(e.target.value)}
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">Timeframe</span>
            <select
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={timeframe}
              onChange={(e) => setTimeframe(e.target.value)}
            >
              {TIMEFRAMES.map((tf) => (
                <option key={tf} value={tf}>
                  {tf}
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">
              Initial Balance (USDT)
            </span>
            <input
              type="number"
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={initialBalance}
              onChange={(e) => setInitialBalance(e.target.value)}
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">Start Time</span>
            <input
              type="datetime-local"
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={startTime}
              onChange={(e) => setStartTime(e.target.value)}
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">End Time</span>
            <input
              type="datetime-local"
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={endTime}
              onChange={(e) => setEndTime(e.target.value)}
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">Strategy</span>
            <select
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={strategyId}
              onChange={(e) => setStrategyId(e.target.value)}
            >
              {strategies.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">AI Model</span>
            <select
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={modelId}
              onChange={(e) => setModelId(e.target.value)}
            >
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name} ({m.provider})
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-[#8b8b8b]">
              Exchange (fee defaults)
            </span>
            <select
              className="w-full rounded-md border border-[#E5E1D8] px-3 py-2 text-sm"
              value={exchangeId}
              onChange={(e) => setExchangeId(e.target.value)}
            >
              <option value="">— default —</option>
              {exchanges.map((ex) => (
                <option key={ex.id} value={ex.id}>
                  {ex.account_name} ({ex.exchange_type})
                </option>
              ))}
            </select>
          </label>
          <div className="flex items-end">
            <button
              className="rounded-md bg-[#1A1813] px-6 py-2 text-sm font-medium text-white disabled:opacity-50"
              onClick={handleStart}
              disabled={starting}
            >
              {starting ? 'Starting…' : 'Start Backtest'}
            </button>
          </div>
        </div>
      </div>

      {/* Runs list */}
      <div className="mb-6 rounded-xl border border-[#E5E1D8] bg-white p-5">
        <h2 className="mb-3 text-lg font-semibold" style={{ color: '#1A1813' }}>
          Runs
        </h2>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#E5E1D8] text-[#8b8b8b]">
                <th className="py-2 pr-3">Name</th>
                <th className="py-2 pr-3">Symbols</th>
                <th className="py-2 pr-3">TF</th>
                <th className="py-2 pr-3">Status</th>
                <th className="py-2 pr-3">Return</th>
                <th className="py-2 pr-3">Trades</th>
                <th className="py-2 pr-3">Created</th>
                <th className="py-2">Actions</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((run) => (
                <tr
                  key={run.id}
                  className="cursor-pointer border-b border-[#F1ECE2] hover:bg-[#FAF8F2]"
                  onClick={() => loadDetail(run)}
                >
                  <td className="py-2 pr-3">{run.name}</td>
                  <td className="py-2 pr-3">
                    {run.symbols.replace(/\[|"|\]/g, '').replace(/,/g, ', ')}
                  </td>
                  <td className="py-2 pr-3">{run.timeframe}</td>
                  <td className="py-2 pr-3">
                    <span
                      className="rounded px-2 py-0.5 text-xs font-medium text-white"
                      style={{
                        background: STATUS_COLORS[run.status] || '#8b8b8b',
                      }}
                    >
                      {run.status}
                    </span>
                  </td>
                  <td className="py-2 pr-3">
                    {run.total_return_pct != null
                      ? `${run.total_return_pct.toFixed(2)}%`
                      : '—'}
                  </td>
                  <td className="py-2 pr-3">{run.trade_count ?? '—'}</td>
                  <td className="py-2 pr-3">
                    {run.created_at
                      ? new Date(run.created_at).toLocaleString()
                      : '—'}
                  </td>
                  <td className="py-2" onClick={(e) => e.stopPropagation()}>
                    {run.status === 'running' || run.status === 'pending' ? (
                      <>
                        <button
                          className="mr-1 text-xs text-[#2563eb]"
                          onClick={() => handleLifecycle('pause', run)}
                        >
                          Pause
                        </button>
                        <button
                          className="mr-1 text-xs text-[#d97706]"
                          onClick={() => handleLifecycle('stop', run)}
                        >
                          Stop
                        </button>
                      </>
                    ) : null}
                    {run.status === 'paused' ? (
                      <button
                        className="mr-1 text-xs text-[#16a34a]"
                        onClick={() => handleLifecycle('resume', run)}
                      >
                        Resume
                      </button>
                    ) : null}
                    <button
                      className="text-xs text-[#dc2626]"
                      onClick={() => handleLifecycle('delete', run)}
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
              {runs.length === 0 ? (
                <tr>
                  <td colSpan={8} className="py-4 text-center text-[#8b8b8b]">
                    No runs yet
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </div>

      {/* Run detail */}
      {selectedRun ? (
        <div className="rounded-xl border border-[#E5E1D8] bg-white p-5">
          <h2
            className="mb-3 text-lg font-semibold"
            style={{ color: '#1A1813' }}
          >
            {selectedRun.name}{' '}
            <span className="text-sm font-normal text-[#8b8b8b]">
              ({selectedRun.id.slice(0, 8)})
            </span>
          </h2>

          {metrics ? (
            <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-4">
              <MetricCard
                label="Final Balance"
                value={`$${metrics.final_balance.toFixed(2)}`}
                hint={`initial $${metrics.initial_balance.toFixed(2)}`}
              />
              <MetricCard
                label="Total Return"
                value={`${metrics.total_return_pct.toFixed(2)}%`}
                hint={metrics.total_return_pct >= 0 ? 'profit' : 'loss'}
              />
              <MetricCard
                label="Max Drawdown"
                value={`${metrics.max_drawdown_pct.toFixed(2)}%`}
              />
              <MetricCard
                label="Sharpe Ratio"
                value={metrics.sharpe_ratio.toFixed(2)}
              />
              <MetricCard
                label="Win Rate"
                value={`${metrics.win_rate.toFixed(1)}%`}
              />
              <MetricCard
                label="Profit Factor"
                value={
                  metrics.profit_factor ? metrics.profit_factor.toFixed(2) : '∞'
                }
              />
              <MetricCard label="Trades" value={String(metrics.trade_count)} />
              <MetricCard label="Timeframe" value={metrics.timeframe} />
            </div>
          ) : (
            <p className="mb-4 text-sm text-[#8b8b8b]">
              {selectedRun.status === 'running' ||
              selectedRun.status === 'pending'
                ? 'Run in progress — results appear when it completes.'
                : 'Loading results…'}
            </p>
          )}

          {equity.length > 0 ? (
            <div className="mb-4 h-64">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart
                  data={equity}
                  margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
                >
                  <XAxis
                    dataKey="time"
                    tick={{ fontSize: 10 }}
                    minTickGap={40}
                  />
                  <YAxis
                    domain={['auto', 'auto']}
                    tick={{ fontSize: 10 }}
                    width={70}
                  />
                  <Tooltip />
                  <Line
                    type="monotone"
                    dataKey="equity"
                    stroke="#1A1813"
                    dot={false}
                    strokeWidth={1.5}
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          ) : null}

          {trades.length > 0 ? (
            <div className="mb-4 overflow-x-auto">
              <h3 className="mb-2 text-sm font-semibold text-[#1A1813]">
                Trades
              </h3>
              <table className="w-full text-left text-xs">
                <thead>
                  <tr className="border-b border-[#E5E1D8] text-[#8b8b8b]">
                    <th className="py-1 pr-3">Time</th>
                    <th className="py-1 pr-3">Symbol</th>
                    <th className="py-1 pr-3">Side</th>
                    <th className="py-1 pr-3">Position</th>
                    <th className="py-1 pr-3">Price</th>
                    <th className="py-1 pr-3">Size</th>
                    <th className="py-1 pr-3">Fee</th>
                    <th className="py-1">PnL</th>
                  </tr>
                </thead>
                <tbody>
                  {trades.map((tr) => (
                    <tr key={tr.id} className="border-b border-[#F1ECE2]">
                      <td className="py-1 pr-3">
                        {new Date(tr.time).toLocaleString()}
                      </td>
                      <td className="py-1 pr-3">{tr.symbol}</td>
                      <td className="py-1 pr-3">{tr.side}</td>
                      <td className="py-1 pr-3">{tr.position}</td>
                      <td className="py-1 pr-3">{tr.price.toFixed(4)}</td>
                      <td className="py-1 pr-3">{tr.size.toFixed(6)}</td>
                      <td className="py-1 pr-3">{tr.fee.toFixed(4)}</td>
                      <td
                        className="py-1"
                        style={{ color: tr.pnl >= 0 ? '#16a34a' : '#dc2626' }}
                      >
                        {tr.pnl.toFixed(4)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}

          {trace.length > 0 ? (
            <div>
              <h3 className="mb-2 text-sm font-semibold text-[#1A1813]">
                Trace
              </h3>
              <pre className="max-h-48 overflow-auto rounded-md bg-[#FAF8F2] p-3 text-xs text-[#555]">
                {trace.join('\n')}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
