import { cleanup, render, screen, fireEvent } from '@testing-library/react'
import { afterEach, describe, it, expect, vi } from 'vitest'
import { PlanningAI500 } from './PlanningAI500'
const mocks = vi.hoisted(() => ({ api: vi.fn() }))
vi.mock('../../lib/planning', () => ({ planningAPI: mocks.api }))
afterEach(() => {
  cleanup()
  mocks.api.mockReset()
})
describe('paid AI500 board', () => {
  it('loads all returned markets and lets the user search and select', async () => {
    mocks.api.mockResolvedValue({
      coins: [
        { pair: 'BTCUSDT', score: 80 },
        { pair: 'ETHUSDT', score: 88 },
        { pair: 'XMRUSDT', score: 82 },
      ],
      source: 'Claw402',
      fetched_at: new Date().toISOString(),
    })
    const select = vi.fn()
    const markets = vi.fn()
    render(
      <PlanningAI500
        venue="binance"
        disabled={false}
        onSelect={select}
        onMarkets={markets}
      />
    )
    await screen.findByRole('button', { name: /ETH/ })
    fireEvent.change(screen.getByLabelText('Search AI500'), {
      target: { value: 'eth' },
    })
    expect(screen.queryByRole('button', { name: /XMR/ })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /ETH/ }))
    expect(select).toHaveBeenCalledWith('ETHUSDT')
    expect(mocks.api).toHaveBeenCalledTimes(1)
    expect(markets).toHaveBeenCalledWith(['BTCUSDT', 'ETHUSDT', 'XMRUSDT'])
    expect(
      screen.getByText('Long entries · market or pullback opportunities')
    ).toBeVisible()
  })
  it('does not buy AI500 when using a Hyperliquid chart', () => {
    render(
      <PlanningAI500 venue="hyperliquid" disabled={false} onSelect={() => {}} />
    )
    expect(mocks.api).not.toHaveBeenCalled()
  })
  it('handles an invalid provider response without crashing or retrying', async () => {
    mocks.api.mockResolvedValue({})
    render(
      <PlanningAI500 venue="binance" disabled={false} onSelect={() => {}} />
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invalid AI500 response'
    )
    expect(mocks.api).toHaveBeenCalledTimes(1)
  })
})

it('guides missing-wallet users without making a paid request', () => {
  const setup = vi.fn()
  render(
    <PlanningAI500
      venue="binance"
      disabled={false}
      walletReady={false}
      onSetup={setup}
      onSelect={() => {}}
    />
  )
  fireEvent.click(screen.getByRole('button', { name: 'Set up AI wallet' }))
  expect(setup).toHaveBeenCalledOnce()
  expect(mocks.api).not.toHaveBeenCalled()
})
