import { apiKeyVenues, venueName } from '../../lib/frontierAccounts'
import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../../lib/api'
import { getAuthHeaders, handleJSONResponse } from '../../lib/api/helpers'
import {
  planningAPI,
  CloseNotSubmittedError,
  type LiveRun,
  type PlanningState,
} from '../../lib/planning'
import {
  accountPositions,
  closeAccountPosition,
  positionDecimal,
  nativeMarket,
  type AccountPosition,
} from '../../lib/frontierAccount'
import { accountLabel } from '../../lib/frontierAccounts'
import type { Exchange } from '../../types'
import type { AccountObservation } from '../../lib/frontierWorkspace'

interface OpenOrder {
  order_id: string
  symbol: string
  side: string
  type: string
  price: number
  stop_price: number
  quantity: number
  status: string
}
const format = (value: number | string) =>
  Number(value).toLocaleString('en-US', { maximumFractionDigits: 8 })
const amount = (value: number) =>
  Number.isFinite(value)
    ? value.toLocaleString('en-US', {
        minimumFractionDigits: 2,
        maximumFractionDigits:
          Math.abs(value) > 0 && Math.abs(value) < 0.01 ? 4 : 2,
      })
    : 'Unavailable'
const exchangeURL = (venue: string) =>
  venue === 'hyperliquid'
    ? 'https://app.hyperliquid.xyz/'
    : venue === 'okx'
      ? 'https://www.okx.com/trade-swap/btc-usdt-swap'
      : venue === 'bitget'
        ? 'https://www.bitget.com/futures/usdt/BTCUSDT'
        : 'https://www.binance.com/en/futures'
const liveStates = new Set([
  'starting',
  'running',
  'position_open',
  'stopping',
  'needs_attention',
  'approved',
])

export function FrontierAccount({
  account,
  accounts,
  venue,
  symbol,
  runs,
  busy,
  onView,
  onManual,
  onBusy,
  onRefresh,
  onCheck,
  onObserve,
}: {
  account?: Exchange
  accounts: Exchange[]
  venue: string
  symbol: string
  runs: LiveRun[]
  busy: boolean
  onView: (run: LiveRun) => void
  onManual: (side: 'long' | 'short') => void
  onBusy: (value: boolean) => void
  onRefresh: () => Promise<void>
  onCheck?: (run: LiveRun) => Promise<void>
  onObserve?: (snapshot: AccountObservation | null) => void
}) {
  const [snapshot, setSnapshot] = useState<{
    accountID: string
    positions: AccountPosition[]
    at: string
  } | null>(null)
  const [orders, setOrders] = useState<{
    accountID: string
    symbol: string
    rows: OpenOrder[]
  } | null>(null)
  const [loading, setLoading] = useState(false),
    [error, setError] = useState(''),
    [orderError, setOrderError] = useState('')
  const [binding, setBinding] = useState<{
    id: string
    accountID: string
    running: boolean
  } | null>(null)
  const [confirm, setConfirm] = useState<AccountPosition | null>(null),
    [notice, setNotice] = useState('')
  const [attempted, setAttempted] = useState<Set<string>>(new Set())
  const attemptLatch = useRef(new Set<string>())
  const generation = useRef(0),
    inFlight = useRef(false)
  const accountID = account?.id || ''
  const refresh = useCallback(async () => {
    if (!accountID || inFlight.current) return
    const version = generation.current
    inFlight.current = true
    setLoading(true)
    try {
      const traders = (await api.getTraders(true)).filter(
        (t) => t.exchange_id === accountID
      )
      const traderID = traders[0]?.trader_id
      if (version !== generation.current) return
      setBinding(
        traderID
          ? {
              id: traderID,
              accountID,
              running: traders.some((t) => t.is_running),
            }
          : null
      )
      const values = accountPositions(
        await api.getPositions(undefined, true, accountID)
      )
      if (version !== generation.current) return
      setSnapshot({
        accountID,
        positions: values,
        at: new Date().toISOString(),
      })
      for (const attempt of attemptLatch.current) {
        if (
          attempt.startsWith(accountID + '/') &&
          !values.some(
            (p) => attempt === accountID + '/' + p.symbol + '/' + p.side
          )
        )
          attemptLatch.current.delete(attempt)
      }
      setAttempted(new Set(attemptLatch.current))
      setError('')
      const orderRequest = new AbortController()
      const orderDeadline = setTimeout(() => orderRequest.abort(), 30000)
      try {
        const query = new URLSearchParams({ exchange_id: accountID, symbol })
        const response = await fetch('/api/open-orders?' + query, {
          headers: getAuthHeaders(),
          signal: orderRequest.signal,
        })
        const rows = await handleJSONResponse<OpenOrder[]>(response)
        if (
          !Array.isArray(rows) ||
          rows.some(
            (o) =>
              !o ||
              typeof o.order_id !== 'string' ||
              !o.order_id ||
              typeof o.type !== 'string' ||
              !Number.isFinite(Number(o.quantity))
          )
        )
          throw new Error('Incomplete order response')
        if (version !== generation.current) return
        setOrders({ accountID, symbol, rows })
        setOrderError('')
      } catch {
        if (version === generation.current) {
          setOrders(null)
          setOrderError('Current exchange orders could not be verified.')
        }
      } finally {
        clearTimeout(orderDeadline)
      }
    } catch (e) {
      if (version === generation.current)
        setError(e instanceof Error ? e.message : String(e))
    } finally {
      if (version === generation.current) {
        setLoading(false)
        inFlight.current = false
      }
    }
  }, [accountID, symbol])
  useEffect(() => {
    generation.current++
    inFlight.current = false
    setSnapshot((old) => (old?.accountID === accountID ? old : null))
    setOrders(null)
    setError('')
    setBinding((old) => (old?.accountID === accountID ? old : null))
    setConfirm(null)
    setNotice('')
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      if (!document.hidden) await refresh()
      if (!cancelled) timer = setTimeout(poll, 10000)
    }
    void poll()
    return () => {
      cancelled = true
      clearTimeout(timer)
      generation.current++
    }
  }, [refresh])

  const positions = snapshot?.accountID === accountID ? snapshot.positions : []
  const currentBinding = binding?.accountID === accountID ? binding : null
  const hasPosition = positions.some(
    (p) => nativeMarket(p.symbol, venue) === nativeMarket(symbol, venue)
  )
  const open =
    orders?.accountID === accountID && orders.symbol === symbol
      ? orders.rows
      : []
  const importantRuns = runs
    .filter((run) => run.exchange_id === accountID && liveStates.has(run.state))
    .sort(
      (a, b) =>
        Number(b.state === 'needs_attention') -
        Number(a.state === 'needs_attention')
    )
  const key = (p: AccountPosition) => accountID + '/' + p.symbol + '/' + p.side
  useEffect(() => {
    onObserve?.(
      !error && snapshot?.accountID === accountID
        ? {
            accountID,
            symbol,
            positions: snapshot.positions,
            orders:
              !orderError &&
              orders?.accountID === accountID &&
              orders.symbol === symbol
                ? orders.rows
                : null,
            checkedAt: Date.parse(snapshot.at),
          }
        : null
    )
  }, [accountID, symbol, snapshot, orders, error, orderError, onObserve])

  async function closePosition() {
    if (
      !confirm ||
      !accountID ||
      (!apiKeyVenues.includes(venue) && !currentBinding) ||
      busy
    )
      return
    const position = confirm,
      traderID = currentBinding?.id,
      attempt = key(position)
    if (attemptLatch.current.has(attempt)) return
    attemptLatch.current.add(attempt)
    setAttempted(new Set(attemptLatch.current))
    setConfirm(null)
    onBusy(true)
    setNotice('Stopping remaining entries before closing the position…')
    let closeSent = false
    let closeMessage = ''
    try {
      const traders = await api.getTraders(true)
      if (
        !apiKeyVenues.includes(venue) &&
        !traders.some(
          (t) => t.trader_id === traderID && t.exchange_id === accountID
        )
      )
        throw new Error(
          'The NOFX account binding changed. Refresh before closing.'
        )
      if (traders.some((t) => t.exchange_id === accountID && t.is_running))
        throw new Error(
          'Stop Autopilot for this account in Dashboard before manually closing.'
        )
      await closeAccountPosition(accountID, venue, position, runs, {
        stop: (run) =>
          planningAPI('stop', { id: run.id, hash: run.approval_hash }),
        read: async () => (await planningAPI<PlanningState>('state')).runs,
        wait: () => new Promise((resolve) => setTimeout(resolve, 500)),
        close: async () => {
          const fresh = accountPositions(
            await api.getPositions(undefined, true, accountID)
          ).find(
            (p) => p.symbol === position.symbol && p.side === position.side
          )
          if (!fresh)
            throw new Error('Position is no longer open; no close was sent.')
          if (
            fresh.quantity > position.quantity ||
            fresh.entry !== position.entry
          )
            throw new Error(
              'Position changed. Refresh and review its size before closing.'
            )
          if (apiKeyVenues.includes(venue)) {
            const quantity = positionDecimal(fresh.quantity)
            const entryPrice = positionDecimal(fresh.entry)
            closeSent = true
            const result = await planningAPI<{ message: string }>(
              'close-position',
              {
                exchange_id: accountID,
                request_id: crypto.randomUUID(),
                symbol: position.symbol,
                side: position.side,
                quantity,
                entry_price: entryPrice,
              }
            )
            closeMessage = result.message
            return result
          }
          closeSent = true
          return api.closePosition(
            traderID!,
            position.symbol,
            position.side.toUpperCase()
          )
        },
      })
      setNotice(
        closeMessage ||
          'Close request accepted. Verify the refreshed position below; no automatic repeat will be sent.'
      )
    } catch (e) {
      const notSubmitted = !closeSent || e instanceof CloseNotSubmittedError
      if (notSubmitted) {
        attemptLatch.current.delete(attempt)
        setAttempted(new Set(attemptLatch.current))
      }
      setNotice(
        (e instanceof Error ? e.message : String(e)) +
          (notSubmitted
            ? ' No close order was submitted. Refresh the account, then review the position again.'
            : ' Check the exchange before sending another close.')
      )
    } finally {
      onBusy(false)
      await Promise.allSettled([refresh(), onRefresh()])
    }
  }

  return (
    <section
      className="frontier-account"
      aria-label="Account positions and orders"
    >
      <header>
        <div>
          <h2>
            Positions & orders{' '}
            <span className="frontier-count">
              {snapshot?.accountID === accountID ? positions.length : '—'}
            </span>
          </h2>
          <p>
            {account
              ? `${accountLabel(account, accounts)} · ${venueName(venue)}${account.hyperliquidWalletAddr ? ' · ' + account.hyperliquidWalletAddr.slice(0, 8) + '…' + account.hyperliquidWalletAddr.slice(-4) : ''}`
              : 'Select a trading account to see all its positions.'}
          </p>
        </div>
        <div className="frontier-account-actions">
          {accountID && (
            <a href={exchangeURL(venue)} target="_blank" rel="noreferrer">
              Open exchange ↗
            </a>
          )}
          <button
            disabled={!accountID || busy || loading}
            onClick={() => void refresh()}
          >
            {loading ? 'Checking…' : 'Refresh account'}
          </button>
          <button
            className="frontier-long"
            disabled={
              !accountID || busy || !!currentBinding?.running || hasPosition
            }
            onClick={() => onManual('long')}
          >
            Buy / Long
          </button>
          <button
            className="frontier-short"
            disabled={
              !accountID || busy || !!currentBinding?.running || hasPosition
            }
            onClick={() => onManual('short')}
          >
            Sell / Short
          </button>
        </div>
      </header>
      {currentBinding?.running && (
        <p className="frontier-notice">
          Autopilot is running. Stop it in Dashboard before manual trading.
        </p>
      )}
      {hasPosition && (
        <small>
          You have a position in {symbol}. You can still generate new drafts.
          Independent entries cannot execute alongside this position; drafts do
          not modify existing positions or exit orders.
        </small>
      )}
      {hasPosition &&
        !error &&
        !orderError &&
        orders?.accountID === accountID &&
        orders.symbol === symbol &&
        open.length === 0 && (
          <p role="alert" className="frontier-notice">
            {symbol} position is open, but no working orders were reported.
            Stop-loss and take-profit protection is not confirmed.{' '}
            <a href={exchangeURL(venue)} target="_blank" rel="noreferrer">
              Check protection at the exchange ↗
            </a>
          </p>
        )}
      {notice && (
        <p role="status" className="frontier-notice">
          {notice}
        </p>
      )}
      {error && (
        <p role="alert" className="frontier-notice">
          Positions unavailable: {error}
          {snapshot
            ? ' Last received values below may be stale.'
            : ' No empty-account assumption was made.'}
        </p>
      )}
      {accountID && (
        <>
          <div className="frontier-account-subhead">
            <span>All markets · selected account</span>
            <span>
              {snapshot?.accountID === accountID
                ? `${error ? 'Last received' : 'Received'} ${new Date(snapshot.at).toLocaleTimeString()}`
                : 'Waiting for exchange response'}
            </span>
          </div>
          {positions.length > 0 ? (
            <div className="frontier-table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>Market</th>
                    <th>Position value</th>
                    <th title="Position value divided by leverage. This estimate is not the exchange-reported margin balance.">
                      Est. initial margin
                    </th>
                    <th>Quantity</th>
                    <th>Average entry</th>
                    <th>Mark price</th>
                    <th>Unrealized PnL</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {positions.map((p) => (
                    <tr key={p.symbol + p.side}>
                      <td>
                        <strong>{nativeMarket(p.symbol, venue)}</strong>
                        <span className={'frontier-position-badge ' + p.side}>
                          {p.side === 'long' ? 'Long' : 'Short'}{' '}
                          <b>{p.leverage}x</b>
                        </span>
                      </td>
                      <td className="frontier-position-value">
                        <strong>{amount(p.quantity * p.mark)}</strong>
                        <small>
                          {venue === 'hyperliquid' ? 'USDC' : 'USDT'} notional
                        </small>
                      </td>
                      <td
                        className="frontier-position-margin"
                        title="Current notional / leverage. Cross margin, added collateral and exchange rules can change actual margin requirements."
                      >
                        <strong>
                          ≈ {amount((p.quantity * p.mark) / p.leverage)}
                        </strong>
                        <small>
                          {venue === 'hyperliquid' ? 'USDC' : 'USDT'} ·
                          estimated
                        </small>
                      </td>
                      <td>
                        {format(p.quantity)}{' '}
                        <small>
                          {nativeMarket(p.symbol, venue).replace(/USDT$/, '')}
                        </small>
                      </td>
                      <td>{format(p.entry)}</td>
                      <td>{format(p.mark)}</td>
                      <td className={p.pnl >= 0 ? 'target-text' : 'stop-text'}>
                        {p.pnl >= 0 ? '+' : ''}
                        {amount(p.pnl)}{' '}
                        {venue === 'hyperliquid' ? 'USDC' : 'USDT'}
                      </td>
                      <td>
                        {!currentBinding && !apiKeyVenues.includes(venue) ? (
                          <a
                            href={exchangeURL(venue)}
                            target="_blank"
                            rel="noreferrer"
                          >
                            Close on exchange ↗
                          </a>
                        ) : (
                          <button
                            disabled={
                              busy ||
                              !!error ||
                              !!currentBinding?.running ||
                              attempted.has(key(p))
                            }
                            onClick={() => setConfirm(p)}
                          >
                            {attempted.has(key(p))
                              ? 'Verify close at exchange'
                              : 'Close position'}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : snapshot?.accountID === accountID && !error ? (
            <p className="frontier-flat">
              No open positions reported by this account.
            </p>
          ) : loading ? (
            <p>Reading positions…</p>
          ) : null}
          {positions.length > 0 && (
            <p className="frontier-margin-note">
              Position value = quantity × mark price. Estimated initial margin =
              position value ÷ leverage; actual exchange margin may differ.
            </p>
          )}
          {open.filter((o) => /STOP|TAKE_PROFIT/.test(o.type)).length > 0 && (
            <div className="frontier-protection">
              <strong>Exchange exit orders · {symbol}</strong>
              {open
                .filter((o) => /STOP|TAKE_PROFIT/.test(o.type))
                .map((o) => (
                  <span key={o.order_id}>
                    {o.type.includes('TAKE_PROFIT')
                      ? 'Take profit'
                      : 'Stop loss'}{' '}
                    <b>{format(o.stop_price || o.price)}</b> · size{' '}
                    {format(o.quantity)}
                  </span>
                ))}
            </div>
          )}
          <details className="frontier-native-orders" open>
            <summary>
              Exchange orders · {symbol}
              {orders?.accountID === accountID && orders.symbol === symbol
                ? ` · ${open.length}`
                : ' · not verified'}
            </summary>
            {orderError ? (
              <p>{orderError}</p>
            ) : orders?.accountID === accountID && orders.symbol === symbol ? (
              open.length ? (
                <div className="frontier-table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>Type</th>
                        <th>Side</th>
                        <th>Price / trigger</th>
                        <th>Size</th>
                        <th>Status</th>
                        <th>Exchange order ID</th>
                      </tr>
                    </thead>
                    <tbody>
                      {open.map((o) => (
                        <tr key={o.order_id}>
                          <td>{o.type}</td>
                          <td>{o.side}</td>
                          <td>{format(o.stop_price || o.price)}</td>
                          <td>{format(o.quantity)}</td>
                          <td>{o.status}</td>
                          <td>{o.order_id}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p>No open orders reported for {symbol}.</p>
              )
            ) : (
              <p>Orders have not been verified.</p>
            )}
          </details>
        </>
      )}
      {importantRuns.map((run) => {
        const entries = run.orders.filter((o) => o.role === 'entry')
        const filled = entries.reduce((sum, o) => sum + Number(o.filled), 0)
        const accepted = entries.some(
          (o) => o.order_id && ['NEW', 'PARTIALLY_FILLED'].includes(o.status)
        )
        const unconfirmed = entries.some((o) =>
          ['UNKNOWN', 'submitting'].includes(o.status)
        )
        const name =
          accounts.find((a) => a.id === run.exchange_id)?.name ||
          'Account not loaded'
        const accountVerified =
          snapshot?.accountID === run.exchange_id && !error
        const nativePosition = accountVerified
          ? positions.find(
              (p) =>
                nativeMarket(p.symbol, run.plan.snapshot.venue) ===
                  nativeMarket(
                    run.plan.snapshot.symbol,
                    run.plan.snapshot.venue
                  ) && p.side === run.plan.idea.side
            )
          : undefined
        const label = unconfirmed
          ? 'Submission unconfirmed — do not resend'
          : filled > 0
            ? nativePosition
              ? 'Position open · entry filled'
              : accountVerified
                ? 'Entry filled earlier · no position in latest account snapshot'
                : 'Entry filled · current position not verified'
            : accepted
              ? 'Entry order is working'
              : run.state === 'approved' && !entries.length
                ? 'Approved only — no order submitted'
                : 'No confirmed entry fill'
        return (
          <details
            className={
              'frontier-trade-summary' +
              (run.state === 'needs_attention' || unconfirmed
                ? ' attention'
                : '')
            }
            key={run.id}
          >
            <summary className="frontier-record-toggle">
              <span>
                {run.plan.snapshot.symbol} ·{' '}
                {run.plan.idea.side === 'long' ? 'Long' : 'Short'}{' '}
                <span className="frontier-record-label">
                  Execution record · {name}
                </span>
              </span>
              <span
                className={
                  run.state === 'needs_attention' || unconfirmed
                    ? 'frontier-review-badge'
                    : ''
                }
              >
                {unconfirmed
                  ? 'Submission unconfirmed'
                  : run.state === 'needs_attention'
                    ? 'Monitor needs review'
                    : label}
              </span>
            </summary>
            <div>
              <strong>
                {run.plan.snapshot.symbol} ·{' '}
                {run.plan.idea.side === 'long' ? 'Long' : 'Short'}
              </strong>
              <p>
                {name} · {run.plan.snapshot.venue} · {label}
                {filled > 0
                  ? ` · ${format(filled)} ${run.plan.snapshot.base_asset}`
                  : ''}
              </p>
              {run.state === 'needs_attention' && (
                <p className="frontier-monitor-alert">
                  {run.error?.startsWith('Service restarted')
                    ? 'The server restarted, so this order monitor is paused. Check the live position and exit orders above.'
                    : run.error ||
                      'Order monitoring needs review. Check the live position and exit orders above.'}
                </p>
              )}
            </div>
            <div>
              {onCheck &&
                ['needs_attention', 'position_open', 'stopping'].includes(
                  run.state
                ) && (
                  <button disabled={busy} onClick={() => void onCheck(run)}>
                    Check trade status
                  </button>
                )}
              <button disabled={busy} onClick={() => onView(run)}>
                View trade on chart
              </button>
              <a
                href={exchangeURL(run.plan.snapshot.venue)}
                target="_blank"
                rel="noreferrer"
              >
                Open exchange ↗
              </a>
            </div>
            <details>
              <summary>
                Recorded orders
                {run.updated_at
                  ? ' · ' + new Date(run.updated_at).toLocaleTimeString()
                  : ''}
              </summary>
              <table>
                <thead>
                  <tr>
                    <th>Order</th>
                    <th>Price</th>
                    <th>Filled / size</th>
                    <th>Last recorded state</th>
                    <th>Exchange ID</th>
                  </tr>
                </thead>
                <tbody>
                  {run.orders.map((o) => (
                    <tr key={o.client_id}>
                      <td>
                        {o.role === 'entry'
                          ? 'Entry'
                          : o.role === 'sl'
                            ? 'Stop loss'
                            : 'Take profit'}
                      </td>
                      <td>{format(o.average || o.price)}</td>
                      <td>
                        {format(o.filled)} / {format(o.quantity)}
                      </td>
                      <td>{o.status}</td>
                      <td>{o.order_id || 'Not confirmed'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <small>
                Order records are not a substitute for a successful live
                exchange read.
              </small>
            </details>
          </details>
        )
      })}
      {!importantRuns.length && <small>No active execution.</small>}
      {confirm && (
        <div className="planning-modal-backdrop">
          <section
            className="planning-modal"
            role="dialog"
            aria-modal="true"
            aria-label="Confirm position close"
          >
            <h2>
              Close {nativeMarket(confirm.symbol, venue)} {confirm.side}?
            </h2>
            <p>
              {account ? accountLabel(account, accounts) : ''} · {venue} · full
              position {format(confirm.quantity)}
            </p>
            <p>
              This sends a market close through NOFX. Remaining Frontier entry
              orders for this market are stopped first. Slippage and fees apply.
              No typed phrase is needed.
            </p>
            <button onClick={() => setConfirm(null)}>Keep position</button>
            <button
              className="planning-primary"
              disabled={busy}
              onClick={() => void closePosition()}
            >
              Confirm market close
            </button>
          </section>
        </div>
      )}
    </section>
  )
}
