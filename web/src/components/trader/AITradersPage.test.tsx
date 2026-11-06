import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { AITradersPage } from './AITradersPage'
const mocks = vi.hoisted(() => ({
  user: { id: 'user' },
  models: vi.fn(),
  accounts: vi.fn(),
  states: vi.fn(),
  update: vi.fn(),
  launch: vi.fn(),
  createExchange: vi.fn(),
}))
vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ user: mocks.user, token: 'fixture' }),
}))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('swr', () => ({
  default: () => ({ data: [], mutate: vi.fn(), isLoading: false }),
}))
vi.mock('../../lib/api', () => ({
  api: {
    getModelConfigs: mocks.models,
    getExchangeConfigs: mocks.accounts,
    getExchangeAccountState: mocks.states,
    getSupportedModels: async () => [],
    updateModelConfigs: mocks.update,
    createTrader: mocks.update,
    createExchangeEncrypted: mocks.createExchange,
  },
}))
vi.mock('./AutopilotLaunchPanel', () => ({
  AutopilotLaunchPanel: () => {
    mocks.launch()
    return <div>Guided setup panel</div>
  },
}))
vi.mock('./TradersList', () => ({
  TradersList: () => <div>Saved Autopilots</div>,
}))
vi.mock('./ModelConfigModal', () => ({
  ModelConfigModal: ({ onClose }: { onClose: () => void }) => (
    <div role="dialog" aria-label="AI connection">
      <button onClick={onClose}>Close</button>
    </div>
  ),
}))
vi.mock('./ExchangeConfigModal', () => ({
  ExchangeConfigModal: ({
    onClose,
    onSave,
  }: {
    onClose: () => void
    onSave: (...args: unknown[]) => Promise<void>
  }) => (
    <div role="dialog" aria-label="Trading account">
      <button
        onClick={() =>
          void onSave(
            null,
            'binance',
            'New account',
            'fixture-key',
            'fixture-secret'
          )
        }
      >
        Save account
      </button>
      <button onClick={onClose}>Close</button>
    </div>
  ),
}))
vi.mock('./TraderConfigModal', () => ({
  TraderConfigModal: () => <div role="dialog">Create Autopilot</div>,
}))
beforeEach(() => {
  vi.clearAllMocks()
  mocks.models.mockResolvedValue([
    {
      id: 'ai',
      name: 'My AI',
      provider: 'deepseek',
      enabled: true,
      has_api_key: true,
      customModelName: 'configured-model',
    },
  ])
  mocks.accounts.mockResolvedValue([
    {
      id: 'exchange',
      name: 'My account',
      exchange_type: 'binance',
      enabled: true,
      type: 'cex',
    },
  ])
  mocks.states.mockResolvedValue({ states: {} })
})
afterEach(cleanup)
const open = (path = '/traders') =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <AITradersPage />
    </MemoryRouter>
  )
it('starts with connections without mounting automated launch or writing configuration', async () => {
  open()
  expect(
    await screen.findByRole('button', { name: 'Edit My AI' })
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Edit My account' })).toBeVisible()
  expect(screen.getByText('Verification needed')).toBeVisible()
  expect(screen.getByText('Not verified')).toBeVisible()
  expect(screen.queryByText(/Telegram|Notifications/)).toBeNull()
  expect(mocks.launch).not.toHaveBeenCalled()
  expect(mocks.update).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Add AI', exact: true }))
  expect(screen.getByRole('dialog', { name: 'AI connection' })).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Close', exact: true }))
  fireEvent.click(
    screen.getByRole('button', { name: 'Add account', exact: true })
  )
  expect(screen.getByRole('dialog', { name: 'Trading account' })).toBeVisible()
  expect(mocks.update).not.toHaveBeenCalled()
})
it('opens guided Autopilot setup only on request and preserves saved connections', async () => {
  open()
  await screen.findByRole('button', { name: 'Edit My AI' })
  fireEvent.click(
    screen.getByRole('button', { name: /Autopilot Optional automated trading/ })
  )
  expect(screen.getByText('Saved Autopilots')).toBeVisible()
  expect(mocks.launch).not.toHaveBeenCalled()
  fireEvent.click(
    screen.getByRole('button', { name: 'Guided setup', exact: true })
  )
  expect(screen.getByText('Guided setup panel')).toBeVisible()
  expect(mocks.update).not.toHaveBeenCalled()
  fireEvent.click(
    screen.getByRole('button', { name: /Connections AI & trading accounts/ })
  )
  expect(screen.getByRole('button', { name: 'Edit My AI' })).toBeVisible()
})
it('keeps disabled saved AI connections editable and treats failed balances as unknown', async () => {
  mocks.models.mockResolvedValue([
    {
      id: 'ai',
      name: 'Disabled AI',
      provider: 'deepseek',
      enabled: false,
      has_api_key: true,
    },
  ])
  mocks.states.mockRejectedValue(new Error('fixture unavailable'))
  open()
  expect(
    await screen.findByRole('button', { name: 'Edit Disabled AI' })
  ).toBeVisible()
  expect(screen.getByText('Disabled')).toBeVisible()
  expect(screen.getByText('Not verified')).toBeVisible()
  expect(mocks.update).not.toHaveBeenCalled()
})
it('shows a read failure instead of inventing an empty successful configuration', async () => {
  mocks.models.mockRejectedValue(new Error('fixture unavailable'))
  open()
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Connections could not be loaded'
    )
  )
  expect(mocks.update).not.toHaveBeenCalled()
})

it('verifies a newly saved account automatically and shows balance once with its currency', async () => {
  open()
  await screen.findByRole('button', { name: 'Edit My account' })
  mocks.states.mockResolvedValue({
    states: {
      exchange: {
        exchange_id: 'exchange',
        status: 'ok',
        display_balance: '321.9725 USDT',
        asset: 'USDT',
        available_balance: 300.5,
      },
    },
  })
  fireEvent.click(
    screen.getByRole('button', { name: 'Add account', exact: true })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save account' }))
  expect(await screen.findByText('Connected')).toBeVisible()
  expect(screen.getByText('321.97 USDT')).toBeVisible()
  expect(screen.getByText('300.50 USDT')).toBeVisible()
  expect(mocks.states).toHaveBeenCalledTimes(2)
  expect(mocks.createExchange).toHaveBeenCalledTimes(1)
  expect(mocks.launch).not.toHaveBeenCalled()
})
it('offers a read-only retry after account verification fails', async () => {
  mocks.states.mockRejectedValueOnce(new Error('timeout'))
  open()
  expect(await screen.findByText('Verification unavailable')).toBeVisible()
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Your connection is saved'
  )
  mocks.states.mockResolvedValue({
    states: {
      exchange: {
        status: 'permission_denied',
        error_message: 'Check Futures permissions and IP allowlist',
      },
    },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Verify connection' }))
  expect(await screen.findByText('Check permissions')).toBeVisible()
  expect(
    screen.getByText('Check Futures permissions and IP allowlist')
  ).toBeVisible()
  expect(mocks.update).not.toHaveBeenCalled()
  expect(mocks.launch).not.toHaveBeenCalled()
})
it('waits for saved connections before allowing a new AI wallet or account setup', () => {
  mocks.models.mockReturnValueOnce(new Promise(() => {}))
  open()
  expect(
    screen.getByRole('button', { name: 'Add AI', exact: true })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Add account', exact: true })
  ).toBeDisabled()
})
