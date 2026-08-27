import { Brain, Landmark, Rocket, Sparkles } from 'lucide-react'

interface BeginnerGuideCardsProps {
  claw402Ready: boolean
  exchangeReady: boolean
  strategyReady: boolean
  traderReady: boolean
  canCreateTrader: boolean
  walletAddress?: string | null
  onQuickSetupClaw402: () => void
  onOpenExchange: () => void
  onOpenStrategy: () => void
  onCreateTrader: () => void
}

function truncateAddress(address: string) {
  if (address.length <= 12) return address
  return `${address.slice(0, 6)}...${address.slice(-4)}`
}

export function BeginnerGuideCards({
  claw402Ready,
  exchangeReady,
  strategyReady,
  traderReady,
  canCreateTrader,
  walletAddress,
  onQuickSetupClaw402,
  onOpenExchange,
  onOpenStrategy,
  onCreateTrader,
}: BeginnerGuideCardsProps) {
  // ponytail: en-only; ternary branches were identical on both sides
  const cards = [
    {
      key: 'model',
      icon: Brain,
      title: '1. Fast AI',
      desc: 'Start with Claw402 + DeepSeek. No model picking needed for the first run.',
      meta: walletAddress
        ? `Wallet ${truncateAddress(walletAddress)}`
        : 'Pay per call with Base USDC',
      ready: claw402Ready,
      actionLabel: claw402Ready ? 'Configured' : 'One-click setup',
      onAction: onQuickSetupClaw402,
      disabled: claw402Ready,
    },
    {
      key: 'exchange',
      icon: Landmark,
      title: '2. Add Exchange',
      desc: 'Connect an exchange so the AI can actually place trades.',
      meta: exchangeReady ? 'Ready' : 'Binance / OKX / Bybit / Hyperliquid',
      ready: exchangeReady,
      actionLabel: exchangeReady ? 'Manage' : 'Configure',
      onAction: onOpenExchange,
      disabled: false,
    },
    {
      key: 'strategy',
      icon: Sparkles,
      title: '3. Pick Strategy',
      desc: 'You can start with a default strategy and fine-tune later.',
      meta: strategyReady
        ? 'Strategy ready'
        : 'Optional, but worth a quick look',
      ready: strategyReady,
      actionLabel: 'Open strategy',
      onAction: onOpenStrategy,
      disabled: false,
    },
    {
      key: 'trader',
      icon: Rocket,
      title: '4. Create Trader',
      desc: 'Last step: bind your model and exchange, then start running.',
      meta: traderReady
        ? 'Trader created, you can add more'
        : canCreateTrader
          ? 'Ready to create'
          : 'Finish the first three steps first',
      ready: traderReady,
      actionLabel: traderReady ? 'Create another' : 'Create now',
      onAction: onCreateTrader,
      disabled: !canCreateTrader,
    },
  ]

  return (
    <section className="space-y-4 rounded-[28px] border border-nofx-gold/20 bg-nofx-bg-lighter p-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-xs font-semibold uppercase tracking-[0.3em] text-nofx-gold/80">
            Quickstart
          </div>
          <h2 className="mt-1 text-xl font-bold text-nofx-text">
            Follow these 4 steps to get started fast
          </h2>
        </div>
        {/* <div className="rounded-full border border-nofx-gold/20 bg-nofx-bg-deeper px-3 py-1 text-xs text-nofx-text-muted">
          {isZh ? 'Hidden in advanced mode' : 'Hidden in advanced mode'}
        </div> */}
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => {
          const Icon = card.icon
          return (
            <div
              key={card.key}
              className="rounded-[22px] border border-nofx-gold/20 bg-nofx-bg-deeper p-4"
            >
              <div className="flex items-center justify-between gap-3">
                <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-nofx-gold/10 text-nofx-gold">
                  <Icon className="h-5 w-5" />
                </div>
                <span
                  className={`rounded-full px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.22em] ${
                    card.ready
                      ? 'bg-nofx-success/15 text-nofx-success'
                      : 'bg-nofx-bg-deeper text-nofx-text-muted'
                  }`}
                >
                  {card.ready ? 'Ready' : 'Pending'}
                </span>
              </div>

              <h3 className="mt-4 text-base font-semibold text-nofx-text">
                {card.title}
              </h3>
              <p className="mt-2 min-h-[72px] text-sm leading-6 text-nofx-text-muted">
                {card.desc}
              </p>
              <div className="mt-3 text-xs text-nofx-text-muted">
                {card.meta}
              </div>

              <button
                type="button"
                onClick={card.onAction}
                disabled={card.disabled}
                className={`mt-5 w-full rounded-2xl px-4 py-3 text-sm font-semibold transition ${
                  card.disabled
                    ? 'cursor-not-allowed bg-nofx-bg-deeper text-nofx-text-muted'
                    : 'bg-nofx-gold text-white hover:bg-nofx-gold/90'
                }`}
              >
                {card.actionLabel}
              </button>
            </div>
          )
        })}
      </div>
    </section>
  )
}
