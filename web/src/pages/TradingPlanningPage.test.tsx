import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
  within,
} from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest'
import { TradingPlanningPage } from './TradingPlanningPage'
const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  stream: vi.fn(),
  exchangesGate: undefined as Promise<void> | undefined,
  models: [] as {
    id: string
    name: string
    provider: string
    enabled: boolean
    has_api_key: boolean
  }[],
  exchanges: [] as {
    id: string
    name: string
    account_name?: string
    exchange_type: string
    enabled: boolean
  }[],
  chartOptions: undefined as
    | undefined
    | {
        onError: (message: string) => void
        onAvailability: (available: boolean) => void
      },
  plot: vi.fn().mockResolvedValue(undefined),
  focus: vi.fn().mockResolvedValue(undefined),
}))
vi.mock('../lib/api', () => ({
  api: {
    getExchangeConfigs: async () => {
      await mocks.exchangesGate
      return mocks.exchanges
    },
    getTraders: async () => [],
    getModelConfigs: async () => mocks.models,
  },
}))
vi.mock('../lib/planning', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  planningAPI: mocks.api,
  streamPlanning: mocks.stream,
  loadPlanningChart: async () => {
    window.WorkspaceChart = class {
      constructor(options: NonNullable<typeof mocks.chartOptions>) {
        mocks.chartOptions = options
      }
      context() {
        return {
          venue: 'binance',
          symbol: 'BTCUSDT',
          interval: '1h',
          resolution: '60',
        }
      }
      async whenReady() {}
      async setContext() {}
      plot = mocks.plot
      focusPlan = mocks.focus
      async draw() {}
      dispose() {}
    }
  },
}))
const plan = (id: string) => ({
  id,
  hash: id.repeat(4),
  created: new Date().toISOString(),
  expires: new Date(Date.now() + 300000).toISOString(),
  quantity: '0.01',
  risk: '10',
  reward: '25',
  reward_risk: '2.5',
  constraints: { min_reward_risk: '2' },
  snapshot: {
    venue: 'binance',
    symbol: 'BTCUSDT',
    kind: 'perpetual',
    base_asset: 'BTC',
    quote_asset: 'USDT',
  },
  idea: {
    name: 'Test plan ' + id,
    side: 'long',
    entry: '80000',
    stop: '79000',
    target: '83000',
    rationale: 'Conditional hypothesis',
    execution: { kind: id === 'two' ? 'limit' : 'market' },
  },
  orders: [{ index: 1, price: '80000', quantity: '0.01', offset_seconds: 0 }],
})
beforeEach(() => {
  localStorage.clear()
  localStorage.setItem(
    'frontier-batch-v1//ex/binance/BTCUSDT',
    JSON.stringify({
      id: 'test-turn',
      ids: ['one', 'two'],
      created: new Date().toISOString(),
    })
  )
  Element.prototype.scrollIntoView = vi.fn()
  mocks.models = [
    {
      id: 'model',
      name: 'Configured DeepSeek',
      provider: 'deepseek',
      enabled: true,
      has_api_key: true,
    },
  ]
  mocks.exchangesGate = undefined
  mocks.exchanges = [
    {
      id: 'ex',
      name: 'Connected account',
      exchange_type: 'binance',
      enabled: true,
    },
  ]
  mocks.api.mockReset()
  mocks.plot.mockClear()
  mocks.stream
    .mockReset()
    .mockRejectedValue(new Error('Account sizing unavailable'))
  mocks.api.mockImplementation(async (path: string) => {
    if (path === 'state')
      return {
        records: {
          one: { plan: plan('one'), state: 'draft' },
          two: { plan: plan('two'), state: 'draft' },
        },
        accounts: { one: 'ex', two: 'ex' },
        runs: [],
        mode: 'live',
        now: new Date().toISOString(),
      }
    if (path.startsWith('account?'))
      return { equity: '1000', available: '1000', fee_bps: '5' }
    if (path.startsWith('setup-wallet'))
      return {
        found: true,
        enabled: true,
        address: '0x1111111111111111111111111111111111111111',
        model_id: 'model',
        chain: 'base',
        asset: 'USDC',
        balance_usdc: '1',
        balance_status: 'ok',
      }
    if (path === 'ai500')
      return {
        coins: [{ pair: 'ETHUSDT', score: 85 }],
        source: 'Claw402 fixture',
        fetched_at: new Date().toISOString(),
      }
    if (path === 'preferences') return { text: '' }
    if (path === 'conversations') return []
    if (path === 'compile')
      return { ...plan('resized'), constraints: { leverage: 5 } }
    if (path === 'approve') return { id: 'run', approval_hash: 'locked' }
    return {}
  })
})
afterEach(cleanup)
function open() {
  return render(
    <MemoryRouter>
      <TradingPlanningPage />
    </MemoryRouter>
  )
}
describe('NOFX live planning', () => {
  it('refreshes account sizing and executes only after the explicit review click', async () => {
    open()
    const card = await screen.findByRole('button', { name: /Test plan one/ })
    fireEvent.click(card)
    await waitFor(() =>
      expect(screen.getByLabelText('NOFX AI model')).toHaveValue('model')
    )
    fireEvent.click(screen.getByRole('button', { name: 'Review order' }))
    const execute = await screen.findByRole('button', {
      name: 'Confirm and place live order',
    })
    expect(execute).toBeEnabled()
    expect(screen.queryByLabelText('Live trade confirmation text')).toBeNull()
    expect(mocks.api).toHaveBeenCalledWith(
      'compile',
      expect.objectContaining({
        auto_size: true,
        exchange_id: 'ex',
        constraints: expect.objectContaining({ min_reward_risk: '0' }),
      })
    )
    expect(
      mocks.api.mock.calls.some(([p]) => p === 'approve' || p === 'execute')
    ).toBe(false)
    fireEvent.click(execute)
    await waitFor(() =>
      expect(mocks.api).toHaveBeenCalledWith('execute', {
        id: 'run',
        hash: 'locked',
      })
    )
    expect(mocks.api).toHaveBeenCalledWith('approve', {
      plan_id: 'resized',
      hash: 'resized'.repeat(4),
      confirmed: true,
    })
  })
  it('selecting a plan exits comparison and displays only that plan on the chart', async () => {
    open()
    await screen.findByRole('button', { name: /Test plan one/ })
    fireEvent.click(
      screen.getByRole('button', { name: 'Compare plans', exact: true })
    )
    await waitFor(() =>
      expect(
        mocks.plot.mock.calls.some(([, , list]) => list?.length === 2)
      ).toBe(true)
    )
    fireEvent.click(screen.getByRole('button', { name: /Test plan two/ }))
    await waitFor(() => {
      const args = mocks.plot.mock.calls.at(-1)!
      expect(args[0].id).toBe('two')
      expect(args[2]).toEqual([])
    })
  })
  it('custom entry mode does not attach TWAP parameters to a ladder', async () => {
    open()
    await screen.findByRole('button', { name: /Test plan one/ })
    fireEvent.click(screen.getByText('Chart data & advanced tools'))
    fireEvent.click(
      screen.getByText('Risk budget, entry orders and advanced tools')
    )
    fireEvent.click(
      screen.getByRole('button', {
        name: 'Custom market / limit / ladder / TWAP',
      })
    )
    fireEvent.change(screen.getByLabelText('Custom entry method'), {
      target: { value: 'ladder' },
    })
    expect(screen.queryByLabelText('Slices')).toBeNull()
    expect(
      screen.getByText(/Enter one price and quantity weight per line/)
    ).toBeInTheDocument()
  })
})

it('opens the only connected account by default and keeps Binance AI500 reachable', async () => {
  mocks.exchanges = [
    {
      id: 'hl',
      name: 'Hyperliquid wallet',
      exchange_type: 'hyperliquid',
      enabled: true,
    },
  ]
  open()
  await waitFor(() =>
    expect(screen.getByLabelText('Trading account')).toHaveValue('hl')
  )
  expect(screen.getByLabelText('Market venue')).toHaveValue('hyperliquid')
  fireEvent.change(screen.getByLabelText('Market venue'), {
    target: { value: 'binance' },
  })
  expect(screen.getByLabelText('Quick market selection')).toBeVisible()
  await screen.findByRole('button', { name: /ETH/ })
  expect(screen.getByLabelText('Market venue')).toHaveValue('binance')
  expect(screen.getByLabelText('Trading account')).toHaveValue('')
  expect(
    screen.getByRole('region', { name: 'Your connections' })
  ).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /ETH/ }))
  expect(screen.getByLabelText('Symbol')).toHaveValue('ETHUSDT')
  fireEvent.change(screen.getByLabelText('Market venue'), {
    target: { value: 'hyperliquid' },
  })
  expect(screen.getByLabelText('Trading account')).toHaveValue('hl')
  expect(screen.getByLabelText('Symbol')).toHaveValue('BTC')
  expect(screen.queryByLabelText('Binance AI500 markets')).toBeNull()
})

it('clears a chart failure after recovery without clearing unrelated errors', async () => {
  render(
    <MemoryRouter>
      <TradingPlanningPage />
    </MemoryRouter>
  )
  await waitFor(() => expect(mocks.chartOptions).toBeDefined())
  const { act } = await import('@testing-library/react')
  act(() => mocks.chartOptions!.onError('Market chart timed out.'))
  expect(screen.getByText('Market chart timed out.')).toBeInTheDocument()
  act(() => mocks.chartOptions!.onAvailability(true))
  expect(screen.queryByText('Market chart timed out.')).not.toBeInTheDocument()
})

it('keeps a venue chosen while account configuration is still loading', async () => {
  let release!: () => void
  mocks.exchanges = [
    {
      id: 'hl',
      name: 'Hyperliquid wallet',
      exchange_type: 'hyperliquid',
      enabled: true,
    },
  ]
  mocks.exchangesGate = new Promise<void>((resolve) => {
    release = resolve
  })
  open()
  fireEvent.change(screen.getByLabelText('Market venue'), {
    target: { value: 'hyperliquid' },
  })
  release()
  await waitFor(() =>
    expect(screen.getByLabelText('Trading account')).toHaveValue('hl')
  )
  expect(screen.getByLabelText('Symbol')).toHaveValue('BTC')
  expect(screen.getByLabelText('Market venue')).toHaveValue('hyperliquid')
})

it('preflight failure clears busy state without accumulating phantom user messages', async () => {
  open()
  await screen.findByRole('button', { name: /Test plan one/ })
  const generate = screen.getByRole('button', { name: 'Generate plans' })
  for (let attempt = 0; attempt < 2; attempt++) {
    fireEvent.click(generate)
    await waitFor(() => expect(mocks.stream).toHaveBeenCalledTimes(attempt + 1))
    await waitFor(() => expect(generate).toBeEnabled())
  }
  expect(
    screen.queryByText(
      'Compare single-limit, weighted ladder and TWAP plans for this market.'
    )
  ).toBeNull()
  expect(screen.getAllByText('Account sizing unavailable')).toHaveLength(1)
  expect(screen.getByLabelText('Trade idea')).toBeEnabled()
  expect(
    JSON.parse(localStorage.getItem('frontier-batch-v1//ex/binance/BTCUSDT')!)
      .ids
  ).toEqual(['one', 'two'])
})

it('account review does not restart model activity or show a planning stop button', async () => {
  let finish!: (value: unknown) => void
  const pending = new Promise((resolve) => {
    finish = resolve
  })
  const base = mocks.api.getMockImplementation()!
  mocks.api.mockImplementation((path: string, body?: unknown) =>
    path === 'compile' ? pending : base(path, body)
  )
  open()
  fireEvent.click(await screen.findByRole('button', { name: /Test plan one/ }))
  fireEvent.click(screen.getByRole('button', { name: 'Review order' }))
  await screen.findByText('Validating account and order details…')
  expect(screen.queryByText('Live activity')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Stop planning' })).toBeNull()
  finish({ ...plan('resized'), constraints: { leverage: 5 } })
  await screen.findByRole('dialog')
})

it.each([true, false])(
  'archives the previous batch only for an explicit wait decision (%s)',
  async (waiting) => {
    mocks.stream.mockImplementation(async (_input, _signal, emit) => {
      if (waiting) emit({ type: 'plans', data: { plans: [], waiting: true } })
      emit({
        type: 'assistant',
        data: {
          message: waiting
            ? 'Wait for a confirmed uptrend.'
            : 'Here is the market context.',
        },
      })
      emit({ type: 'outcome', data: { drafts: 0, stop_reason: 'completed' } })
    })
    open()
    await screen.findByRole('button', { name: /Test plan one/ })
    const generate = screen.getByRole('button', { name: 'Generate plans' })
    fireEvent.click(generate)
    await waitFor(() => expect(mocks.stream).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(generate).toBeEnabled())
    expect(
      JSON.parse(localStorage.getItem('frontier-batch-v1//ex/binance/BTCUSDT')!)
        .ids
    ).toEqual(waiting ? [] : ['one', 'two'])
    if (waiting) {
      expect(screen.getByText('Plan history · 2')).toBeInTheDocument()
      expect(
        screen.getByRole('heading', { name: 'Waiting for an entry' })
      ).toBeInTheDocument()
    }
  }
)

it('includes the AI500 policy only when the current Binance market is on the board', async () => {
  const base = mocks.api.getMockImplementation()!
  mocks.api.mockImplementation((path: string) =>
    path === 'ai500'
      ? Promise.resolve({
          coins: [{ pair: 'BTCUSDT', score: 85 }],
          fetched_at: new Date().toISOString(),
        })
      : base(path)
  )
  open()
  await screen.findByRole('button', { name: /BTC 85/ })
  fireEvent.click(screen.getByRole('button', { name: 'Generate plans' }))
  await waitFor(() => expect(mocks.stream).toHaveBeenCalledTimes(1))
  expect(mocks.stream.mock.calls[0][0]).toMatchObject({
    market_group: 'ai500',
    constraints: { entry_policy: 'ai500_trend_long' },
  })
})

it('moves older AI500 short drafts to history without offering review', async () => {
  const base = mocks.api.getMockImplementation()!
  mocks.api.mockImplementation(async (path: string) => {
    if (path === 'ai500')
      return {
        coins: [{ pair: 'BTCUSDT', score: 85 }],
        fetched_at: new Date().toISOString(),
      }
    const result = await base(path)
    if (path === 'state') result.records.one.plan.idea.side = 'short'
    return result
  })
  open()
  await screen.findByRole('button', { name: /BTC 85/ })
  expect(screen.getByRole('button', { name: /Test plan one/ })).toBeDisabled()
  expect(
    screen.getByText('Outside AI500 long-only policy · generate a new plan')
  ).toBeInTheDocument()
  expect(screen.getByText('Plan history · 1')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Review Market' })).toBeNull()
})

it('restores the reasons for waiting after the page is reloaded', async () => {
  const deferred = ['market', 'limit', 'ladder', 'twap'].map((kind) => ({
    kind,
    reason: `${kind}: waiting for a supported entry.`,
  }))
  localStorage.setItem(
    'frontier-batch-v1//ex/binance/BTCUSDT',
    JSON.stringify({
      id: 'wait-turn',
      ids: [],
      created: new Date().toISOString(),
      waiting: true,
      deferred,
    })
  )
  open()
  await screen.findByRole('heading', { name: 'Waiting for an entry' })
  expect(screen.getAllByText('Wait for entry')).toHaveLength(4)
  for (const entry of deferred)
    expect(screen.getByText(entry.reason)).toBeInTheDocument()
  expect(
    screen.getByRole('link', { name: 'View entry conditions →' })
  ).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Review Market' })).toBeNull()
})

it('opens setup before a paid request and retains the original idea when AI is missing', async () => {
  mocks.models = []
  open()
  await screen.findByRole('region', { name: 'Your connections' })
  const idea = screen.getByRole('textbox', { name: 'Trade idea' })
  fireEvent.change(idea, {
    target: { value: 'Compare a ladder and TWAP for BTC' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(
    await screen.findByRole('dialog', { name: 'Set up your AI' })
  ).toBeVisible()
  expect(idea).toHaveValue('Compare a ladder and TWAP for BTC')
  expect(mocks.stream).not.toHaveBeenCalled()
})

it('offers a safe starting point, keeps real account activity separate and keeps plans visible outside the assistant', async () => {
  open()
  await waitFor(() =>
    expect(screen.getByLabelText('NOFX AI model')).toHaveValue('model')
  )
  const positions = screen.getByRole('region', {
    name: 'Account positions and orders',
  })
  expect(positions.closest('.planning-workspace')).toBeNull()
  expect(
    screen.getByText('Chart data & advanced tools').closest('details')
  ).not.toHaveAttribute('open')
  fireEvent.click(screen.getByRole('button', { name: /Find a pullback entry/ }))
  expect(screen.getByLabelText('Trade idea')).toHaveValue(
    'Find a pullback entry with clear risk and exits.'
  )
  expect(mocks.stream).not.toHaveBeenCalled()
  expect(
    mocks.api.mock.calls.some(
      ([path]) => path === 'approve' || path === 'execute'
    )
  ).toBe(false)
  const deck = screen.getByRole('region', { name: 'Entry plans' })
  expect(deck.closest('.planning-assistant')).toBeNull()
  expect(
    screen.getByRole('link', { name: /View entry plans/ })
  ).toHaveAttribute('href', '#frontier-plans')
  expect(screen.getByLabelText('Trade idea')).toHaveValue(
    'Find a pullback entry with clear risk and exits.'
  )
})

it('shows generated plans automatically and uses the user idea instead of a generic prompt', async () => {
  mocks.stream.mockImplementation(async (_body, _signal, onEvent) => {
    onEvent({
      type: 'plans',
      data: { plans: [plan('one'), plan('two')], checks: [] },
    })
  })
  open()
  await waitFor(() =>
    expect(screen.getByLabelText('NOFX AI model')).toHaveValue('model')
  )
  fireEvent.change(screen.getByLabelText('Trade idea'), {
    target: { value: 'Compare timed entries for a pullback.' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Generate plans' }))
  await waitFor(() =>
    expect(
      screen.getByRole('region', { name: 'Entry plans' })
    ).toHaveTextContent('Test plan one')
  )
  expect(mocks.stream).toHaveBeenCalledWith(
    expect.objectContaining({
      message: 'Compare timed entries for a pullback.',
    }),
    expect.anything(),
    expect.any(Function)
  )
  expect(
    mocks.api.mock.calls.some(
      ([path]) => path === 'approve' || path === 'execute'
    )
  ).toBe(false)
})

it('starts a separate draft from a locked trade without revising or executing that trade', async () => {
  const base = mocks.api.getMockImplementation()!
  mocks.api.mockImplementation((path: string, body?: unknown) =>
    path === 'state'
      ? {
          records: { one: { plan: plan('one'), state: 'approved' } },
          accounts: { one: 'ex' },
          runs: [],
          mode: 'live',
          now: new Date().toISOString(),
        }
      : base(path, body)
  )
  mocks.stream.mockResolvedValue(undefined)
  open()
  await waitFor(() =>
    expect(screen.getByLabelText('NOFX AI model')).toHaveValue('model')
  )
  fireEvent.click(await screen.findByText('Plan history · 1'))
  fireEvent.click(await screen.findByRole('button', { name: /Test plan one/ }))
  fireEvent.change(screen.getByLabelText('Trade idea'), {
    target: { value: 'Generate a new independent entry plan.' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Generate plans' }))
  await waitFor(() => expect(mocks.stream).toHaveBeenCalledTimes(1))
  const input = mocks.stream.mock.calls[0][0]
  expect(input.plan_id).toBeUndefined()
  expect(input.history).toEqual([])
  expect(input.message).toBe('Generate a new independent entry plan.')
  expect(
    mocks.api.mock.calls.some(([path]) =>
      ['approve', 'execute', 'stop'].includes(path)
    )
  ).toBe(false)
  expect(screen.queryByText(/AI cannot modify a locked plan/)).toBeNull()
})

describe('previous trade recovery', () => {
  it('reviews a new plan without cancelling an unused approval, then replaces only that approval on confirmation', async () => {
    const base = mocks.api.getMockImplementation()!
    const previous = {
      id: 'unused-run',
      exchange_id: 'ex',
      plan_id: 'old',
      approval_hash: 'previous-hash',
      state: 'approved',
      plan: plan('old'),
      orders: [],
    }
    mocks.api.mockImplementation(async (path: string, body?: unknown) =>
      path === 'state'
        ? { ...(await base(path, body)), runs: [previous] }
        : base(path, body)
    )
    open()
    await screen.findByText('BTCUSDT order not submitted')
    fireEvent.click(screen.getByRole('button', { name: 'Review Market' }))
    let dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('note')).toHaveTextContent(
      'replaces your earlier unsubmitted BTCUSDT approval'
    )
    expect(
      mocks.api.mock.calls.some(([path]) =>
        ['approve', 'execute', 'stop'].includes(path)
      )
    ).toBe(false)
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Back to review' })
    )
    fireEvent.click(screen.getByRole('button', { name: 'Review Single limit' }))
    dialog = await screen.findByRole('dialog')
    fireEvent.click(
      within(dialog).getByRole('button', {
        name: 'Confirm and place live order',
      })
    )
    await waitFor(() =>
      expect(mocks.api).toHaveBeenCalledWith(
        'approve',
        expect.objectContaining({
          confirmed: true,
          replace_approval_id: 'unused-run',
          replace_approval_hash: 'previous-hash',
        })
      )
    )
    await waitFor(() =>
      expect(mocks.api).toHaveBeenCalledWith('execute', {
        id: 'run',
        hash: 'locked',
      })
    )
    expect(mocks.api.mock.calls.some(([path]) => path === 'stop')).toBe(false)
    expect(
      screen.queryByText(/An earlier order is approved but not submitted/)
    ).toBeNull()
  })

  it('does not leave a success notice when previous trade recovery is followed by a review failure', async () => {
    const base = mocks.api.getMockImplementation()!
    let closed = false
    const previous = {
      id: 'old-run',
      exchange_id: 'ex',
      plan_id: 'old',
      state: 'needs_attention',
      plan: plan('old'),
      orders: [],
    }
    mocks.api.mockImplementation(async (path: string, body?: unknown) => {
      if (path === 'state')
        return {
          ...(await base(path, body)),
          runs: [{ ...previous, state: closed ? 'completed' : previous.state }],
        }
      if (path === 'check-status') {
        closed = true
        return { ...previous, state: 'completed' }
      }
      if (path === 'compile')
        throw new Error('Account balance is temporarily unavailable.')
      return base(path, body)
    })
    open()
    await screen.findByRole('button', { name: 'Verify status' })
    fireEvent.click(screen.getByRole('button', { name: 'Review Market' }))
    expect(
      await screen.findByText('Account balance is temporarily unavailable.')
    ).toBeVisible()
    expect(screen.queryByText(/Previous trade closed/)).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByRole('button', { name: 'Review Market' })).toBeEnabled()
  })

  it('identifies the blocking trade and checks it without submitting or cancelling orders', async () => {
    const base = mocks.api.getMockImplementation()!
    let closed = false
    const old = {
      id: 'previous-run',
      exchange_id: 'ex',
      plan_id: 'old',
      approval_hash: 'hash',
      state: 'needs_attention',
      plan: plan('old'),
      orders: [],
    }
    mocks.api.mockImplementation(async (path: string, body?: unknown) => {
      if (path === 'state') {
        const data = await base(path, body)
        return {
          ...data,
          runs: [{ ...old, state: closed ? 'completed' : old.state }],
        }
      }
      if (path === 'check-status') {
        closed = true
        return { ...old, state: 'completed' }
      }
      return base(path, body)
    })
    open()
    await screen.findByRole('button', { name: 'Verify status' })
    expect(
      screen.getByRole('region', { name: 'Current trade status' })
    ).toHaveTextContent('BTCUSDT trade status needs verification')
    fireEvent.click(
      await screen.findByRole('button', { name: /Test plan one/ })
    )
    fireEvent.click(screen.getByRole('button', { name: 'Review order' }))
    await screen.findByRole('dialog')
    expect(screen.queryByText(/Previous trade closed/)).toBeNull()
    expect(mocks.api).toHaveBeenCalledWith(
      'compile',
      expect.objectContaining({ auto_size: true })
    )
    expect(mocks.api).toHaveBeenCalledWith('check-status', {
      id: 'previous-run',
    })
    expect(
      mocks.api.mock.calls.some(([path]) =>
        ['execute', 'approve', 'stop', 'reconcile'].includes(path)
      )
    ).toBe(false)
  })
  it('keeps new orders blocked when the exchange check fails', async () => {
    const base = mocks.api.getMockImplementation()!
    mocks.api.mockImplementation(async (path: string, body?: unknown) => {
      if (path === 'state')
        return {
          ...(await base(path, body)),
          runs: [
            {
              id: 'previous-run',
              exchange_id: 'ex',
              plan_id: 'old',
              state: 'needs_attention',
              plan: plan('old'),
              orders: [],
            },
          ],
        }
      if (path === 'check-status')
        throw new Error('Could not confirm stop order. Check the exchange.')
      return base(path, body)
    })
    open()
    fireEvent.click(
      await screen.findByRole('button', { name: /Review Market/ })
    )
    expect(
      await screen.findByText(
        'Could not confirm stop order. Check the exchange.'
      )
    ).toBeVisible()
    expect(
      screen.getByRole('region', { name: 'Current trade status' })
    ).toBeVisible()
    expect(
      mocks.api.mock.calls.some(([path]) =>
        ['execute', 'approve', 'stop', 'compile'].includes(path)
      )
    ).toBe(false)
  })
  it('clears a failed verification after the exchange confirms closure, without submitting an order', async () => {
    const base = mocks.api.getMockImplementation()!
    let closed = false
    let attempts = 0
    const old = {
      id: 'previous-run',
      exchange_id: 'ex',
      plan_id: 'old',
      state: 'needs_attention',
      plan: plan('old'),
      orders: [],
    }
    mocks.api.mockImplementation(async (path: string, body?: unknown) => {
      if (path === 'state')
        return {
          ...(await base(path, body)),
          runs: [{ ...old, state: closed ? 'completed' : old.state }],
        }
      if (path === 'check-status') {
        if (++attempts === 1)
          throw new Error('Could not confirm stop order. Check the exchange.')
        closed = true
        return { ...old, state: 'completed' }
      }
      return base(path, body)
    })
    open()
    fireEvent.click(
      await screen.findByRole('button', { name: 'Verify status' })
    )
    const blocker = screen.getByRole('region', { name: 'Current trade status' })
    expect(await within(blocker).findByRole('alert')).toHaveTextContent(
      'Could not confirm stop order'
    )
    const retry = screen.getByRole('button', { name: 'Verify status' })
    await waitFor(() => expect(retry).toBeEnabled())
    fireEvent.click(retry)
    await waitFor(() =>
      expect(
        screen.queryByRole('region', { name: 'Current trade status' })
      ).toBeNull()
    )
    expect(
      screen.queryByText('Could not confirm stop order. Check the exchange.')
    ).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent(
      'Previous trade is closed'
    )
    expect(
      mocks.api.mock.calls.some(([path]) =>
        ['execute', 'approve', 'stop', 'reconcile'].includes(path)
      )
    ).toBe(false)
  })
})

it('keeps expired drafts visible and refreshes them before review, without executing', async () => {
  const base = mocks.api.getMockImplementation()!
  const stale = {
    ...plan('one'),
    expires: new Date(Date.now() - 60000).toISOString(),
  }
  mocks.api.mockImplementation(async (path: string, body?: unknown) =>
    path === 'state'
      ? {
          ...(await base(path, body)),
          records: { one: { plan: stale, state: 'draft' } },
        }
      : base(path, body)
  )
  open()
  fireEvent.click(await screen.findByRole('button', { name: /Review Market/ }))
  await screen.findByRole('dialog')
  expect(mocks.api).toHaveBeenCalledWith(
    'compile',
    expect.objectContaining({ auto_size: true })
  )
  expect(
    mocks.api.mock.calls.some(([p]) => p === 'approve' || p === 'execute')
  ).toBe(false)
})

it('distinguishes Binance accounts by their names and remembers each venue selection', async () => {
  mocks.exchanges = [
    {
      id: 'binance-a',
      name: 'Binance Futures',
      account_name: 'Main portfolio',
      exchange_type: 'binance',
      enabled: true,
    },
    {
      id: 'binance-b',
      name: 'Binance Futures',
      account_name: 'Second portfolio',
      exchange_type: 'binance',
      enabled: true,
    },
    {
      id: 'hl',
      name: 'Hyperliquid',
      exchange_type: 'hyperliquid',
      enabled: true,
    },
  ]
  const view = render(
    <MemoryRouter>
      <TradingPlanningPage />
    </MemoryRouter>
  )
  await waitFor(() =>
    expect(screen.getByLabelText('Trading account')).toHaveValue('binance-a')
  )
  expect(
    screen.getByRole('option', { name: 'Second portfolio · binance' })
  ).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Symbol'), {
    target: { value: 'ETHUSDT' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Open market' }))
  fireEvent.change(screen.getByLabelText('Trading account'), {
    target: { value: 'binance-b' },
  })
  expect(screen.getByLabelText('Symbol')).toHaveValue('ETHUSDT')
  fireEvent.change(screen.getByLabelText('Market venue'), {
    target: { value: 'hyperliquid' },
  })
  fireEvent.change(screen.getByLabelText('Market venue'), {
    target: { value: 'binance' },
  })
  expect(screen.getByLabelText('Trading account')).toHaveValue('binance-b')
  view.unmount()
  render(
    <MemoryRouter>
      <TradingPlanningPage />
    </MemoryRouter>
  )
  await waitFor(() =>
    expect(screen.getByLabelText('Trading account')).toHaveValue('binance-b')
  )
  expect(mocks.stream).not.toHaveBeenCalled()
})
