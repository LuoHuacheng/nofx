import { StrictMode } from 'react'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { FrontierSetup, feeWalletState } from './FrontierSetup'
const mocks = vi.hoisted(() => ({ api: vi.fn(), create: vi.fn() }))
vi.mock('../../lib/planning', () => ({ planningAPI: mocks.api }))
vi.mock('../../lib/api', () => ({
  api: {
    createExchangeEncrypted: mocks.create,
    getExchangeConfigs: async () => [],
    getSupportedModels: async () => [{ id: 'claw402', provider: 'claw402' }],
  },
}))
vi.mock('../common/HyperliquidWalletConnect', () => ({
  HyperliquidWalletConnect: ({
    onSaved,
  }: {
    onSaved: (id?: string) => void
  }) => (
    <button onClick={() => onSaved('saved-hl')}>
      Mock authorized connection saved
    </button>
  ),
}))
vi.mock('../trader/ExchangeConfigModal', () => ({
  ExchangeConfigModal: ({
    initialExchangeType,
    onSave,
  }: {
    initialExchangeType: string
    onSave: (...args: unknown[]) => Promise<void>
  }) => (
    <button
      onClick={() =>
        void onSave(
          null,
          initialExchangeType,
          'Account two',
          'test-key',
          'test-secret',
          'test-passphrase',
          false
        )
      }
    >
      Save mock account
    </button>
  ),
}))
const address = '0x1111111111111111111111111111111111111111'
const missing = {
  found: false,
  enabled: false,
  chain: 'base',
  asset: 'USDC',
  balance_status: 'unknown',
}
const refresh = vi.fn().mockResolvedValue(undefined),
  open = vi.fn(),
  state = vi.fn()
beforeEach(() => {
  mocks.api.mockReset()
  refresh.mockClear()
  open.mockClear()
  state.mockClear()
  mocks.api.mockResolvedValue(missing)
})
afterEach(cleanup)
function show(step: 'ai' | 'exchange' | null = 'ai', direct = false) {
  return render(
    <StrictMode>
      <MemoryRouter>
        <FrontierSetup
          models={
            direct
              ? [
                  {
                    id: 'direct',
                    name: 'My AI',
                    provider: 'deepseek',
                    enabled: true,
                    has_api_key: true,
                  },
                ]
              : []
          }
          modelID={direct ? 'direct' : ''}
          accounts={[]}
          accountID=""
          venue="hyperliquid"
          loaded
          open={step}
          onOpen={open}
          onRefresh={refresh}
          onWalletState={state}
        />
      </MemoryRouter>
    </StrictMode>
  )
}
it('opens the shared backup flow instead of creating and funding a wallet silently', async () => {
  show()
  const button = await screen.findByRole('button', { name: 'Create AI wallet' })
  await waitFor(() => expect(button).toBeEnabled())
  fireEvent.click(button)
  expect(
    await screen.findByRole('button', { name: 'Create my AI wallet' })
  ).toBeVisible()
  expect(mocks.api.mock.calls.every((call) => call.length === 1)).toBe(true)
  expect(refresh).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: /execute|place live order/i })
  ).toBeNull()
})
it('never retries an unconfirmed setup mutation automatically', async () => {
  mocks.api.mockImplementation(async (_path: string, body: unknown) => {
    if (body) throw new Error('request interrupted')
    return {
      ...missing,
      found: true,
      address,
      model_id: 'saved',
      enabled: false,
    }
  })
  show()
  const button = await screen.findByRole('button', {
    name: 'Enable existing AI wallet',
  })
  await waitFor(() => expect(button).toBeEnabled())
  fireEvent.click(button)
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'could not be confirmed'
  )
  expect(mocks.api.mock.calls.filter((call) => call.length === 2)).toHaveLength(
    1
  )
})
it('lets an existing direct AI provider continue without a fee-wallet deposit', async () => {
  show('ai', true)
  expect(screen.getByText('Your AI is configured')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Continue with this AI' }))
  expect(open).toHaveBeenCalledWith(null)
  expect(mocks.api.mock.calls.every((call) => call.length === 1)).toBe(true)
})
it('refreshes a saved exchange in place without starting trading or the model', async () => {
  show('exchange', true)
  fireEvent.click(
    screen.getByRole('button', { name: 'Mock authorized connection saved' })
  )
  await waitFor(() =>
    expect(refresh).toHaveBeenCalledWith(undefined, 'hyperliquid', 'saved-hl')
  )
  expect(open).not.toHaveBeenCalled()
  expect(mocks.create).not.toHaveBeenCalled()
})
it('does not treat an unknown or malformed balance as funded', () => {
  for (const balance of [undefined, '', 'NaN', '-1'])
    expect(
      feeWalletState({
        ...missing,
        found: true,
        enabled: true,
        balance_usdc: balance,
        balance_status: 'ok',
      })
    ).toBe('unknown')
  expect(
    feeWalletState({
      ...missing,
      found: true,
      enabled: true,
      balance_usdc: '0.000001',
      balance_status: 'ok',
    })
  ).toBe('ready')
})

it('does not mark saved credentials as verified until the live account check succeeds', async () => {
  let resolveAccount!: (value: unknown) => void
  const accountRead = new Promise((resolve) => {
    resolveAccount = resolve
  })
  mocks.api.mockImplementation((path: string) =>
    path.startsWith('account?') ? accountRead : Promise.resolve(missing)
  )
  const complete = vi.fn()
  render(
    <FrontierSetup
      models={[]}
      modelID=""
      accounts={[
        {
          id: 'binance',
          name: 'My Binance',
          exchange_type: 'binance',
          enabled: true,
        },
      ]}
      accountID="binance"
      venue="binance"
      loaded
      open="exchange"
      onOpen={open}
      onRefresh={refresh}
      onWalletState={state}
      onComplete={complete}
    />
  )
  expect(
    screen.getByRole('button', { name: 'Return to my plan' })
  ).toBeDisabled()
  resolveAccount({
    equity: '500',
    available: '250',
    position: '0',
    open_orders: 0,
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Return to my plan' })
    ).toBeEnabled()
  )
  expect(screen.getByText(/Available margin 250.00 USDT/)).toBeVisible()
  expect(
    screen.getByRole('button', {
      name: /Trading account My Binance Account equity 500.00 USDT Available to trade 250.00 USDT/,
    })
  ).toBeVisible()
  expect(complete).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Return to my plan' }))
  expect(complete).toHaveBeenCalledWith('exchange')
  expect(
    mocks.api.mock.calls.every(
      ([path]) => path.startsWith('account?') || path.startsWith('setup-wallet')
    )
  ).toBe(true)
})
it('keeps invalid exchange credentials actionable instead of showing a ready account', async () => {
  mocks.api.mockImplementation(async (path: string) => {
    if (path.startsWith('account?'))
      throw new Error('Futures trading permission is disabled')
    return missing
  })
  render(
    <FrontierSetup
      models={[]}
      modelID=""
      accounts={[
        {
          id: 'binance',
          name: 'My Binance',
          exchange_type: 'binance',
          enabled: true,
        },
      ]}
      accountID="binance"
      venue="binance"
      loaded
      open="exchange"
      onOpen={open}
      onRefresh={refresh}
      onWalletState={state}
    />
  )
  expect(
    await screen.findByText('Futures trading permission is disabled')
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Return to my plan' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Verify account' })).toBeEnabled()
})

it('does not offer an empty provider picker when the deployment supports Claw402 only', async () => {
  show()
  await screen.findByRole('button', { name: 'Create AI wallet' })
  expect(
    screen.queryByRole('button', { name: 'Use my own API key instead →' })
  ).toBeNull()
})

it.each(['okx', 'bitget'])(
  'connects %s with an encrypted passphrase and selects the saved account',
  async (venue) => {
    mocks.create.mockResolvedValue({ id: 'saved-account' })
    render(
      <FrontierSetup
        models={[]}
        modelID=""
        accounts={[]}
        accountID=""
        venue={venue}
        loaded
        open="exchange"
        onOpen={open}
        onRefresh={refresh}
        onWalletState={state}
      />
    )
    fireEvent.click(
      screen.getByRole('button', {
        name: `Connect ${venue === 'okx' ? 'OKX' : 'Bitget'}`,
        exact: true,
      })
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save mock account' }))
    await waitFor(() =>
      expect(refresh).toHaveBeenCalledWith(undefined, venue, 'saved-account')
    )
    expect(mocks.create).toHaveBeenCalledWith(
      expect.objectContaining({
        exchange_type: venue,
        api_key: 'test-key',
        secret_key: 'test-secret',
        passphrase: 'test-passphrase',
        testnet: false,
      })
    )
  }
)
