import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import type { AIModel } from '../../types'
import { TerminalDashboard } from './TerminalDashboard'

const mocks = vi.hoisted(() => ({
  models: vi.fn(),
  heatmap: vi.fn(),
  matrix: vi.fn(),
  config: { claw402_enabled: true, vergex_enabled: true },
  user: { id: 'alice' },
}))
vi.mock('../../lib/api', () => ({
  api: {
    getModelConfigs: mocks.models,
    getVergexCostLiquidationHeatmap: mocks.heatmap,
    getVergexHolderWinrateMap: mocks.matrix,
  },
}))
vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ user: mocks.user }),
}))
vi.mock('../../hooks/useSystemConfig', () => ({
  useSystemConfig: () => ({ config: mocks.config }),
}))
vi.mock('./OrderBook', () => ({ OrderBook: () => <div>Order book</div> }))
vi.mock('./KlineChart', () => ({ KlineChart: () => <div>Candles</div> }))

const wallet: AIModel = {
  id: 'fee-wallet',
  name: 'Claw402',
  provider: 'claw402',
  enabled: false,
  has_api_key: true,
  walletAddress: '0x1234',
}
function view(cache = new Map()) {
  return (
    <SWRConfig value={{ provider: () => cache }}>
      <TerminalDashboard onTraderSelect={vi.fn()} />
    </SWRConfig>
  )
}
beforeEach(() => {
  mocks.user.id = 'alice'
  mocks.config.claw402_enabled = true
  mocks.config.vergex_enabled = true
  mocks.models.mockReset().mockResolvedValue([])
  mocks.heatmap.mockReset().mockResolvedValue({ data: { bins: [] } })
  mocks.matrix.mockReset().mockResolvedValue({ data: { cells: [] } })
})
afterEach(cleanup)

it('does not query either paid panel while wallet configuration is loading or absent', async () => {
  let resolve!: (models: AIModel[]) => void
  mocks.models.mockReturnValue(
    new Promise<AIModel[]>((done) => {
      resolve = done
    })
  )
  render(view())
  expect(mocks.heatmap).not.toHaveBeenCalled()
  expect(mocks.matrix).not.toHaveBeenCalled()
  await waitFor(() => expect(mocks.models).toHaveBeenCalledOnce())
  await act(async () => {
    resolve([
      {
        id: 'custom',
        name: 'DeepSeek',
        provider: 'custom',
        enabled: true,
        has_api_key: true,
      },
      { ...wallet, has_api_key: false, walletAddress: undefined },
    ])
  })
  expect(await screen.findByText(/未启用付费数据/)).toBeInTheDocument()
  expect(mocks.heatmap).not.toHaveBeenCalled()
  expect(mocks.matrix).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: /Retry matrix query/ })
  ).not.toBeInTheDocument()
  expect(screen.getByText('Candles')).toBeInTheDocument()
  expect(screen.getByText('Order book')).toBeInTheDocument()
})

it('preserves paid data access for a saved wallet even if its AI model is disabled', async () => {
  mocks.models.mockResolvedValue([wallet])
  render(view())
  await waitFor(() => expect(mocks.heatmap).toHaveBeenCalledOnce())
  expect(mocks.matrix).toHaveBeenCalledOnce()
  expect(screen.queryByText(/未启用付费数据/)).not.toBeInTheDocument()
})

it('does not spend when configuration cannot be loaded', async () => {
  mocks.models.mockRejectedValue(new Error('configuration unavailable'))
  render(view())
  await screen.findByText(/付费数据配置暂不可用/)
  expect(mocks.heatmap).not.toHaveBeenCalled()
  expect(mocks.matrix).not.toHaveBeenCalled()
})

it('does not reuse another user’s cached wallet permission', async () => {
  mocks.models
    .mockResolvedValueOnce([wallet])
    .mockReturnValue(new Promise(() => {}))
  const cache = new Map()
  const ui = render(view(cache))
  await waitFor(() => expect(mocks.matrix).toHaveBeenCalledOnce())
  mocks.user.id = 'bob'
  ui.rerender(view(cache))
  await waitFor(() => expect(mocks.models).toHaveBeenCalledTimes(2))
  expect(screen.getByText(/正在检查付费数据配置/)).toBeInTheDocument()
  expect(mocks.matrix).toHaveBeenCalledOnce()
  expect(mocks.heatmap).toHaveBeenCalledOnce()
})

it('preserves the local demo without a paid wallet or data calls', async () => {
  render(view())
  await screen.findByText(/未启用付费数据/)
  fireEvent.click(
    screen.getByRole('button', { name: 'toggle presentation mode' })
  )
  await screen.findByText(/click a cell for demo addresses/)
  expect(mocks.heatmap).not.toHaveBeenCalled()
  expect(mocks.matrix).not.toHaveBeenCalled()
})

it('keeps both paid panels disabled when the global feature is off', async () => {
  mocks.config.vergex_enabled = false
  mocks.models.mockResolvedValue([wallet])
  render(view())
  expect(await screen.findByText(/未启用付费数据/)).toBeInTheDocument()
  expect(mocks.models).not.toHaveBeenCalled()
  expect(mocks.heatmap).not.toHaveBeenCalled()
  expect(mocks.matrix).not.toHaveBeenCalled()
})
