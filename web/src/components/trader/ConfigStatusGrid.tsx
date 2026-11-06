import {
  Brain,
  Landmark,
  Eye,
  EyeOff,
  Copy,
  Check,
  Plus,
  ArrowUpRight,
} from 'lucide-react'
import type { AIModel, Exchange, ExchangeAccountState } from '../../types'
import type { Language } from '../../i18n/translations'
import { t } from '../../i18n/translations'
import { getModelIcon } from '../common/ModelIcons'
import { getExchangeIcon } from '../common/ExchangeIcons'
import { AI_PROVIDER_CONFIG, truncateAddress } from './model-constants'

interface UsageInfo {
  runningCount: number
  totalCount: number
}
interface ConfigStatusGridProps {
  configuredModels: AIModel[]
  configuredExchanges: Exchange[]
  exchangeAccountStates?: Record<string, ExchangeAccountState>
  isExchangeAccountStatesLoading?: boolean
  accountStateError?: string
  onCheckAccounts?: () => void
  visibleExchangeAddresses: Set<string>
  copiedId: string | null
  language: Language
  getModelUsageInfo: (id: string) => UsageInfo
  getExchangeUsageInfo: (id: string) => UsageInfo
  onModelClick: (id: string) => void
  onExchangeClick: (id: string) => void
  onToggleExchangeAddress: (id: string) => void
  onCopyAddress: (id: string, address: string) => void
  onAddModel?: () => void
  onAddExchange?: () => void
  loading?: boolean
}
const displayFunds = (value: string) => {
  const raw = value.replace(/,/g, '').trim()
  if (!/^\d+(?:\.\d+)?$/.test(raw) || !Number.isFinite(Number(raw)))
    return value
  const amount = Number(raw)
  return amount.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: amount > 0 && amount < 0.01 ? 6 : 2,
  })
}
const accountStatus = (state?: ExchangeAccountState) => {
  if (!state) return 'Verification needed'
  const labels: Record<string, string> = {
    ok: 'Connected',
    disabled: 'Disabled',
    missing_credentials: 'Finish setup',
    invalid_credentials: 'Check credentials',
    permission_denied: 'Check permissions',
  }
  return labels[state.status] || 'Connection unavailable'
}
const accountBalance = (state: ExchangeAccountState) => {
  const asset = state.asset || ''
  const balance = state.display_balance || String(state.total_equity ?? '')
  const numeric =
    asset && balance.endsWith(' ' + asset)
      ? balance.slice(0, -asset.length - 1)
      : balance
  return `${displayFunds(numeric)}${asset ? ' ' + asset : ''}`
}
const usage = (value: UsageInfo) =>
  value.runningCount > 0
    ? `${value.runningCount} running Autopilot${value.runningCount === 1 ? '' : 's'}`
    : value.totalCount > 0
      ? `${value.totalCount} saved Autopilot${value.totalCount === 1 ? '' : 's'}`
      : 'Not used by Autopilot'

export function ConfigStatusGrid({
  configuredModels,
  configuredExchanges,
  exchangeAccountStates,
  isExchangeAccountStatesLoading,
  accountStateError,
  onCheckAccounts,
  visibleExchangeAddresses,
  copiedId,
  language,
  getModelUsageInfo,
  getExchangeUsageInfo,
  onModelClick,
  onExchangeClick,
  onToggleExchangeAddress,
  onCopyAddress,
  onAddModel,
  onAddExchange,
  loading,
}: ConfigStatusGridProps) {
  return (
    <div className="config-connections">
      <section className="config-card" aria-labelledby="config-models-title">
        <header>
          <div className="config-section-icon">
            <Brain size={20} />
          </div>
          <div>
            <h2 id="config-models-title">{t('aiModels', language)}</h2>
            <p>The intelligence behind your trade plans.</p>
          </div>
          <button
            className="config-add"
            onClick={onAddModel}
            disabled={loading}
          >
            <Plus size={14} /> Add AI
          </button>
        </header>
        <div className="config-card-body">
          {loading ? (
            <p role="status" className="config-loading">
              Loading AI connections…
            </p>
          ) : (
            configuredModels.map((model) => (
              <article className="config-connection" key={model.id}>
                <button
                  className="config-connection-main"
                  onClick={() => onModelClick(model.id)}
                  aria-label={`Edit ${model.name}`}
                >
                  <span className="config-provider-icon">
                    {getModelIcon(model.provider || model.id, {
                      width: 23,
                      height: 23,
                    }) || <Brain size={22} />}
                  </span>
                  <span className="config-connection-name">
                    <strong>{model.name}</strong>
                    <span>
                      {model.customModelName ||
                        AI_PROVIDER_CONFIG[model.provider]?.defaultModel ||
                        model.provider}
                    </span>
                  </span>
                  <span
                    className={
                      'config-status ' + (model.enabled ? 'positive' : '')
                    }
                  >
                    {model.enabled ? 'Enabled' : 'Disabled'}
                  </span>
                  <ArrowUpRight size={14} />
                </button>
                {model.provider === 'claw402' && (
                  <div className="config-wallet-info">
                    <span>AI fee wallet · Base USDC</span>
                    <strong>
                      {model.balanceUsdc !== undefined &&
                      model.balanceUsdc !== ''
                        ? `${displayFunds(model.balanceUsdc)} USDC`
                        : 'Balance not available'}
                    </strong>
                    {model.walletAddress && (
                      <code>{truncateAddress(model.walletAddress)}</code>
                    )}
                  </div>
                )}
                <div className="config-connection-foot">
                  <span>{usage(getModelUsageInfo(model.id))}</span>
                  <button onClick={() => onModelClick(model.id)}>
                    {model.provider === 'claw402'
                      ? 'Manage wallet & backup'
                      : 'Manage connection'}
                  </button>
                </div>
              </article>
            ))
          )}
          {!loading && !configuredModels.length && (
            <div className="config-empty">
              <Brain size={28} />
              <h3>{t('noModelsConfigured', language)}</h3>
              <p>
                Connect a supported AI provider or a Claw402 fee wallet. Trading
                funds stay separate.
              </p>
              <button onClick={onAddModel}>
                Connect AI <ArrowUpRight size={14} />
              </button>
            </div>
          )}
        </div>
        <footer>
          AI fees pay for analysis. They are separate from your trading balance.
        </footer>
      </section>
      <section className="config-card" aria-labelledby="config-accounts-title">
        <header>
          <div className="config-section-icon">
            <Landmark size={20} />
          </div>
          <div>
            <h2 id="config-accounts-title">{t('exchanges', language)}</h2>
            <p>Choose where your approved orders are placed.</p>
          </div>
          <button
            className="config-add"
            onClick={onAddExchange}
            disabled={loading}
          >
            <Plus size={14} /> Add account
          </button>
        </header>
        <div className="config-card-body">
          {accountStateError && (
            <p role="alert" className="config-load-error">
              {accountStateError}
            </p>
          )}
          {loading ? (
            <p role="status" className="config-loading">
              Loading trading accounts…
            </p>
          ) : (
            configuredExchanges.map((exchange) => {
              const state = exchangeAccountStates?.[exchange.id]
              const address =
                exchange.hyperliquidWalletAddr ||
                exchange.asterUser ||
                exchange.lighterWalletAddr
              const visible = visibleExchangeAddresses.has(exchange.id)
              return (
                <article className="config-connection" key={exchange.id}>
                  <button
                    className="config-connection-main"
                    onClick={() => onExchangeClick(exchange.id)}
                    aria-label={`Edit ${exchange.name}`}
                  >
                    <span className="config-provider-icon">
                      {getExchangeIcon(exchange.exchange_type || exchange.id, {
                        width: 23,
                        height: 23,
                      }) || <Landmark size={22} />}
                    </span>
                    <span className="config-connection-name">
                      <strong>{exchange.account_name || exchange.name}</strong>
                      <span>
                        {exchange.exchange_type || exchange.type}
                        {exchange.testnet ? ' · Testnet' : ' · Mainnet'}
                      </span>
                    </span>
                    <span
                      className={
                        'config-status ' +
                        (state?.status === 'ok' ? 'positive' : '')
                      }
                    >
                      {isExchangeAccountStatesLoading && !state
                        ? 'Checking…'
                        : accountStateError
                          ? 'Verification unavailable'
                          : accountStatus(state)}
                    </span>
                    <ArrowUpRight size={14} />
                  </button>
                  <div className="config-account-balance">
                    <span>
                      {exchange.exchange_type === 'binance'
                        ? 'USDT Futures balance'
                        : 'Account balance'}
                    </span>
                    <strong>
                      {state?.status === 'ok' && state.display_balance
                        ? accountBalance(state)
                        : isExchangeAccountStatesLoading
                          ? 'Checking…'
                          : 'Not verified'}
                    </strong>
                  </div>
                  {state?.status === 'ok' &&
                    state.available_balance !== undefined && (
                      <div className="config-account-available">
                        <span>Available to trade</span>
                        <strong>
                          {displayFunds(String(state.available_balance))}{' '}
                          {state.asset}
                        </strong>
                      </div>
                    )}
                  {state?.error_message && (
                    <p className="config-account-error">
                      {state.error_message}
                    </p>
                  )}
                  {address && (
                    <div className="config-address">
                      <code>
                        {visible ? address : truncateAddress(address)}
                      </code>
                      <button
                        aria-label={`${visible ? 'Hide' : 'Show'} ${exchange.name} wallet address`}
                        onClick={() => onToggleExchangeAddress(exchange.id)}
                      >
                        {visible ? <EyeOff size={13} /> : <Eye size={13} />}
                      </button>
                      <button
                        aria-label={`Copy ${exchange.name} wallet address`}
                        onClick={() =>
                          onCopyAddress(`exchange-${exchange.id}`, address)
                        }
                      >
                        {copiedId === `exchange-${exchange.id}` ? (
                          <Check size={13} />
                        ) : (
                          <Copy size={13} />
                        )}
                      </button>
                    </div>
                  )}
                  <div className="config-connection-foot">
                    <span>
                      {getExchangeUsageInfo(exchange.id).totalCount > 0
                        ? usage(getExchangeUsageInfo(exchange.id))
                        : 'Autopilot optional · not required for Frontier'}
                    </span>
                    <button
                      disabled={isExchangeAccountStatesLoading}
                      onClick={onCheckAccounts}
                    >
                      {isExchangeAccountStatesLoading
                        ? 'Checking…'
                        : 'Verify connection'}
                    </button>
                    <button onClick={() => onExchangeClick(exchange.id)}>
                      Manage account
                    </button>
                  </div>
                </article>
              )
            })
          )}
          {!loading && !configuredExchanges.length && (
            <div className="config-empty">
              <Landmark size={28} />
              <h3>{t('noExchangesConfigured', language)}</h3>
              <p>
                Add Hyperliquid, Binance or another supported exchange.
                Connecting does not start trading.
              </p>
              <button onClick={onAddExchange}>
                Add account <ArrowUpRight size={14} />
              </button>
            </div>
          )}
        </div>
        <footer>
          Review permissions and balances before trading. A saved account is not
          an active bot.
        </footer>
      </section>
    </div>
  )
}
