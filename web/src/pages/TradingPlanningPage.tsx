import { frontierVenues, defaultMarket } from '../lib/frontierAccounts'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { getAuthHeaders, handleJSONResponse } from '../lib/api/helpers'
import { type AIModel, type Exchange } from '../types'
import { ROUTES } from '../router/paths'
import {
  type Plan,
  type LiveRun,
  type PlanningState,
  type Constraints,
  type Drawing,
  type Idea,
  type PlanningChart,
  type ChartContext,
  defaultLimits,
  expired,
  loadPlanningChart,
  planningAPI,
  streamPlanning,
} from '../lib/planning'
import { formatRequestError } from '../lib/requestErrors'
import { preservePlanningPlans } from '../lib/planningPresentation'
import { PlanningDataPanel } from '../components/planning/PlanningDataPanel'
import { FrontierMarkets } from '../components/planning/FrontierMarkets'
import { ExecutionSchedule } from '../components/planning/ExecutionSchedule'
import {
  PlanningActivity,
  recordActivity,
  type ActivityItem,
} from '../components/planning/PlanningActivity'
import {
  PlanStructures,
  type PlanCheck,
} from '../components/planning/PlanStructures'
import { FrontierOrderBlocker } from '../components/planning/FrontierOrderBlocker'
import { PlanFunding } from '../components/planning/PlanFunding'
import { FrontierAccount } from '../components/planning/FrontierAccount'
import {
  FrontierSetup,
  type FeeWalletState,
} from '../components/planning/FrontierSetup'
import {
  accountLabel,
  preferredAccount as preferredTradingAccount,
  rememberAccount,
} from '../lib/frontierAccounts'
import { nativeMarket } from '../lib/frontierAccount'
import {
  batchPlans,
  comparablePlans,
  readBatch,
  saveBatch,
  workspaceKey,
  type PlanBatch,
  type AccountObservation,
} from '../lib/frontierWorkspace'
import './TradingPlanningPage.css'
const format = (n: string | number) =>
  Number(n).toLocaleString('en-US', { maximumFractionDigits: 8 })
const states: Record<string, string> = {
  draft: 'Awaiting approval',
  approved: 'Approved',
  running: 'Running',
  position_open: 'Position open',
  stopping: 'Stop further entries',
  needs_attention: 'Needs reconciliation',
  completed: 'Completed',
  cancelled: 'Cancelled',
  expired: 'Expired',
}
const orderStates: Record<string, string> = {
  scheduled: 'Scheduled',
  submitting: 'Submitting',
  UNKNOWN: 'Unconfirmed receipt',
  NEW: 'Open',
  PARTIALLY_FILLED: 'Partially filled',
  FILLED: 'Filled',
  CANCELED: 'Cancelled',
  REJECTED: 'Rejected',
  EXPIRED: 'Expired',
  TRIGGERED: 'Triggered · verify fills',
}
interface ChatMessage {
  role: 'user' | 'assistant' | 'error'
  content: string
}
export function TradingPlanningPage() {
  const [observation, setObservation] = useState<AccountObservation | null>(
    null
  )
  const pendingIntent = useRef<{
    venue: string
    symbol: string
    text?: string
    compare?: boolean
    plan?: Plan
  } | null>(null)
  const [setupOpen, setSetupOpen] = useState<'ai' | 'exchange' | null>(null)
  const [walletState, setWalletState] = useState<FeeWalletState>('checking')
  const [configsLoaded, setConfigsLoaded] = useState(false)
  const [accounts, setAccounts] = useState<Exchange[]>([]),
    [models, setModels] = useState<AIModel[]>([]),
    [exchangeID, setExchangeID] = useState(''),
    [modelID, setModelID] = useState('')
  const [venue, setVenue] = useState('binance')
  const [ai500Symbols, setAI500Symbols] = useState<string[]>([])
  const requestedVenue = useRef('binance')
  const marketChosen = useRef(false)
  const [symbol, setSymbol] = useState('BTCUSDT'),
    [symbolInput, setSymbolInput] = useState('BTCUSDT'),
    [limits, setLimits] = useState<Constraints>(defaultLimits)
  const marketGroup =
    venue === 'binance' && ai500Symbols.includes(symbol) ? 'ai500' : undefined
  const entryPolicy = marketGroup ? 'ai500_trend_long' : undefined
  const [data, setData] = useState<PlanningState>({
      records: {},
      accounts: {},
      runs: [],
      mode: 'live',
      now: '',
    }),
    [selected, setSelected] = useState(''),
    [compare, setCompare] = useState<string[]>([])
  const [messages, setMessages] = useState<ChatMessage[]>([]),
    [instruction, setInstruction] = useState(''),
    [busy, setBusy] = useState(false),
    [planningBusy, setPlanningBusy] = useState(false),
    [status, setStatus] = useState(''),
    [error, setError] = useState(''),
    [orderNotice, setOrderNotice] = useState(''),
    [price, setPrice] = useState('—'),
    [interval, setInterval] = useState('1h')
  const [tradeCheckError, setTradeCheckError] = useState<{
    runID: string
    message: string
  } | null>(null)
  const [reviewReplacement, setReviewReplacement] = useState<LiveRun | null>(
    null
  )
  const [trace, setTrace] = useState<ActivityItem[]>([]),
    [review, setReview] = useState<Plan | null>(null),
    [leverage, setLeverage] = useState(5),
    [extrasOpen, setExtrasOpen] = useState(false),
    [builder, setBuilder] = useState(false),
    [builderIdea, setBuilderIdea] = useState<Idea>({
      name: 'Custom plan',
      side: 'long',
      entry: '',
      stop: '',
      target: '',
      rationale:
        'User-defined conditional prices, subject to mathematical validation; neither approved nor executed.',
      execution: { kind: 'limit' },
    }),
    [levels, setLevels] = useState('')
  const [pluginWindow, setPluginWindow] = useState('7d')
  const [preferences, setPreferences] = useState(''),
    [history, setHistory] = useState<
      {
        id: string
        symbol: string
        exchange_id: string
        model_id: string
        messages: string
      }[]
    >([]),
    [tools, setTools] = useState<unknown[]>([])
  const chart = useRef<PlanningChart | null>(null),
    abort = useRef<AbortController | null>(null),
    session = useRef<string>(crypto.randomUUID()),
    chatBox = useRef<HTMLDivElement | null>(null),
    chatFollow = useRef(true),
    active = useRef(true)
  const account = accounts.find((a) => a.id === exchangeID)
  const records = useMemo(
    () =>
      Object.values(data.records)
        .filter(
          (r) =>
            data.accounts[r.plan.id] === exchangeID &&
            r.plan.snapshot.symbol === symbol &&
            r.plan.snapshot.venue === venue
        )
        .sort(
          (a, b) =>
            Date.parse(b.plan.created) - Date.parse(a.plan.created) ||
            ['market', 'limit', 'ladder', 'twap'].indexOf(
              a.plan.idea.execution.kind
            ) -
              ['market', 'limit', 'ladder', 'twap'].indexOf(
                b.plan.idea.execution.kind
              )
        ),
    [data, exchangeID, symbol, venue]
  )
  const [checks, setChecks] = useState<PlanCheck[]>([])
  const [turnPlanIDs, setTurnPlanIDs] = useState<string[]>([])
  const batchKey = workspaceKey(exchangeID, venue, symbol)
  const [batch, setBatch] = useState<{ key: string; value: PlanBatch | null }>(
    () => ({ key: batchKey, value: readBatch(batchKey) })
  )
  const currentBatch =
    batch.key === batchKey ? batch.value : readBatch(batchKey)
  function rememberBatch(value: PlanBatch, key = batchKey) {
    saveBatch(key, value)
    setBatch({ key, value })
  }
  function showSection(section: 'chart' | 'plans' | 'assistant' | 'account') {
    requestAnimationFrame(() =>
      document
        .getElementById('frontier-' + section)
        ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    )
  }
  function requestReview(plan: Plan) {
    if (!exchangeID) {
      pendingIntent.current = { venue, symbol, plan }
      setSetupOpen('exchange')
    } else void reviewWithAccount(plan)
  }
  const openSetup = useCallback((step: 'ai' | 'exchange' | null) => {
    if (!step) pendingIntent.current = null
    setSetupOpen(step)
  }, [])
  function completeSetup(step: 'ai' | 'exchange') {
    const intent = pendingIntent.current
    pendingIntent.current = null
    if (!intent) return
    if (intent.venue !== venue || intent.symbol !== symbol) {
      setOrderNotice(
        'Connection saved. Choose a plan for the current market to continue.'
      )
      return
    }
    if (step === 'ai' && intent.text) void send(intent.text, intent.compare)
    if (step === 'exchange' && intent.plan && exchangeID)
      void reviewWithAccount(intent.plan)
  }
  function resetTurn() {
    setChecks([])
    setTurnPlanIDs([])
    setTrace([])
  }
  const selectedRun = data.runs.find(
    (r) => r.plan_id === selected && r.exchange_id === exchangeID
  )
  const current = useMemo(
    () =>
      data.records[selected] ||
      (selectedRun
        ? { plan: selectedRun.plan, state: selectedRun.state }
        : undefined),
    [data.records, selected, selectedRun?.plan, selectedRun?.state]
  )
  const run = selectedRun
  const clock = Date.parse(data.now) || Date.now()
  const blockingRun = data.runs.find(
    (r) =>
      r.exchange_id === exchangeID &&
      [
        'approved',
        'starting',
        'running',
        'position_open',
        'stopping',
        'needs_attention',
      ].includes(r.state) &&
      !(r.state === 'approved' && expired(r.plan, clock))
  )
  useEffect(() => {
    setOrderNotice('')
    setTradeCheckError(null)
  }, [exchangeID])
  const reload = useCallback(async () => {
    const next = await planningAPI<PlanningState>('state')
    if (active.current)
      setData((old) =>
        JSON.stringify({ ...old, now: '' }) ===
        JSON.stringify({ ...next, now: '' })
          ? { ...old, now: next.now }
          : preservePlanningPlans(old, next)
      )
  }, [])
  const refreshHistory = useCallback(async () => {
    const rows = await planningAPI<typeof history>('conversations')
    if (active.current) setHistory(rows)
  }, [])
  useEffect(() => {
    active.current = true
    Promise.all([api.getExchangeConfigs(), api.getModelConfigs()])
      .then(([ex, ai]) => {
        if (!active.current) return
        const supported = ex.filter(
          (a) =>
            a.enabled &&
            !a.testnet &&
            frontierVenues.includes(a.exchange_type || '')
        )
        const enabled = ai.filter((m) => m.enabled && m.has_api_key)
        setAccounts(supported)
        setModels(enabled)
        const preferred =
          supported.find(
            (a) =>
              a.id ===
              preferredTradingAccount(supported, requestedVenue.current)
          ) ||
          (!marketChosen.current &&
          supported.length === 1 &&
          supported[0].exchange_type !== requestedVenue.current
            ? supported[0]
            : undefined)
        setExchangeID(preferred?.id || '')
        if (
          preferred &&
          !marketChosen.current &&
          preferred.exchange_type !== requestedVenue.current
        ) {
          requestedVenue.current = preferred.exchange_type
          setVenue(preferred.exchange_type)
          const native = defaultMarket(preferred.exchange_type || '')
          setSymbol(native)
          setSymbolInput(native)
        }
        setModelID(enabled[0]?.id || '')
        setConfigsLoaded(true)
      })
      .catch((e) => setError(String(e.message)))
    reload().catch((e) => setError(e.message))
    refreshHistory().catch(() => {})
    planningAPI<{ text: string }>('preferences')
      .then((p) => setPreferences(p.text))
      .catch(() => {})
    const timer = window.setInterval(() => reload().catch(() => {}), 4000)
    return () => {
      active.current = false
      clearInterval(timer)
      abort.current?.abort()
      chart.current?.dispose()
      chart.current = null
    }
  }, [reload, refreshHistory])
  useEffect(() => {
    let cancelled = false
    let chartFailure = ''
    const reportChartFailure = (message: string) => {
      if (cancelled) return
      chartFailure = message
      setError(message)
    }
    loadPlanningChart()
      .then(() => {
        if (cancelled) return
        chart.current?.dispose()
        chart.current = new window.WorkspaceChart({
          venue,
          symbol,
          onStatus: () => {},
          onPrice: (c: { close: string }) => !cancelled && setPrice(c.close),
          onContext: (c: ChartContext) => !cancelled && setInterval(c.interval),
          onError: reportChartFailure,
          onAvailability: (available: boolean) => {
            if (cancelled) return
            if (available && chartFailure)
              setError((message) => (message === chartFailure ? '' : message))
          },
        })
        chart.current
          .setContext(venue, symbol, '60')
          .catch((e) => reportChartFailure(e.message))
      })
      .catch((e) => reportChartFailure(e.message))
    return () => {
      cancelled = true
      chart.current?.dispose()
      chart.current = null
    }
  }, [venue, symbol])
  useEffect(() => {
    if (!chart.current) return
    const list = compare
      .map((id) => data.records[id]?.plan)
      .filter(
        (p): p is Plan =>
          !!p && p.snapshot.symbol === symbol && p.snapshot.venue === venue
      )
    chart.current
      .plot(
        current?.plan || null,
        run?.state || current?.state || 'draft',
        list.length >= 2 ? list : []
      )
      .catch((e) => setError(e.message))
  }, [current, compare, data.records, symbol, venue, run?.state])
  useEffect(() => {
    const box = chatBox.current
    if (box && chatFollow.current)
      box.scrollTop = messages.length || trace.length ? box.scrollHeight : 0
  }, [messages, trace])
  async function refreshSetup(
    preferredModel?: string,
    preferredVenue?: string,
    preferredAccount?: string
  ) {
    const [ex, ai] = await Promise.all([
      api.getExchangeConfigs(),
      api.getModelConfigs(),
    ])
    if (!active.current) return
    const supported = ex.filter(
      (a) =>
        a.enabled &&
        !a.testnet &&
        frontierVenues.includes(a.exchange_type || '')
    )
    const enabled = ai.filter((m) => m.enabled && m.has_api_key)
    setAccounts(supported)
    setModels(enabled)
    setModelID((old) =>
      preferredModel && enabled.some((m) => m.id === preferredModel)
        ? preferredModel
        : enabled.some((m) => m.id === old)
          ? old
          : enabled[0]?.id || ''
    )
    const nextVenue = preferredVenue || venue
    const matching = supported.filter((a) => a.exchange_type === nextVenue)
    const nextAccountID = matching.some((a) => a.id === preferredAccount)
      ? preferredAccount!
      : matching.some((a) => a.id === exchangeID)
        ? exchangeID
        : preferredTradingAccount(supported, nextVenue)
    setExchangeID(nextAccountID)
    rememberAccount(nextVenue, nextAccountID)
    if (nextAccountID !== exchangeID) {
      setReview(null)
      setBuilder(false)
      setObservation(null)
      setLimits(defaultLimits)
      setError('')
      setOrderNotice('')
      setSelected('')
      setCompare([])
      resetTurn()
    }
    if (preferredVenue && preferredVenue !== venue) {
      requestedVenue.current = preferredVenue
      setVenue(preferredVenue)
      const native = defaultMarket(preferredVenue)
      setSymbol(native)
      setSymbolInput(native)
      setSelected('')
      setCompare([])
    }
    setConfigsLoaded(true)
  }
  function openMarket(next: string) {
    marketChosen.current = true
    requestedVenue.current = venue
    setPrice('—')
    setSymbol(next)
    setSymbolInput(next)
    setSelected('')
    resetTurn()
    setCompare([])
    setMessages([])
    setTrace([])
    setError('')
    setOrderNotice('')
    session.current = crypto.randomUUID()
  }
  async function choose(p: Plan) {
    setSelected(p.id)
    setCompare([])
    setError('')
    showSection('chart')
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
    await chart.current?.plot(p, data.records[p.id]?.state || 'draft', [])
    await chart.current?.focusPlan()
  }
  function changeAccount(
    id: string,
    nextVenue = accounts.find((a) => a.id === id)?.exchange_type || venue
  ) {
    marketChosen.current = true
    abort.current?.abort()
    pendingIntent.current = null
    setExchangeID(id)
    if (configsLoaded) rememberAccount(nextVenue, id)
    setLimits(defaultLimits)
    setObservation(null)
    setReview(null)
    setBuilder(false)
    setError('')
    setOrderNotice('')
    requestedVenue.current = nextVenue
    setVenue(nextVenue)
    if (nextVenue !== venue) {
      const native = defaultMarket(nextVenue)
      setSymbol(native)
      setSymbolInput(native)
      setPrice('—')
    }
    setSelected('')
    resetTurn()
    setCompare([])
    setMessages([])
    session.current = crypto.randomUUID()
  }
  function viewTrade(trade: LiveRun) {
    marketChosen.current = true
    requestedVenue.current = trade.plan.snapshot.venue
    setVenue(trade.plan.snapshot.venue)
    setExchangeID(trade.exchange_id)
    setSymbol(
      nativeMarket(trade.plan.snapshot.symbol, trade.plan.snapshot.venue)
    )
    setSymbolInput(
      nativeMarket(trade.plan.snapshot.symbol, trade.plan.snapshot.venue)
    )
    setSelected(trade.plan_id)
    setCompare([])
    resetTurn()
    setMessages([])
  }
  function manualEntry(side: 'long' | 'short') {
    setBuilderIdea({
      name: `Manual ${symbol} ${side}`,
      rationale: 'User-created manual limit entry',
      side,
      entry: price === '—' ? '' : price,
      stop: '',
      target: '',
      execution: { kind: 'limit' },
    })
    setBuilder(true)
  }
  async function send(text = instruction, compareStructures = false) {
    if (busy || !text.trim()) return
    if (
      !modelID ||
      (models.find((m) => m.id === modelID)?.provider === 'claw402' &&
        walletState !== 'ready')
    ) {
      setInstruction(text)
      pendingIntent.current = {
        venue,
        symbol,
        text,
        compare: compareStructures,
      }
      setSetupOpen('ai')
      return
    }
    const context = chart.current?.context()
    if (!context || context.venue !== venue || context.symbol !== symbol) {
      setError(
        'The chart is still switching markets. Try again once it is ready.'
      )
      return
    }
    // A locked trade is immutable, but it must not lock the whole workspace.
    const separateDraft = Boolean(current && current.state !== 'draft')
    const revisionID =
      !compareStructures && current?.state === 'draft' ? selected : undefined
    const historyMessages = separateDraft
      ? []
      : messages.filter((m) => m.role !== 'error').slice(-16)
    if (separateDraft) {
      session.current = crypto.randomUUID()
      setSelected('')
      setCompare([])
      setMessages([])
    }
    setBusy(true)
    setPlanningBusy(true)
    const generation: PlanBatch = {
      id: crypto.randomUUID(),
      ids: [],
      created: new Date().toISOString(),
    }
    setError('')
    setOrderNotice('')
    setInstruction('')
    resetTurn()
    chatFollow.current = true
    setStatus('Reading current market context…')
    setMessages((m) => [
      ...(separateDraft ? [] : m),
      { role: 'user', content: text },
    ])
    const controller = new AbortController()
    abort.current = controller
    let streamStarted = false
    let waiting = false
    let preferredPlan: Plan | undefined
    try {
      await streamPlanning(
        {
          exchange_id: exchangeID,
          model_id: modelID,
          symbol,
          venue,
          interval: context.interval,
          visible_from: context.visible_from,
          visible_to: context.visible_to,
          message: text,
          market_group: marketGroup,
          plan_id: revisionID,
          execution_kind: 'auto',
          compare_structures: compareStructures,
          plugin_window: pluginWindow,
          session_id: session.current,
          turn_id: generation.id,
          history: historyMessages,
          constraints: { ...limits, leverage, entry_policy: entryPolicy },
          auto_size: Boolean(exchangeID),
        },
        controller.signal,
        (event) => {
          streamStarted = true
          setTrace((items) => recordActivity(items, event))
          if (event.type === 'planning_sizing')
            setOrderNotice(String(event.data.message))
          if (event.type === 'assistant')
            setMessages((m) => [
              ...m,
              { role: 'assistant', content: String(event.data.message) },
            ])
          if (event.type === 'error') {
            const message = formatRequestError(String(event.data.message), 'en')
            setError(message)
          }
          if (event.type === 'status') setStatus(String(event.data.message))
          if (event.type === 'iteration')
            setStatus(
              `Planning round ${event.data.round} of ${event.data.max_rounds}`
            )
          if (event.type === 'outcome' && Number(event.data.drafts) === 0) {
            setStatus(
              'Planning completed without a validated draft. Review the explanation.'
            )
            if (waiting && event.data.stop_reason === 'completed') {
              rememberBatch({ ...generation, waiting: true }, batchKey)
              setSelected('')
              setCompare([])
            }
          }
          const mergeChecks = (incoming: PlanCheck[]) =>
            setChecks((previous) => {
              const merged = new Map(previous.map((check) => [check.id, check]))
              incoming.forEach((check) => merged.set(check.id, check))
              return [...merged.values()]
            })
          if (event.type === 'candidate_result')
            mergeChecks([event.data as unknown as PlanCheck])
          if (event.type === 'plans') {
            waiting = event.data.waiting === true
            if (Array.isArray(event.data.checks)) {
              mergeChecks(event.data.checks as PlanCheck[])
              const deferred = new Map(
                (generation.deferred || []).map((entry) => [entry.kind, entry])
              )
              for (const check of event.data.checks as PlanCheck[]) {
                if (check.status === 'deferred' && check.reason)
                  deferred.set(check.kind, {
                    kind: check.kind,
                    reason: check.reason,
                  })
                else if (check.status === 'saved' || check.status === 'reused')
                  deferred.delete(check.kind)
              }
              generation.deferred = [...deferred.values()]
            }
            const plans = (event.data.plans as Plan[])
              ?.slice()
              .sort(
                (a, b) =>
                  ['market', 'limit', 'ladder', 'twap'].indexOf(
                    a.idea.execution.kind
                  ) -
                  ['market', 'limit', 'ladder', 'twap'].indexOf(
                    b.idea.execution.kind
                  )
              )
            if (plans?.length) {
              generation.ids = [
                ...new Set([...generation.ids, ...plans.map((p) => p.id)]),
              ]
              rememberBatch({ ...generation }, batchKey)
              setLimits(plans[0].constraints)
              setTurnPlanIDs((ids) => [
                ...new Set([...ids, ...plans.map((plan) => plan.id)]),
              ])
              const rank = (p: Plan) =>
                ['market', 'limit', 'ladder', 'twap'].indexOf(
                  p.idea.execution.kind
                )
              if (!preferredPlan || rank(plans[0]) <= rank(preferredPlan))
                preferredPlan = plans[0]
              setSelected(preferredPlan.id)

              setCompare([])
            } else if (generation.ids.length) {
              rememberBatch({ ...generation }, batchKey)
            }
            reload().catch(() => {})
          }
          if (event.type === 'drawings') {
            const drawings = event.data.drawings as Drawing[]
            chart.current
              ?.draw(drawings, 'ai', context.interval)
              .catch((e) => setError(e.message))
          }
        }
      )
    } catch (e) {
      if (!controller.signal.aborted) {
        if (!streamStarted) setMessages(separateDraft ? [] : messages)
        const message = formatRequestError(e, 'en')
        setError(message)
        setTrace((items) =>
          recordActivity(items, { type: 'error', data: { message } })
        )
      }
    } finally {
      if (controller.signal.aborted)
        setTrace((items) =>
          recordActivity(items, { type: 'cancelled', data: {} })
        )
      setBusy(false)
      setPlanningBusy(false)
      setStatus(
        controller.signal.aborted ? 'Planning stopped' : 'Planning finished'
      )
      await reload().catch(() => {})
      await refreshHistory().catch(() => {})
    }
  }
  async function reviewWithAccount(plan: Plan) {
    if (busy) return
    setBusy(true)
    setError('')
    setOrderNotice('')
    setTradeCheckError(null)
    setReviewReplacement(null)
    let checkingPreviousTrade = false
    let replacement: LiveRun | null = null
    try {
      const existingPosition =
        observation?.accountID === exchangeID &&
        Math.abs(clock - observation.checkedAt) < 30000
          ? observation.positions.find(
              (p) =>
                nativeMarket(p.symbol, venue) ===
                nativeMarket(blockingRun?.plan.snapshot.symbol || symbol, venue)
            )
          : undefined
      if (blockingRun && existingPosition) {
        showSection('account')
        return
      }
      if (blockingRun?.state === 'approved') {
        replacement = blockingRun
      } else if (blockingRun) {
        checkingPreviousTrade = true
        const checked = await planningAPI<LiveRun>('check-status', {
          id: blockingRun.id,
        })
        await reload()
        if (checked.state === 'approved') {
          replacement = checked
        } else if (
          !['completed', 'cancelled', 'expired'].includes(checked.state)
        ) {
          throw new Error(
            'The previous trade is still active. Review Positions & orders below before placing another.'
          )
        }
        checkingPreviousTrade = false
      }
      const sized = await planningAPI<Plan>('compile', {
        exchange_id: exchangeID,
        venue,
        symbol,
        idea: plan.idea,
        market_group: marketGroup,
        constraints: {
          ...plan.constraints,
          leverage,
          min_reward_risk: limits.min_reward_risk,
          entry_policy: plan.constraints.entry_policy || entryPolicy,
        },
        auto_size: true,
      })
      await reload()
      // A refreshed account size is a new review, never a replacement in an older comparison set.
      setSelected(sized.id)
      setCompare([])
      setLimits(sized.constraints)
      setReviewReplacement(replacement)
      setReview(sized)
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e)
      if (checkingPreviousTrade && blockingRun) {
        setTradeCheckError({ runID: blockingRun.id, message })
        await reload().catch(() => {})
      } else setError(message)
    } finally {
      setBusy(false)
    }
  }
  async function approveAndExecute() {
    if (!review || busy) return
    const locked = review
    setBusy(true)
    setError('')
    setOrderNotice('')
    try {
      const approved = await planningAPI<LiveRun>('approve', {
        plan_id: locked.id,
        hash: locked.hash,
        confirmed: true,
        ...(reviewReplacement
          ? {
              replace_approval_id: reviewReplacement.id,
              replace_approval_hash: reviewReplacement.approval_hash,
            }
          : {}),
      })
      setReview(null)
      await planningAPI('execute', {
        id: approved.id,
        hash: approved.approval_hash,
      })
      await reload()
    } catch (e) {
      setReview(null)
      setError(e instanceof Error ? e.message : String(e))
      await reload().catch(() => {})
    } finally {
      setBusy(false)
    }
  }
  async function checkPreviousTrade(r: LiveRun) {
    setBusy(true)
    setError('')
    setOrderNotice('')
    setTradeCheckError(null)
    try {
      const checked = await planningAPI<LiveRun>('check-status', { id: r.id })
      await reload()
      setOrderNotice(
        checked.state === 'completed'
          ? 'Previous trade is closed. Your account is ready for a new order. Review a plan when you are ready.'
          : ''
      )
    } catch (e) {
      setTradeCheckError({
        runID: r.id,
        message: e instanceof Error ? e.message : String(e),
      })
      await reload().catch(() => {})
    } finally {
      setBusy(false)
    }
  }
  async function runAction(
    action: 'execute' | 'stop' | 'reconcile',
    r: LiveRun
  ) {
    setBusy(true)
    setError('')
    setOrderNotice('')
    try {
      await planningAPI(action, { id: r.id, hash: r.approval_hash })
      await reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  async function saveBuilder() {
    setBusy(true)
    setError('')
    setReviewReplacement(blockingRun?.state === 'approved' ? blockingRun : null)
    try {
      const idea = { ...builderIdea, execution: { ...builderIdea.execution } }
      if (idea.execution.kind === 'ladder') {
        idea.execution.levels = levels
          .split('\n')
          .filter(Boolean)
          .map((line) => {
            const [p, w] = line.trim().split(/\s+/)
            return { price: p, weight: w }
          })
      }
      const p = await planningAPI<Plan>('compile', {
        exchange_id: exchangeID,
        venue,
        symbol,
        idea,
        market_group: marketGroup,
        constraints: { ...limits, leverage, entry_policy: entryPolicy },
        auto_size: Boolean(exchangeID),
      })
      setBuilder(false)
      await reload()
      setSelected(p.id)
      setCompare([])
      setReview(p)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  function newSession() {
    abort.current?.abort()
    session.current = crypto.randomUUID()
    setMessages([])
    setSelected('')
    resetTurn()
    setCompare([])
    setTrace([])
    setError('')
    setOrderNotice('')
  }
  const outsideEntryPolicy = (plan: Plan) =>
    (entryPolicy === 'ai500_trend_long' ||
      plan.constraints.entry_policy === 'ai500_trend_long') &&
    plan.idea.side !== 'long'
  const deckPlans = batchPlans(records, currentBatch).filter(
    (plan) => !outsideEntryPolicy(plan)
  )
  const waitingForEntry = currentBatch?.waiting && !planningBusy
  const entryChecks =
    checks.length || planningBusy
      ? checks
      : (currentBatch?.deferred || []).map((entry) => ({
          id: `deferred_${entry.kind}`,
          kind: entry.kind,
          name: 'Wait for entry conditions',
          status: 'deferred',
          reason: entry.reason,
        }))
  const comparisonPlans = comparablePlans(deckPlans, clock)
  const archivedRecords = records.filter(
    (r) => !deckPlans.some((p) => p.id === r.plan.id)
  )
  const renderPlanCard = (r: PlanningState['records'][string]) => (
    <button
      key={r.plan.id}
      className={'planning-card ' + (selected === r.plan.id ? 'selected' : '')}
      onClick={() => choose(r.plan)}
      disabled={r.state === 'draft' && outsideEntryPolicy(r.plan)}
    >
      <span
        className={r.plan.idea.side === 'long' ? 'target-text' : 'stop-text'}
      >
        {compare.includes(r.plan.id)
          ? String.fromCharCode(65 + compare.indexOf(r.plan.id)) + ' · '
          : ''}
        {r.plan.idea.side === 'long' ? '↗ Long' : '↘ Short'} ·{' '}
        {r.plan.idea.execution.kind === 'market'
          ? 'Market'
          : r.plan.idea.execution.kind === 'ladder'
            ? 'Price ladder'
            : r.plan.idea.execution.kind === 'twap'
              ? 'TWAP'
              : 'Single limit'}
      </span>
      <strong>{r.plan.idea.name}</strong>
      <span>Entry {format(r.plan.idea.entry)}</span>
      <div>
        <span>
          Planned loss <b>{format(r.plan.risk)}</b>
        </span>
        <span>
          Net reward/risk <b>{Number(r.plan.reward_risk).toFixed(2)} : 1</b>
        </span>
      </div>
      <small>
        {r.state === 'draft' && outsideEntryPolicy(r.plan)
          ? 'Outside AI500 long-only policy · generate a new plan'
          : expired(r.plan, clock) && r.state === 'draft'
            ? 'Expired · replan'
            : states[r.state] || r.state}
      </small>
    </button>
  )
  return (
    <div className="planning-page">
      <div className="planning-heading">
        <div>
          <h1>Frontier</h1>
          <p>Your market. Your next move.</p>
        </div>
        <nav className="frontier-workflow" aria-label="How Frontier works">
          <span data-active={!current && !turnPlanIDs.length}>
            <b>1</b> Explore the market
          </span>
          <span data-active={!current && turnPlanIDs.length > 0}>
            <b>2</b> Compare plans
          </span>
          <span data-active={Boolean(current)}>
            <b>3</b> Review & trade
          </span>
        </nav>
        <a
          className="frontier-account-link"
          href="#frontier-account"
          onClick={(event) => {
            event.preventDefault()
            showSection('account')
          }}
        >
          Positions & orders ↓
        </a>
        <Link to={ROUTES.settings}>Settings ↗</Link>
      </div>
      <div className="planning-toolbar">
        <div className="frontier-toolbar-field">
          <span>Exchange</span>
          <select
            aria-label="Market venue"
            value={venue}
            disabled={busy}
            onChange={(e) =>
              changeAccount(
                preferredTradingAccount(accounts, e.target.value),
                e.target.value
              )
            }
          >
            <option value="binance">Binance · USDT perpetual</option>
            <option value="hyperliquid">Hyperliquid · perpetual</option>
            <option value="okx">OKX · USDT perpetual</option>
            <option value="bitget">Bitget · USDT perpetual</option>
          </select>
        </div>
        <div className="frontier-toolbar-field">
          <span>Account</span>
          <select
            aria-label="Trading account"
            value={exchangeID}
            onChange={(e) => {
              if (e.target.value === '__add_account__') {
                setSetupOpen('exchange')
                return
              }
              changeAccount(e.target.value)
            }}
            disabled={busy}
          >
            <option value="">Explore without an account</option>
            {accounts
              .filter((a) => a.exchange_type === venue)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {accountLabel(a, accounts)} · {a.exchange_type}
                </option>
              ))}
            <option value="__add_account__">+ Add another account…</option>
          </select>
        </div>
        <div className="frontier-toolbar-field">
          <span>Market</span>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              openMarket(
                symbolInput.trim().includes(':')
                  ? symbolInput.trim()
                  : symbolInput.trim().toUpperCase()
              )
            }}
          >
            <input
              aria-label="Symbol"
              value={symbolInput}
              onChange={(e) => setSymbolInput(e.target.value)}
              disabled={busy}
            />
            <button disabled={busy}>Open market</button>
          </form>
        </div>
        <span className="planning-live">
          <i /> Live trading · You’re in control
        </span>
      </div>
      <FrontierSetup
        models={models}
        modelID={modelID}
        accounts={accounts}
        accountID={exchangeID}
        venue={venue}
        loaded={configsLoaded}
        open={setupOpen}
        onOpen={openSetup}
        onRefresh={refreshSetup}
        onWalletState={setWalletState}
        onComplete={completeSetup}
      />
      {
        <FrontierMarkets
          venue={venue}
          disabled={busy}
          selected={symbol}
          walletReady={walletState === 'ready'}
          walletChecking={walletState === 'checking'}
          onSetup={() => setSetupOpen('ai')}
          onSelect={openMarket}
          onAI500Markets={setAI500Symbols}
        />
      }
      {blockingRun && (
        <FrontierOrderBlocker
          run={blockingRun}
          now={clock}
          observation={observation}
          checkError={
            tradeCheckError?.runID === blockingRun.id
              ? tradeCheckError.message
              : undefined
          }
          onAccount={() => showSection('account')}
          busy={busy}
          onCheck={() => void checkPreviousTrade(blockingRun)}
          onView={() => {
            viewTrade(blockingRun)
            showSection('account')
          }}
          onRevoke={() => void runAction('stop', blockingRun)}
        />
      )}
      {orderNotice && !blockingRun && !error && (
        <p className="frontier-order-notice" role="status">
          {orderNotice}
        </p>
      )}
      {error &&
        !(
          blockingRun &&
          (error.includes('already has an approved or unresolved plan') ||
            error.includes('An earlier trade is reserving this account'))
        ) &&
        !trace.some(
          (item) => item.state === 'failed' && item.detail === error
        ) && (
          <div className="planning-error" role="alert">
            {formatRequestError(error, 'en')}
            <button onClick={() => setError('')} aria-label="Dismiss">
              ×
            </button>
          </div>
        )}
      <div className="planning-workspace">
        <section className="planning-chart-pane" id="frontier-chart">
          <div className="planning-market">
            <div>
              <b>{symbol}</b>
              <strong>{price === '—' ? price : format(price)}</strong>
              <small>
                <i className="frontier-live-dot" /> Live market · {interval}
              </small>
            </div>
            <div>
              <button
                disabled={!current}
                onClick={() => chart.current?.focusPlan()}
              >
                Fit selected plan
              </button>
              <button
                disabled={busy || comparisonPlans.length < 2}
                onClick={() => {
                  setCompare(comparisonPlans.slice(0, 4).map((p) => p.id))
                }}
              >
                Compare plans
              </button>
            </div>
          </div>
          <div className="frontier-sizing">
            <label>
              Leverage{' '}
              <select
                aria-label="Planning leverage"
                value={leverage}
                disabled={busy}
                onChange={(e) => setLeverage(Number(e.target.value))}
              >
                {[1, 2, 3, 5, 10, 20].map((value) => (
                  <option key={value} value={value}>
                    {value}x
                  </option>
                ))}
              </select>
            </label>
            <span>
              {exchangeID
                ? `Available margin × ${leverage}x · execution costs reserved`
                : 'Connect an account for automatic sizing'}
            </span>
          </div>
          <div className="planning-chart-frame">
            <div className="planning-chart" id="chart-container" />
            {current && compare.length < 2 && (
              <ExecutionSchedule plan={current.plan} />
            )}
          </div>
          <div className="planning-legend">
            Entry <i className="entry" /> Stop loss <i className="stop" /> Take
            profit <i className="target" />
            {compare.length > 1 && (
              <button onClick={() => setCompare([])}>
                Exit comparison · select one plan to focus
              </button>
            )}
          </div>
          <section
            className="frontier-plan-deck"
            id="frontier-plans"
            aria-label="Entry plans"
          >
            <div className="frontier-deck-heading">
              <div>
                <h2>
                  {waitingForEntry
                    ? 'Waiting for an entry'
                    : currentBatch
                      ? 'Your latest plans'
                      : deckPlans.length
                        ? 'Saved idea'
                        : 'Choose your entry'}
                </h2>
                <p>
                  {waitingForEntry ? (
                    'No new entry selected. See the assistant’s explanation and conditions to watch.'
                  ) : (
                    <>
                      {currentBatch
                        ? `Generated together · ${new Date(currentBatch.created).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}. `
                        : 'Older drafts are in history below. '}
                      Review updates the selected plan with current prices and
                      funds.
                    </>
                  )}
                </p>
              </div>
              <span>
                {deckPlans.length} {deckPlans.length === 1 ? 'plan' : 'plans'}
              </span>
            </div>
            <PlanStructures
              checks={entryChecks}
              showAllTypes={Boolean(currentBatch)}
              plans={deckPlans}
              busy={planningBusy}
              selected={selected}
              onSelect={choose}
              onReview={requestReview}
              reviewDisabled={busy}
              connected={Boolean(exchangeID)}
              now={clock}
            />
            {!deckPlans.length &&
              !planningBusy &&
              !entryChecks.length &&
              !waitingForEntry && (
                <p className="frontier-deck-empty">
                  Generate plans to compare market, limit, ladder and TWAP
                  entries here.
                </p>
              )}
            {archivedRecords.length > 0 && (
              <details className="planning-all-plans">
                <summary>Plan history · {archivedRecords.length}</summary>
                <div className="planning-card-list">
                  {archivedRecords.map(renderPlanCard)}
                </div>
              </details>
            )}
          </section>
          {current && (
            <div className="planning-selected">
              <b>
                {current.state === 'draft'
                  ? 'Draft · not submitted · '
                  : 'Trade · '}
                {current.plan.idea.name} ·{' '}
                {current.plan.idea.side === 'long' ? 'Long' : 'Short'}
              </b>
              <div className="planning-price-row">
                <span>
                  {current.plan.idea.execution.kind === 'market'
                    ? 'Est. market entry'
                    : 'Entry'}{' '}
                  <strong>{format(current.plan.idea.entry)}</strong>
                </span>
                <span>
                  Stop loss{' '}
                  <strong className="stop-text">
                    {format(current.plan.idea.stop)}
                  </strong>
                </span>
                <span>
                  Target{' '}
                  <strong className="target-text">
                    {format(current.plan.idea.target)}
                  </strong>
                </span>
                <span>
                  Planned loss{' '}
                  <strong>
                    {format(current.plan.risk)}{' '}
                    {current.plan.snapshot.quote_asset}
                  </strong>
                </span>
              </div>
              <PlanFunding plan={current.plan} />
              <details>
                <summary>
                  {current.plan.orders.length || 1} child orders · Inspect
                  prices, quantities and release times
                </summary>
                <table>
                  <thead>
                    <tr>
                      <th>Child order</th>
                      <th>
                        {current?.plan.idea.execution.kind === 'market'
                          ? 'Execution estimate'
                          : 'Limit price'}
                      </th>
                      <th>Quantity</th>
                      <th>Release time</th>
                    </tr>
                  </thead>
                  <tbody>
                    {current.plan.orders.map((o) => (
                      <tr key={o.index}>
                        <td>{o.index}</td>
                        <td>{format(o.price)}</td>
                        <td>
                          {format(o.quantity)}{' '}
                          {current.plan.snapshot.base_asset}
                        </td>
                        <td>
                          {o.offset_seconds === 0
                            ? 'At start'
                            : o.offset_seconds + ' seconds later'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <p>{current.plan.idea.rationale}</p>
              </details>
            </div>
          )}
          <details className="frontier-advanced">
            <summary>Chart data & advanced tools</summary>
            <div>
              <details
                className="planning-tools"
                onToggle={(e) => setExtrasOpen(e.currentTarget.open)}
              >
                <summary>Market data and indicators</summary>
                {extrasOpen && (
                  <PlanningDataPanel
                    exchangeID={exchangeID}
                    symbol={symbol}
                    venue={venue}
                    onWindowChange={setPluginWindow}
                  />
                )}
              </details>
              <details className="planning-tools">
                <summary>Risk budget, entry orders and advanced tools</summary>
                <div className="planning-limit-grid">
                  {(
                    Object.keys(defaultLimits) as Exclude<
                      keyof Constraints,
                      | 'leverage'
                      | 'available_margin'
                      | 'sizing_policy'
                      | 'planning_only'
                      | 'entry_policy'
                    >[]
                  )
                    .filter(
                      (key) =>
                        !exchangeID ||
                        !['equity', 'max_risk', 'max_notional'].includes(key)
                    )
                    .map((key) => (
                      <label key={key}>
                        {
                          {
                            equity: 'Equity budget',
                            max_risk: 'Maximum planned loss',
                            max_notional: 'Maximum notional',
                            min_reward_risk:
                              'Minimum net reward/risk (0 = no minimum)',
                            fee_bps: 'Fee assumption / bps',
                            slippage_bps: 'Slippage assumption / bps',
                          }[key]
                        }
                        <input
                          value={limits[key]}
                          inputMode="decimal"
                          disabled={
                            busy ||
                            (Boolean(exchangeID) &&
                              ['equity', 'max_risk', 'max_notional'].includes(
                                key
                              ))
                          }
                          onChange={(e) =>
                            setLimits({ ...limits, [key]: e.target.value })
                          }
                        />
                      </label>
                    ))}
                </div>
                <p>
                  Live sizing refreshes before planning and review. Leverage is
                  applied to current available margin, with execution costs
                  reserved. Quantities are rounded to exchange steps. Market
                  orders require enough depth within the approved price bound.
                  Hyperliquid costs must include Builder fees. Gaps, failed
                  protection and slippage may cause losses beyond the estimate.
                </p>
                <button disabled={busy} onClick={() => setBuilder(true)}>
                  Custom market / limit / ladder / TWAP
                </button>
                <label>
                  Discussion preferences
                  <textarea
                    value={preferences}
                    onChange={(e) => setPreferences(e.target.value)}
                    maxLength={1600}
                  />
                </label>
                <button
                  onClick={async () => {
                    try {
                      const r = await fetch('/api/planning/preferences', {
                        method: 'PUT',
                        headers: getAuthHeaders(),
                        body: JSON.stringify({ text: preferences }),
                      })
                      await handleJSONResponse(r)
                      setStatus(
                        'Preferences saved; hard risk constraints are unchanged'
                      )
                    } catch (e) {
                      setError(String(e))
                    }
                  }}
                >
                  Save preferences
                </button>
                <button
                  onClick={async () => {
                    try {
                      setTools(
                        await planningAPI<unknown[]>(
                          'entry-plugins?session_id=' + session.current
                        )
                      )
                    } catch (e) {
                      setError(String(e))
                    }
                  }}
                >
                  Inspect entry calculators
                </button>
                {tools.length > 0 && (
                  <pre>{JSON.stringify(tools, null, 2)}</pre>
                )}
              </details>
            </div>
          </details>
        </section>
        <aside className="planning-assistant" id="frontier-assistant">
          <div className="planning-assistant-title">
            <div>
              <span className="frontier-eyebrow">YOUR TRADING COPILOT</span>
              <b>Trade assistant</b>
            </div>
            <button onClick={newSession} disabled={busy}>
              New idea
            </button>
          </div>
          <details className="frontier-model-settings">
            <summary>
              {models.find((m) => m.id === modelID)?.customModelName ||
                models.find((m) => m.id === modelID)?.name ||
                'Choose AI'}{' '}
              <span>Change model</span>
            </summary>
            <select
              aria-label="NOFX AI model"
              value={modelID}
              onChange={(e) => setModelID(e.target.value)}
              disabled={busy}
            >
              {models.length === 0 && (
                <option>Enable AI in NOFX settings first</option>
              )}
              {models.map((m) => (
                <option value={m.id} key={m.id}>
                  {m.name} · {m.customModelName || m.provider}
                </option>
              ))}
            </select>
          </details>
          <div className="frontier-assistant-action">
            <button
              className="planning-primary"
              disabled={busy}
              onClick={() =>
                send(
                  instruction.trim() ||
                    'Compare four plans in this order: market entry, single limit, weighted ladder and TWAP for this market.',
                  true
                )
              }
            >
              Generate plans
            </button>
          </div>
          <a
            className="frontier-plans-link"
            href="#frontier-plans"
            onClick={(event) => {
              event.preventDefault()
              showSection('plans')
            }}
          >
            {waitingForEntry ? 'View entry conditions' : 'View entry plans'}{' '}
            <span>{waitingForEntry ? '→' : `${deckPlans.length} →`}</span>
          </a>
          {busy && (
            <div className="planning-progress" role="status" aria-live="polite">
              <span className="planning-spinner" />
              {planningBusy
                ? status || 'Preparing plans…'
                : 'Validating account and order details…'}
              {planningBusy && (
                <button onClick={() => abort.current?.abort()}>Stop</button>
              )}
            </div>
          )}
          <div
            className="planning-thread"
            ref={chatBox}
            onScroll={() => {
              const box = chatBox.current
              if (box)
                chatFollow.current =
                  box.scrollHeight - box.scrollTop - box.clientHeight < 80
            }}
          >
            <div className="planning-messages" id="frontier-chat-panel">
              {messages.length === 0 && !current && (
                <div className="frontier-welcome">
                  <span className="frontier-welcome-icon" aria-hidden="true">
                    <img src="/icons/nofx-flow.svg" alt="" />
                  </span>
                  <h2>
                    Your next move,
                    <br />
                    made clear.
                  </h2>
                  <p>
                    Tell me what you’re considering. I’ll map out entries, risk
                    and exits for you to review.
                  </p>
                  <div className="frontier-welcome-note">
                    <span>01 Explore</span>
                    <span>02 Compare</span>
                    <span>03 Review & trade</span>
                  </div>
                  <div
                    className="frontier-starters"
                    aria-label="Trade idea suggestions"
                  >
                    <button
                      onClick={() =>
                        setInstruction(
                          'Find a pullback entry with clear risk and exits.'
                        )
                      }
                    >
                      Find a pullback entry <span>↗</span>
                    </button>
                    <button
                      onClick={() =>
                        setInstruction(
                          'Compare market, single-limit, price-ladder and TWAP entries for this market.'
                        )
                      }
                    >
                      Compare ways to enter <span>↗</span>
                    </button>
                    <button
                      onClick={() =>
                        setInstruction(
                          'Mark support and resistance, without creating orders.'
                        )
                      }
                    >
                      Explore key price levels <span>↗</span>
                    </button>
                  </div>
                </div>
              )}
              {messages.length === 0 && current && (
                <p className="frontier-plan-hint">
                  This plan is on the chart. Ask me to explain or revise it,
                  then review the order when you are ready.
                </p>
              )}
              {messages
                .filter(
                  (m, i) =>
                    m.role !== 'user' ||
                    i === 0 ||
                    messages[i - 1].role !== 'user' ||
                    messages[i - 1].content !== m.content
                )
                .map((m, i) => (
                  <div key={i} className={'planning-message ' + m.role}>
                    {m.content}
                  </div>
                ))}
            </div>
            <div>
              <PlanningActivity items={trace} busy={planningBusy} />
            </div>
          </div>

          <form
            className="planning-composer"
            onSubmit={(e) => {
              e.preventDefault()
              send()
            }}
          >
            <textarea
              aria-label="Trade idea"
              value={instruction}
              onChange={(e) => setInstruction(e.target.value)}
              placeholder="Ask about this market, or describe a trade…"
              maxLength={8000}
              disabled={busy}
            />
            <div>
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  send(
                    'Mark potential support and resistance on the current chart. Explain assumptions without generating trade plans yet.'
                  )
                }
              >
                Support / resistance
              </button>
              {planningBusy ? (
                <button type="button" onClick={() => abort.current?.abort()}>
                  Stop planning
                </button>
              ) : (
                <button type="submit" disabled={busy || !instruction.trim()}>
                  Send
                </button>
              )}
            </div>
          </form>
          <div className="planning-review">
            {current ? (
              <>
                <b>{current.plan.idea.name}</b>
                <p>
                  {states[run?.state || current.state] || current.state} · Live
                  trading ·{' '}
                  {accounts.find((a) => a.id === data.accounts[selected])?.name}
                </p>
                {run?.error && <p className="stop-text">{run.error}</p>}
                {current.state === 'draft' ? (
                  <button
                    className="planning-primary"
                    disabled={
                      busy ||
                      (!!exchangeID && data.accounts[selected] !== exchangeID)
                    }
                    onClick={() => {
                      if (!exchangeID) requestReview(current.plan)
                      else void reviewWithAccount(current.plan)
                    }}
                  >
                    {!exchangeID
                      ? 'Connect account to trade'
                      : expired(current.plan, clock)
                        ? 'Refresh & review order'
                        : 'Review order'}
                  </button>
                ) : run?.state === 'approved' ? (
                  <>
                    <button
                      disabled={busy}
                      onClick={() => runAction('execute', run)}
                    >
                      Place approved orders
                    </button>
                    <button
                      disabled={busy}
                      onClick={() => runAction('stop', run)}
                    >
                      Revoke approval
                    </button>
                  </>
                ) : (
                  run &&
                  !['completed', 'cancelled', 'expired'].includes(
                    run.state
                  ) && (
                    <button
                      disabled={busy}
                      onClick={() => runAction('stop', run)}
                    >
                      Stop remaining entries
                    </button>
                  )
                )}
                <small>
                  AI Revisions are not approval. Unknown orders are not
                  resubmitted. Check live positions immediately if protection
                  fails.
                </small>
              </>
            ) : (
              <p className="frontier-no-orders">
                <span aria-hidden="true">○</span> Plans are drafts. You approve
                every trade.
              </p>
            )}
          </div>
          <details className="planning-history">
            <summary>Planning conversations {history.length}</summary>
            {history.map((h) => (
              <button
                key={h.id}
                disabled={busy}
                onClick={() => {
                  session.current = h.id
                  changeAccount(h.exchange_id)
                  session.current = h.id
                  setSymbol(h.symbol)
                  setSymbolInput(h.symbol)
                  setModelID(h.model_id)
                  setMessages(JSON.parse(h.messages))
                  setSelected('')
                  resetTurn()
                }}
              >
                {h.symbol} · {h.id.slice(0, 8)}
              </button>
            ))}
          </details>
        </aside>
      </div>
      <div className="frontier-portfolio" id="frontier-account">
        <FrontierAccount
          account={account}
          accounts={accounts}
          venue={venue}
          symbol={symbol}
          runs={data.runs}
          busy={busy}
          onView={viewTrade}
          onManual={manualEntry}
          onBusy={setBusy}
          onRefresh={reload}
          onObserve={setObservation}
          onCheck={checkPreviousTrade}
        />
        <details className="planning-tools">
          <summary>
            Previous executions ·{' '}
            {
              data.runs.filter(
                (r) =>
                  r.exchange_id === exchangeID &&
                  ['completed', 'cancelled', 'expired'].includes(r.state)
              ).length
            }
          </summary>
          {data.runs
            .filter(
              (r) =>
                r.exchange_id === exchangeID &&
                ['completed', 'cancelled', 'expired'].includes(r.state)
            )
            .map((r) => (
              <div className="planning-run" key={r.id}>
                <b>
                  {r.plan.idea.name} · {states[r.state] || r.state}
                </b>
                {r.error && <p className="stop-text">{r.error}</p>}
                <table>
                  <thead>
                    <tr>
                      <th>Type</th>
                      <th>Native order ID</th>
                      <th>State</th>
                      <th>Filled quantity</th>
                    </tr>
                  </thead>
                  <tbody>
                    {r.orders.map((o) => (
                      <tr key={o.client_id}>
                        <td>{o.role}</td>
                        <td>{o.order_id || 'Unconfirmed'}</td>
                        <td>{orderStates[o.status] || o.status}</td>
                        <td>{format(o.filled)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ))}
        </details>{' '}
      </div>
      {review && (
        <div className="planning-modal-backdrop">
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby="planning-approval-title"
            className="planning-modal"
          >
            <h2 id="planning-approval-title">
              Place {review.idea.side === 'long' ? 'Long' : 'Short'} ·{' '}
              {review.snapshot.symbol}
            </h2>
            <p>
              {account?.name} · {review.snapshot.venue} ·{' '}
              {review.snapshot.symbol} ·{' '}
              {review.idea.side === 'long' ? 'Long' : 'Short'}
            </p>
            <div className="planning-price-row">
              <span>
                {review.idea.execution.kind === 'market'
                  ? 'Est. market entry'
                  : 'Entry'}{' '}
                {format(review.idea.entry)}
              </span>
              <span>Stop loss {format(review.idea.stop)}</span>
              <span>Target {format(review.idea.target)}</span>
            </div>
            <p>
              Total quantity {format(review.quantity)} · Planned loss{' '}
              {format(review.risk)} {review.snapshot.quote_asset} ·{' '}
              {review.orders.length || 1} child orders ·{' '}
              {review.constraints.leverage || 1}x leverage
            </p>
            <PlanFunding plan={review} />
            {reviewReplacement && (
              <p role="note">
                Confirming this order replaces your earlier unsubmitted{' '}
                {reviewReplacement.plan.snapshot.symbol} approval. Existing
                exchange orders and positions are unchanged by the replacement.
              </p>
            )}
            <p>
              Quantity is locked at this review; balance changes require a new
              review.
            </p>
            <details>
              <summary>Execution and protection details</summary>
              <p>
                {review.idea.execution.kind === 'market'
                  ? 'Market entry is sent immediately after approval. Binance, OKX and Bitget use market orders; Hyperliquid uses an IOC order within the approved slippage bound. Actual fill may differ and any unfilled IOC quantity is canceled. '
                  : 'Only the locked, fixed-limit basket will be submitted. '}
                Ladder entries are released immediately; TWAP slices follow the
                schedule. Partial fills receive matching reduce-only protection.
                Protection failures stop further entries and require manual
                review. Stopping entries does not close filled positions. Actual
                loss can exceed the estimate.
              </p>
            </details>
            <table>
              <thead>
                <tr>
                  <th>
                    {review.idea.execution.kind === 'market'
                      ? 'Execution estimate'
                      : 'Limit price'}
                  </th>
                  <th>Quantity</th>
                  <th>Release time</th>
                </tr>
              </thead>
              <tbody>
                {review.orders.map((o) => (
                  <tr key={o.index}>
                    <td>{format(o.price)}</td>
                    <td>{format(o.quantity)}</td>
                    <td>{o.offset_seconds} seconds</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <details>
              <summary>Locked order reference</summary>
              <code>{review.hash}</code>
            </details>
            <div>
              <button disabled={busy} onClick={() => setReview(null)}>
                Back to review
              </button>
              <button
                className="planning-primary"
                disabled={busy || expired(review, clock)}
                onClick={approveAndExecute}
              >
                Confirm and place live order
              </button>
            </div>
          </section>
        </div>
      )}
      {builder && (
        <div className="planning-modal-backdrop">
          <section role="dialog" aria-modal="true" className="planning-modal">
            <h2>Manual entry · {symbol}</h2>
            <p>
              {account?.name} · {venue} · {leverage}x · automatically sized from
              free margin. Review before submitting.
            </p>
            <label>
              Leverage
              <select
                aria-label="Manual leverage"
                value={leverage}
                onChange={(e) => setLeverage(Number(e.target.value))}
              >
                {[1, 2, 3, 5, 10, 20].map((value) => (
                  <option key={value} value={value}>
                    {value}x
                  </option>
                ))}
              </select>
            </label>
            <select
              aria-label="Custom entry method"
              value={builderIdea.execution.kind}
              onChange={(e) =>
                setBuilderIdea({
                  ...builderIdea,
                  execution: {
                    kind: e.target.value as Idea['execution']['kind'],
                    ...(e.target.value === 'twap'
                      ? { slices: 4, interval_seconds: 30 }
                      : {}),
                  },
                })
              }
            >
              <option value="market">Market</option>
              <option value="limit">Single limit</option>
              <option value="ladder">Price ladder</option>
              <option value="twap">TWAP fixed limit</option>
            </select>
            <select
              aria-label="Custom direction"
              value={builderIdea.side}
              onChange={(e) =>
                setBuilderIdea({
                  ...builderIdea,
                  side: e.target.value as Idea['side'],
                })
              }
            >
              <option value="long">Long</option>
              <option value="short">Short</option>
            </select>
            {builderIdea.execution.kind === 'market' && (
              <p>
                Entry uses the current market quote when you create the draft.
                Actual fill may differ.
              </p>
            )}
            {(['entry', 'stop', 'target'] as const)
              .filter(
                (k) => k !== 'entry' || builderIdea.execution.kind !== 'market'
              )
              .map((k) => (
                <label key={k}>
                  {
                    {
                      name: 'Name',
                      entry: 'Limit price',
                      stop: 'Stop loss',
                      target: 'Take profit',
                    }[k]
                  }
                  <input
                    value={builderIdea[k]}
                    onChange={(e) =>
                      setBuilderIdea({ ...builderIdea, [k]: e.target.value })
                    }
                  />
                </label>
              ))}
            {builderIdea.execution.kind === 'ladder' && (
              <label>
                Enter one price and quantity weight per line; weights must sum
                to 1
                <textarea
                  placeholder={'82000 0.3\n81500 0.3\n81000 0.4'}
                  value={levels}
                  onChange={(e) => setLevels(e.target.value)}
                />
              </label>
            )}
            {builderIdea.execution.kind === 'twap' && (
              <>
                <label>
                  Slices
                  <input
                    value={builderIdea.execution.slices}
                    onChange={(e) =>
                      setBuilderIdea({
                        ...builderIdea,
                        execution: {
                          ...builderIdea.execution,
                          slices: Number(e.target.value),
                        },
                      })
                    }
                  />
                </label>
                <label>
                  Interval / seconds
                  <input
                    value={builderIdea.execution.interval_seconds}
                    onChange={(e) =>
                      setBuilderIdea({
                        ...builderIdea,
                        execution: {
                          ...builderIdea.execution,
                          interval_seconds: Number(e.target.value),
                        },
                      })
                    }
                  />
                </label>
              </>
            )}
            <p>This creates an unapproved draft only. No orders are sent.</p>
            <button disabled={busy} onClick={() => setBuilder(false)}>
              Cancel
            </button>
            <button disabled={busy} onClick={saveBuilder}>
              Review order
            </button>
          </section>
        </div>
      )}
    </div>
  )
}
