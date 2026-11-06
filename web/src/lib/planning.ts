import { getAuthHeaders, handleJSONResponse } from './api/helpers'
export interface Plan {
  id: string
  hash: string
  expires: string
  created: string
  quantity: string
  sizing_limit?: 'budget' | 'risk' | 'liquidity'
  risk: string
  reward: string
  reward_risk: string
  snapshot: {
    venue: string
    symbol: string
    kind: string
    tick: string
    step: string
    quote_asset: string
    base_asset: string
  }
  constraints: Constraints
  idea: Idea
  orders: {
    index: number
    price: string
    quantity: string
    offset_seconds: number
  }[]
}
export interface Idea {
  name: string
  side: 'long' | 'short'
  entry: string
  stop: string
  target: string
  rationale: string
  execution: {
    kind: 'market' | 'limit' | 'ladder' | 'twap'
    levels?: { price: string; weight: string }[]
    slices?: number
    interval_seconds?: number
  }
}
export interface Constraints {
  entry_policy?: 'ai500_trend_long'
  planning_only?: boolean
  leverage?: number
  available_margin?: string
  sizing_policy?: 'account_margin'
  equity: string
  max_risk: string
  max_notional: string
  min_reward_risk: string
  fee_bps: string
  slippage_bps: string
}
export interface LiveOrder {
  client_id: string
  order_id: string
  role: string
  price: string
  quantity: string
  filled: string
  status: string
  error?: string
  average?: string
}
export interface LiveRun {
  id: string
  exchange_id: string
  plan_id: string
  plan_hash: string
  approval_hash: string
  updated_at?: string
  state: string
  error?: string
  plan: Plan
  orders: LiveOrder[]
}
export interface PlanningState {
  records: Record<string, { plan: Plan; state: string }>
  accounts: Record<string, string>
  runs: LiveRun[]
  mode: 'live'
  now: string
}
export interface Drawing {
  kind: string
  label: string
  points: { time: number; price: string }[]
}
export interface ChartContext {
  venue: string
  symbol: string
  interval: string
  resolution: string
  visible_from?: number
  visible_to?: number
}
export interface PlanningChart {
  context(): ChartContext
  whenReady(): Promise<void>
  setContext(venue: string, symbol: string, resolution?: string): Promise<void>
  plot(p: Plan | null, phase?: string, compare?: Plan[]): Promise<void>
  focusPlan(): Promise<void>
  draw(drawings: Drawing[], source: string, interval: string): Promise<void>
  dispose(): void
}
declare global {
  interface Window {
    WorkspaceChart: new (options: Record<string, unknown>) => PlanningChart
  }
}
export const defaultLimits: Constraints = {
  equity: '1000',
  max_risk: '10',
  max_notional: '100',
  min_reward_risk: '0',
  fee_bps: '10',
  slippage_bps: '10',
}
export class CloseNotSubmittedError extends Error {
  readonly closeState = 'not_submitted'
}

export async function planningAPI<T>(path: string, body?: unknown): Promise<T> {
  const action = path.split('?')[0]
  const tradeAction = [
    'approve',
    'execute',
    'stop',
    'reconcile',
    'close-position',
  ].includes(action)
  const paidData = ['ai500', 'plugin-data'].includes(action)
  const controller = new AbortController()
  const timer = setTimeout(
    () => controller.abort(),
    tradeAction ? 45000 : 35000
  )
  try {
    const r = await fetch('/api/planning/' + path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: getAuthHeaders(),
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    })
    if (action === 'close-position' && !r.ok) {
      const data = await r.json().catch(() => null)
      if (data?.close_state === 'not_submitted')
        throw new CloseNotSubmittedError(data.error || 'No close was sent.')
      throw new Error(
        data?.error ||
          'Close result is unconfirmed. Check the exchange before retrying.'
      )
    }
    return await handleJSONResponse<T>(r)
  } catch (error) {
    if (controller.signal.aborted) {
      if (tradeAction)
        throw new Error(
          'Order request timed out. Its result is unconfirmed. No automatic retry was made. Check Positions & orders and verify trade status before sending another request.'
        )
      if (paidData)
        throw new Error(
          'AI/data request timed out. Payment result is unconfirmed. No automatic retry; check the original payment before making another request.'
        )
      if (action === 'check-status')
        throw new Error(
          'Trade status check timed out. The previous trade remains unverified. No new order was submitted by this check.'
        )
      throw new Error(
        'Request timed out. Check the account or service status before trying again. No automatic retry was made.'
      )
    }
    throw error
  } finally {
    clearTimeout(timer)
  }
}
export interface PlanningEvent {
  type: string
  data: Record<string, unknown>
}
export async function streamPlanning(
  body: unknown,
  signal: AbortSignal,
  onEvent: (e: PlanningEvent) => void
): Promise<void> {
  const r = await fetch('/api/planning/chat', {
    method: 'POST',
    headers: getAuthHeaders(),
    body: JSON.stringify(body),
    signal,
  })
  if (!r.ok) {
    await handleJSONResponse(r)
    return
  }
  if (!r.body) throw new Error('Server returned no planning stream')
  const reader = r.body.getReader(),
    decoder = new TextDecoder()
  let buffer = ''
  let finished = false
  let serverError = false
  try {
    for (;;) {
      const { done, value } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      buffer = buffer.replace(/\r\n/g, '\n')
      let at: number
      while ((at = buffer.indexOf('\n\n')) >= 0) {
        const frame = buffer.slice(0, at)
        buffer = buffer.slice(at + 2)
        for (const line of frame.split('\n'))
          if (line.startsWith('data: ')) {
            const event = JSON.parse(line.slice(6)) as PlanningEvent
            if (event.type === 'done' || event.type === 'outcome')
              finished = true
            if (event.type === 'error') serverError = true
            onEvent(event)
          }
      }
      if (buffer.length > 1 << 20)
        throw new Error('Planning event exceeds the size limit')
      if (done) {
        if (!finished && !serverError)
          throw new Error(
            'Planning connection ended without a result. No automatic retry was made; check payment status before retrying.'
          )
        break
      }
    }
  } finally {
    await reader.cancel().catch(() => {})
  }
}
const scripts = new Map<string, Promise<void>>()
export function loadPlanningChart(): Promise<void> {
  const load = (url: string) => {
    let promise = scripts.get(url)
    if (!promise) {
      promise = new Promise<void>((resolve, reject) => {
        const script = document.createElement('script')
        script.src = url
        script.onload = () => resolve()
        script.onerror = () => {
          scripts.delete(url)
          script.remove()
          reject(
            new Error(
              'Chart failed to load. Check that the charting submodule was initialized during the build.'
            )
          )
        }
        document.head.append(script)
      })
      scripts.set(url, promise)
    }
    return promise
  }
  return load('/charting_library/charting_library.standalone.js').then(() =>
    load('/planning/chart.js?v=' + __PLANNING_CHART_VERSION__)
  )
}
export function expired(p: Plan, clock = Date.now()) {
  return Date.parse(p.expires) <= clock
}
