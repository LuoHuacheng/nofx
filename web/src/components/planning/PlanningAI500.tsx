import { useEffect, useState } from 'react'
import { planningAPI } from '../../lib/planning'
import { formatRequestError } from '../../lib/requestErrors'

type Board = {
  coins: { pair: string; score: number }[]
  source: string
  fetched_at: string
}
export function PlanningAI500({
  venue,
  onSelect,
  disabled,
  selected,
  walletReady = true,
  walletChecking = false,
  onSetup,
  onMarkets,
}: {
  venue: string
  onSelect: (symbol: string) => void
  disabled: boolean
  selected?: string
  walletReady?: boolean
  walletChecking?: boolean
  onSetup?: () => void
  onMarkets?: (symbols: string[]) => void
}) {
  const [board, setBoard] = useState<Board | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    if (venue !== 'binance' || !walletReady) return
    let current = true
    setLoading(true)
    setError('')
    planningAPI<Board>('ai500')
      .then((v) => {
        if (
          !Array.isArray(v.coins) ||
          v.coins.some(
            (c) => typeof c.pair !== 'string' || !Number.isFinite(c.score)
          )
        )
          throw new Error('Invalid AI500 response')
        if (current) {
          setBoard(v)
          onMarkets?.(v.coins.map((coin) => coin.pair))
        }
      })
      .catch((e) => {
        if (current) setError(formatRequestError(e, 'en'))
      })
      .finally(() => {
        if (current) setLoading(false)
      })
    return () => {
      current = false
    }
  }, [venue, revision, walletReady, onMarkets])
  if (venue !== 'binance') return null
  const coins =
    board?.coins.filter((v) =>
      v.pair.toLowerCase().includes(search.toLowerCase())
    ) || []
  return (
    <section className="planning-ai500" aria-label="Binance AI500 markets">
      <div>
        <b>AI500</b>
        <small>Claw402 paid data · cached 5 min</small>
        <small>Long entries · market or pullback opportunities</small>
        <input
          aria-label="Search AI500"
          placeholder="Search AI500 markets"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <button
          disabled={loading || disabled || !walletReady}
          onClick={() => setRevision((v) => v + 1)}
        >
          Refresh list
        </button>
      </div>
      {!walletReady && (
        <div className="planning-ai500-setup">
          <span>
            {walletChecking
              ? 'Checking AI wallet…'
              : 'Enable your AI fee wallet to load AI500 shortcuts.'}
          </span>
          {!walletChecking && onSetup && (
            <button onClick={onSetup}>Set up AI wallet</button>
          )}
        </div>
      )}
      {loading && <p role="status">Loading AI500…</p>}
      {error && <p role="alert">{error}</p>}
      {walletReady && !loading && !error && board && (
        <>
          <div className="planning-ai500-symbols">
            {coins.map((v) => (
              <button
                disabled={disabled}
                key={v.pair}
                aria-pressed={selected === v.pair}
                onClick={() => onSelect(v.pair)}
                title={`AI score ${v.score}`}
              >
                {v.pair.replace(/USDT$/, '')}{' '}
                <small>{Number(v.score).toFixed(0)}</small>
              </button>
            ))}
          </div>
          {!coins.length && <small>No matching AI500 markets.</small>}
          <small>
            As of {new Date(board.fetched_at).toLocaleTimeString('en-US')} ·{' '}
            {board.coins.length} markets
          </small>
        </>
      )}
    </section>
  )
}
