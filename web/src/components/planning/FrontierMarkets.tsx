import { useEffect, useState } from 'react'
import { planningAPI } from '../../lib/planning'
import { PlanningAI500 } from './PlanningAI500'

type Market = {
  symbol: string
  base: string
  category: string
  volume_24h: number
  price: string
  execution_supported: boolean
}
type Board = {
  venue: string
  markets: Market[]
  fetched_at: string
  sources: string[]
  warnings?: string[]
}
const volume = (value: number) =>
  new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
export function FrontierMarkets({
  venue,
  selected,
  disabled,
  walletReady,
  walletChecking,
  onSetup,
  onSelect,
  onAI500Markets,
}: {
  venue: string
  selected: string
  disabled: boolean
  walletReady: boolean
  walletChecking: boolean
  onSetup: () => void
  onSelect: (symbol: string) => void
  onAI500Markets?: (symbols: string[]) => void
}) {
  const [mode, setMode] = useState(venue === 'binance' ? 'ai500' : 'volume')
  const [board, setBoard] = useState<Board | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [limit, setLimit] = useState(12)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    setMode(venue === 'binance' ? 'ai500' : 'volume')
    setSearch('')
    setLimit(12)
  }, [venue])
  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    planningAPI<Board>('markets?venue=' + encodeURIComponent(venue))
      .then((next) => {
        if (
          next.venue !== venue ||
          !Array.isArray(next.markets) ||
          next.markets.some(
            (m) =>
              typeof m.symbol !== 'string' ||
              !Number.isFinite(m.volume_24h) ||
              m.volume_24h < 0
          )
        )
          throw new Error('Invalid public market list')
        if (active) setBoard(next)
      })
      .catch(() => {
        if (active)
          setError(
            'Could not load public markets. Retry or enter a native symbol above.'
          )
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [venue, revision])
  const current = board?.venue === venue ? board : null
  const rows = (current?.markets || [])
    .filter(
      (m) =>
        (mode !== 'stocks' ||
          m.category === 'stocks' ||
          m.category === 'international_stocks') &&
        (m.symbol + ' ' + m.base).toLowerCase().includes(search.toLowerCase())
    )
    .slice()
    .sort(
      (a, b) => b.volume_24h - a.volume_24h || a.symbol.localeCompare(b.symbol)
    )
  const selectedMarket = current?.markets.find((m) => m.symbol === selected)
  return (
    <section className="frontier-markets" aria-label="Quick market selection">
      <div className="frontier-market-filters">
        <b>Markets</b>
        {[
          [
            ...(venue === 'binance' ? [['ai500', 'AI500']] : []),
            ['volume', 'Top volume'],
            ['stocks', 'Stocks'],
            ['all', 'All markets'],
          ],
        ]
          .flat()
          .map(([id, label]) => (
            <button
              key={id}
              aria-pressed={mode === id}
              onClick={() => {
                setMode(id)
                setSearch('')
                setLimit(id === 'all' ? 50 : 12)
              }}
            >
              {label}
            </button>
          ))}
        {mode !== 'ai500' && (
          <>
            <input
              aria-label="Search exchange markets"
              placeholder="Search a symbol, e.g. TSLA"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value)
                setLimit(50)
              }}
            />
            <button
              disabled={loading}
              onClick={() => setRevision((v) => v + 1)}
            >
              Refresh markets
            </button>
            <small>Public data · 24h notional volume · cached 1 min</small>
          </>
        )}
      </div>
      {mode === 'ai500' ? (
        <PlanningAI500
          venue={venue}
          selected={selected}
          disabled={disabled}
          walletReady={walletReady}
          walletChecking={walletChecking}
          onSetup={onSetup}
          onSelect={onSelect}
          onMarkets={onAI500Markets}
        />
      ) : (
        <>
          {loading && <small role="status">Loading exchange markets…</small>}
          {error && <p role="alert">{error}</p>}
          <div className="frontier-market-shortcuts">
            {rows.slice(0, limit).map((m) => (
              <button
                key={m.symbol}
                aria-pressed={selected === m.symbol}
                disabled={disabled}
                onClick={() => onSelect(m.symbol)}
                title={`${m.symbol} · 24h notional ${volume(m.volume_24h)} · ${m.execution_supported ? 'Execution requires account review' : 'Chart only; this deployment has no verified execution adapter for this market'}`}
              >
                <b>{m.symbol}</b>
                <small>
                  {volume(m.volume_24h)}{' '}
                  {venue === 'hyperliquid' ? 'USD' : 'USDT'}
                </small>
                {!m.execution_supported && <small>Chart only</small>}
              </button>
            ))}
          </div>
          {rows.length > limit && (
            <button
              className="frontier-show-markets"
              onClick={() => setLimit((n) => n + 50)}
            >
              Show more · {rows.length - limit} remaining
            </button>
          )}
          {!loading && !error && current && !rows.length && (
            <small>
              No matching {mode === 'stocks' ? 'stock contracts' : 'markets'}{' '}
              returned by this venue.
            </small>
          )}
          {current && (
            <small>
              As of {new Date(current.fetched_at).toLocaleTimeString('en-US')} ·{' '}
              {rows.length} markets
              {mode === 'stocks'
                ? ' · Includes US and international stock contracts'
                : ''}
            </small>
          )}
          {selectedMarket && !selectedMarket.execution_supported && (
            <small>
              Chart only · This builder market does not have a verified NOFX
              execution adapter.
            </small>
          )}
          {!!current?.warnings?.length && (
            <small role="status">{current.warnings.join(' · ')}</small>
          )}
        </>
      )}
    </section>
  )
}
