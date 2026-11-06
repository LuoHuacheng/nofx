import {
  apiKeyVenues,
  defaultMarket,
  venueName,
} from '../../lib/frontierAccounts'
import { accountLabel } from '../../lib/frontierAccounts'
import { useCallback, useEffect, useRef, useState } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import { AIWalletBackup } from '../common/AIWalletBackup'
import type { AIModel, Exchange } from '../../types'
import { api } from '../../lib/api'
import { planningAPI } from '../../lib/planning'
import type { AccountCheck } from '../../lib/frontierWorkspace'
import { HyperliquidWalletConnect } from '../common/HyperliquidWalletConnect'
import { ExchangeConfigModal } from '../trader/ExchangeConfigModal'
import { ModelConfigModal } from '../trader/ModelConfigModal'

export type FeeWalletState =
  | 'checking'
  | 'missing'
  | 'empty'
  | 'unknown'
  | 'ready'
type Wallet = {
  found: boolean
  enabled: boolean
  address?: string
  model_id?: string
  chain: string
  asset: string
  balance_usdc?: string
  balance_status: string
}
const formatBalance = (value: string | undefined) =>
  Number(value).toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
export function feeWalletState(wallet: Wallet): FeeWalletState {
  if (!wallet.found || !wallet.enabled) return 'missing'
  if (
    wallet.balance_status !== 'ok' ||
    !wallet.balance_usdc?.trim() ||
    !Number.isFinite(Number(wallet.balance_usdc)) ||
    Number(wallet.balance_usdc) < 0
  )
    return 'unknown'
  return Number(wallet.balance_usdc) > 0 ? 'ready' : 'empty'
}
export function FrontierSetup({
  models,
  modelID,
  accounts,
  accountID,
  venue,
  loaded,
  open,
  onOpen,
  onRefresh,
  onWalletState,
  onComplete,
}: {
  models: AIModel[]
  modelID: string
  accounts: Exchange[]
  accountID: string
  venue: string
  loaded: boolean
  open: 'ai' | 'exchange' | null
  onOpen: (step: 'ai' | 'exchange' | null) => void
  onRefresh: (
    modelID?: string,
    venue?: string,
    accountID?: string
  ) => Promise<void>
  onWalletState: (state: FeeWalletState) => void
  onComplete?: (step: 'ai' | 'exchange') => void
}) {
  const [wallet, setWallet] = useState<Wallet | null>(null)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [cex, setCex] = useState(false)
  const [addingAccount, setAddingAccount] = useState(false)
  const [choice, setChoice] = useState(venue)
  const [ownAI, setOwnAI] = useState<AIModel[] | null>(null)
  const [directProviders, setDirectProviders] = useState<AIModel[]>([])
  const [check, setCheck] = useState<AccountCheck | null>(null)
  const dialog = useRef<HTMLElement>(null)
  const mounted = useRef(true),
    request = useRef(0),
    pending = useRef(false),
    accountRequest = useRef(0)
  const selectedModel = models.find((m) => m.id === modelID)
  const status = wallet ? feeWalletState(wallet) : 'checking'
  const aiReady =
    !!selectedModel &&
    (selectedModel.provider !== 'claw402' || status === 'ready')
  const checked = check?.accountID === accountID ? check : null
  const exchangeReady = checked?.status === 'verified'
  const currentAccount = accounts.find((a) => a.id === accountID)
  const refreshWallet = useCallback(async () => {
    if (pending.current) return
    pending.current = true
    const revision = ++request.current
    try {
      const next = await planningAPI<Wallet>(
        'setup-wallet' +
          (selectedModel?.provider === 'claw402'
            ? '?model_id=' + encodeURIComponent(modelID)
            : '')
      )
      if (mounted.current && revision === request.current) {
        setWallet(next)
        onWalletState(feeWalletState(next))
        setError('')
      }
    } catch {
      if (mounted.current && revision === request.current) {
        onWalletState('unknown')
        setWallet(null)
        setError(
          'Could not check your AI balance. Retry the balance check before creating another wallet.'
        )
      }
    } finally {
      if (revision === request.current) pending.current = false
    }
  }, [onWalletState, selectedModel?.provider, modelID])
  const verifyAccount = useCallback(
    async (background = false) => {
      const revision = ++accountRequest.current
      if (!accountID) {
        setCheck(null)
        return
      }
      setCheck((previous) =>
        background &&
        previous?.accountID === accountID &&
        previous.status === 'verified'
          ? previous
          : { accountID, status: 'checking' }
      )
      try {
        const next = await planningAPI<{
          equity: string
          available: string
          position: string
          open_orders: number
        }>(
          `account?exchange_id=${encodeURIComponent(accountID)}&symbol=${defaultMarket(venue)}`
        )
        if (
          !Number.isFinite(Number(next.equity)) ||
          !Number.isFinite(Number(next.available)) ||
          next.equity == null ||
          next.available == null ||
          String(next.equity).trim() === '' ||
          String(next.available).trim() === ''
        )
          throw new Error(
            'The exchange did not return a complete balance. Retry verification.'
          )
        if (mounted.current && revision === accountRequest.current)
          setCheck({
            ...next,
            accountID,
            status: 'verified',
            checkedAt: Date.now(),
          })
      } catch (e) {
        if (mounted.current && revision === accountRequest.current)
          setCheck({
            accountID,
            status: 'unavailable',
            error:
              e instanceof Error ? e.message : 'Could not verify the account.',
          })
      }
    },
    [accountID, venue]
  )
  useEffect(() => {
    mounted.current = true
    setWallet(null)
    setCopied(false)
    onWalletState('checking')
    void refreshWallet()
    return () => {
      mounted.current = false
      request.current++
      pending.current = false
    }
  }, [refreshWallet, onWalletState])
  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async (background = false) => {
      if (!document.hidden) await verifyAccount(background)
      if (!cancelled && accountID)
        timer = setTimeout(() => void poll(true), 30000)
    }
    void poll()
    return () => {
      cancelled = true
      clearTimeout(timer)
      accountRequest.current++
    }
  }, [verifyAccount, accountID])
  useEffect(() => {
    if (open !== 'ai') return
    let cancelled = false
    void (async () => {
      try {
        const supported = await api.getSupportedModels()
        if (!cancelled)
          setDirectProviders(
            supported.filter(
              (model) =>
                model.provider !== 'claw402' &&
                model.id !== 'claw402' &&
                !model.provider?.startsWith('blockrun')
            )
          )
      } catch {
        if (!cancelled) setDirectProviders([])
      }
    })()
    return () => {
      cancelled = true
    }
  }, [open])
  useEffect(() => {
    if (!open || !wallet?.found || status === 'ready') return
    const timer = window.setInterval(() => {
      if (!document.hidden) void refreshWallet()
    }, 15000)
    return () => clearInterval(timer)
  }, [open, wallet?.found, status, refreshWallet])
  useEffect(() => {
    if (open === 'exchange') {
      setChoice(venue)
      void verifyAccount()
    }
  }, [open, venue, verifyAccount])
  useEffect(() => {
    if (!open || cex || ownAI) return
    const previous = document.activeElement as HTMLElement | null
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    dialog.current?.querySelector<HTMLButtonElement>('button')?.focus()
    const keys = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onOpen(null)
      if (event.key !== 'Tab') return
      const items = Array.from(
        dialog.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled)'
        ) || []
      )
      const first = items[0],
        last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last?.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first?.focus()
      }
    }
    document.addEventListener('keydown', keys)
    return () => {
      document.removeEventListener('keydown', keys)
      document.body.style.overflow = overflow
      previous?.focus()
    }
  }, [open, cex, ownAI, onOpen])
  async function createWallet() {
    if (working || pending.current) return
    if (!wallet?.found) {
      setOwnAI([
        { id: 'claw402', name: 'Claw402', provider: 'claw402', enabled: false },
      ])
      return
    }
    setWorking(true)
    setError('')
    try {
      const next = await planningAPI<Wallet>('setup-wallet', {
        model_id:
          wallet?.model_id ||
          (selectedModel?.provider === 'claw402' ? modelID : ''),
      })
      if (!mounted.current) return
      setWallet(next)
      onWalletState(feeWalletState(next))
      await onRefresh(selectedModel?.id || next.model_id)
      await refreshWallet()
    } catch {
      if (mounted.current)
        setError(
          'AI setup could not be confirmed. Check the wallet status before retrying.'
        )
    } finally {
      if (mounted.current) setWorking(false)
    }
  }
  async function savedExchange(id?: string) {
    const latest = await api.getExchangeConfigs()
    const saved =
      id ||
      latest.find(
        (a) =>
          a.exchange_type === choice && !accounts.some((old) => old.id === a.id)
      )?.id
    await onRefresh(undefined, choice, saved)
    setCex(false)
    // Stay here for the live check. Saving credentials alone is not verification.
  }
  function finish(step: 'ai' | 'exchange') {
    onComplete?.(step)
    onOpen(null)
  }
  return (
    <>
      {loaded && (
        <section className="frontier-connections" aria-label="Your connections">
          <button onClick={() => onOpen('ai')}>
            <span>AI & data</span>
            <b>
              {selectedModel?.provider !== 'claw402' && selectedModel
                ? 'Your API provider'
                : status === 'ready'
                  ? `${Number(wallet?.balance_usdc).toFixed(2)} USDC`
                  : status === 'empty'
                    ? 'Add AI funds'
                    : status === 'checking'
                      ? 'Checking balance…'
                      : 'Set up AI'}
            </b>
          </button>
          <button
            className="frontier-account-funds"
            onClick={() => onOpen('exchange')}
          >
            <span>Trading account</span>
            <b>
              {!accountID
                ? 'Connect to trade'
                : exchangeReady
                  ? currentAccount
                    ? accountLabel(currentAccount, accounts)
                    : 'Account'
                  : checked?.status === 'checking'
                    ? 'Loading balance…'
                    : 'Balance unavailable · Check connection'}
            </b>
            {exchangeReady && (
              <>
                <span
                  className="frontier-funds-metric"
                  title="Total account equity, including unrealized profit and loss."
                >
                  Account equity{' '}
                  <b>
                    {formatBalance(checked.equity)}{' '}
                    {venue === 'hyperliquid' ? 'USDC' : 'USDT'}
                  </b>
                </span>
                <span
                  className="frontier-funds-metric"
                  title="Free margin available for new orders, before leverage and execution costs."
                >
                  Available to trade{' '}
                  <b>
                    {formatBalance(checked.available)}{' '}
                    {venue === 'hyperliquid' ? 'USDC' : 'USDT'}
                  </b>
                </span>
              </>
            )}
          </button>
          <small>AI fees and trading funds are separate.</small>
        </section>
      )}
      {open && !cex && !ownAI && (
        <div className="frontier-setup-backdrop">
          <section
            ref={dialog}
            className="frontier-setup-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="frontier-setup-title"
          >
            <header>
              <div>
                <small>
                  {open === 'ai' ? 'ANALYSIS & DATA' : 'YOUR TRADING FUNDS'}
                </small>
                <h2 id="frontier-setup-title">
                  {open === 'ai'
                    ? 'Set up your AI'
                    : currentAccount
                      ? 'Your trading account'
                      : 'Connect a trading account'}
                </h2>
              </div>
              <button aria-label="Close setup" onClick={() => onOpen(null)}>
                ×
              </button>
            </header>
            {error && (
              <p role="alert" className="frontier-setup-error">
                {error}
              </p>
            )}
            {open === 'ai' ? (
              <>
                <p>
                  Generate plans first. You only need a trading account when you
                  want to place an order.
                </p>
                {selectedModel && selectedModel.provider !== 'claw402' && (
                  <div className="frontier-setup-success">
                    <b>Your AI is configured</b>
                    <p>
                      {selectedModel.name} bills through your provider. The
                      separate AI balance below is only needed for paid data
                      such as AI500.
                    </p>
                  </div>
                )}
                <div className="frontier-ai-options">
                  <b>Pay as you go</b>
                  <p>
                    AI requests and premium data are charged to a dedicated USDC
                    wallet. Your trading balance is never used for these fees.
                  </p>
                </div>
                {wallet?.found ? (
                  <div className="frontier-wallet-card">
                    <QRCodeSVG value={wallet.address || ''} size={130} />
                    <div>
                      <b>USDC on Base</b>
                      <p>
                        Send only USDC on the Base network to this AI payment
                        address.
                      </p>
                      <code>{wallet.address}</code>
                      <button
                        onClick={async () => {
                          try {
                            await navigator.clipboard.writeText(
                              wallet.address || ''
                            )
                            setCopied(true)
                          } catch {
                            setError(
                              'Copy failed. Select and copy the address instead.'
                            )
                          }
                        }}
                      >
                        {copied ? 'Address copied' : 'Copy AI payment address'}
                      </button>
                      <p role="status">
                        <strong>
                          {wallet.balance_status === 'ok'
                            ? `${Number(wallet.balance_usdc).toFixed(2)} USDC`
                            : 'Balance not verified'}
                        </strong>{' '}
                        ·{' '}
                        {status === 'ready'
                          ? 'Funds received'
                          : status === 'empty'
                            ? 'Waiting for your deposit · checks every 15 seconds'
                            : 'Verification needed'}
                      </p>
                      <button
                        disabled={working}
                        onClick={() => void refreshWallet()}
                      >
                        Check balance
                      </button>
                      {!wallet.enabled && (
                        <button
                          disabled={working}
                          onClick={() => void createWallet()}
                        >
                          Enable existing AI wallet
                        </button>
                      )}
                    </div>
                  </div>
                ) : (
                  <div className="frontier-setup-start">
                    <p>
                      Create your AI payment wallet, then fund it with Base
                      USDC. Creating it does not charge you.
                    </p>
                    <button
                      className="planning-primary"
                      disabled={working || !!error || status === 'checking'}
                      onClick={() => void createWallet()}
                    >
                      {working ? 'Setting up…' : 'Create AI wallet'}
                    </button>
                    {error && (
                      <button onClick={() => void refreshWallet()}>
                        Check wallet status
                      </button>
                    )}
                  </div>
                )}
                {wallet?.found && wallet.address && wallet.model_id && (
                  <AIWalletBackup
                    key={wallet.model_id + wallet.address}
                    modelID={wallet.model_id}
                    address={wallet.address}
                  />
                )}
                <p className="frontier-setup-note">
                  Pay per request; the gateway checks the price before payment.
                  A positive balance does not guarantee enough funds for every
                  request. This wallet is encrypted on your NOFX server, not in
                  your browser. Use Private key backup above to save your key.
                  Keep a backup of your deployment too.
                </p>
                {directProviders.length > 0 && (
                  <button
                    disabled={working}
                    className="frontier-own-ai"
                    onClick={() => setOwnAI(directProviders)}
                  >
                    Use my own API key instead →
                  </button>
                )}
                <footer>
                  <button onClick={() => onOpen(null)}>Keep exploring</button>
                  <button
                    className="planning-primary"
                    disabled={!aiReady || working}
                    onClick={() => finish('ai')}
                  >
                    Continue with this AI
                  </button>
                </footer>
              </>
            ) : (
              <>
                <p>
                  Choose where your orders will go. Connecting does not place an
                  order or start Autopilot.
                </p>
                <div className="frontier-exchange-choices">
                  <button
                    aria-pressed={choice === 'hyperliquid'}
                    onClick={() => setChoice('hyperliquid')}
                  >
                    <b>Hyperliquid</b>
                    <span>Connect your wallet</span>
                  </button>
                  {apiKeyVenues.map((exchange) => (
                    <button
                      key={exchange}
                      aria-pressed={choice === exchange}
                      onClick={() => setChoice(exchange)}
                    >
                      <b>{venueName(exchange)} Futures</b>
                      <span>
                        API key{exchange !== 'binance' ? ' + passphrase' : ''}
                      </span>
                    </button>
                  ))}
                </div>
                {choice === venue && currentAccount && (
                  <div
                    className={
                      exchangeReady
                        ? 'frontier-setup-success'
                        : 'frontier-account-check'
                    }
                    role="status"
                  >
                    <b>
                      {currentAccount.name} ·{' '}
                      {exchangeReady
                        ? 'Account verified'
                        : checked?.status === 'checking'
                          ? 'Checking the exchange…'
                          : 'Verification incomplete'}
                    </b>
                    {exchangeReady ? (
                      <>
                        <p>
                          Equity {Number(checked.equity).toFixed(2)} · Available
                          margin {Number(checked.available).toFixed(2)}{' '}
                          {venue === 'hyperliquid' ? 'USDC' : 'USDT'}
                        </p>
                        {Number(checked.available) <= 0 && (
                          <p>
                            No free margin right now. You can still generate
                            plans; add trading funds or free margin before
                            placing a new order.
                          </p>
                        )}
                      </>
                    ) : (
                      <p>
                        {checked?.error ||
                          'Checking credentials and reading the live account.'}
                      </p>
                    )}
                    <button
                      onClick={() => void verifyAccount()}
                      disabled={checked?.status === 'checking'}
                    >
                      Verify account
                    </button>
                  </div>
                )}
                {choice === 'hyperliquid' ? (
                  currentAccount?.exchange_type === 'hyperliquid' ? (
                    <details className="frontier-wallet-details">
                      <summary>Wallet permissions & funding</summary>
                      <HyperliquidWalletConnect
                        key={currentAccount.id}
                        account={{
                          id: currentAccount.id,
                          walletAddress:
                            currentAccount.hyperliquidWalletAddr || '',
                        }}
                        language="en"
                        isLoggedIn
                        variant="inline"
                        onSaved={savedExchange}
                      />
                    </details>
                  ) : (
                    <HyperliquidWalletConnect
                      language="en"
                      isLoggedIn
                      variant="inline"
                      onSaved={savedExchange}
                    />
                  )
                ) : (
                  <div className="frontier-setup-start">
                    <b>Connect {venueName(choice)} USDT Futures</b>
                    <ol>
                      <li>Create an API key with Futures trading enabled.</li>
                      <li>
                        Allow this NOFX server IP. Keep withdrawals disabled.
                      </li>
                      <li>
                        Save the key here. We then verify the live account.
                      </li>
                    </ol>
                    <button
                      className="planning-primary"
                      onClick={() => {
                        setAddingAccount(false)
                        setCex(true)
                      }}
                    >
                      {currentAccount?.exchange_type === choice
                        ? `Update ${venueName(choice)} connection`
                        : `Connect ${venueName(choice)}`}
                    </button>
                    {currentAccount?.exchange_type === choice && (
                      <button
                        onClick={() => {
                          setAddingAccount(true)
                          setCex(true)
                        }}
                      >
                        Add another {venueName(choice)} account
                      </button>
                    )}
                  </div>
                )}
                <p className="frontier-setup-note">
                  Trading permissions, balances and order details are checked
                  again before execution. Setup does not approve a trade.
                </p>
                <footer>
                  <button onClick={() => onOpen(null)}>
                    {currentAccount ? 'Close' : 'Plan without an account'}
                  </button>
                  <button
                    className="planning-primary"
                    disabled={!exchangeReady || choice !== venue}
                    onClick={() => finish('exchange')}
                  >
                    Return to my plan
                  </button>
                </footer>
              </>
            )}
          </section>
        </div>
      )}
      {cex && (
        <ExchangeConfigModal
          allExchanges={accounts}
          editingExchangeId={
            !addingAccount && currentAccount?.exchange_type === choice
              ? currentAccount.id
              : null
          }
          initialExchangeType={choice}
          allowedExchangeTypes={[choice]}
          language="en"
          onClose={() => setCex(false)}
          onDelete={() => {}}
          onSave={async (id, type, name, key, secret, passphrase, testnet) => {
            if (!apiKeyVenues.includes(type) || type !== choice || testnet)
              throw new Error(
                'Frontier requires a supported mainnet USDT futures account'
              )
            if (id) {
              await api.updateExchangeConfigsEncrypted({
                exchanges: {
                  [id]: {
                    enabled: true,
                    api_key: key,
                    secret_key: secret || '',
                    passphrase: passphrase || '',
                    testnet: false,
                  },
                },
              })
              await savedExchange(id)
            } else {
              const result = await api.createExchangeEncrypted({
                exchange_type: type,
                account_name: name,
                enabled: true,
                api_key: key,
                secret_key: secret || '',
                passphrase: passphrase || '',
                testnet: false,
              })
              await savedExchange(result.id)
            }
          }}
        />
      )}
      {ownAI && (
        <ModelConfigModal
          allModels={ownAI}
          configuredModels={models}
          editingModelId={null}
          language="en"
          onClose={() => setOwnAI(null)}
          onDelete={() => {}}
          onSave={async (id, key, url, name) => {
            try {
              await api.updateModelConfigs({
                models: {
                  [id]: {
                    enabled: true,
                    api_key: key,
                    custom_api_url: url || '',
                    custom_model_name: name || '',
                  },
                },
              })
              const configured = await api.getModelConfigs()
              const match =
                configured.find((m) => m.id === id) ||
                configured.find((m) => m.provider === id && m.enabled)
              await onRefresh(match?.id)
              await refreshWallet()
              setOwnAI(null)
              setError('')
            } catch {
              throw new Error('Could not save the AI provider.')
            }
          }}
        />
      )}
    </>
  )
}
