import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { LiveRun } from '../../lib/planning'
import type { AccountObservation } from '../../lib/frontierWorkspace'
import { FrontierOrderBlocker } from './FrontierOrderBlocker'

afterEach(cleanup)
const run = {
  exchange_id: 'account',
  state: 'needs_attention',
  plan: {
    snapshot: { venue: 'hyperliquid', symbol: 'BTC', quote_asset: 'USDC' },
  },
} as LiveRun
const observation: AccountObservation = {
  accountID: 'account',
  symbol: 'BTC',
  positions: [
    {
      symbol: 'BTC',
      side: 'short',
      quantity: 0.01,
      mark: 80000,
      entry: 81000,
      pnl: 10,
      leverage: 5,
    },
  ],
  orders: [],
  checkedAt: Date.now(),
}
const actions = {
  now: Date.now(),
  busy: false,
  onCheck: vi.fn(),
  onView: vi.fn(),
  onRevoke: vi.fn(),
  onAccount: vi.fn(),
}
it('shows a verified live position separately from its monitoring issue', () => {
  render(
    <FrontierOrderBlocker {...actions} run={run} observation={observation} />
  )
  expect(screen.getByText('BTC Short open · 5x')).toBeVisible()
  expect(screen.getByText(/800 USDC position/)).toBeVisible()
  expect(screen.getByText(/Exit order status needs verification/)).toBeVisible()
  expect(screen.getByRole('button', { name: 'Verify status' })).toBeVisible()
  fireEvent.click(
    screen.getByRole('button', { name: 'View position & orders' })
  )
  expect(actions.onAccount).toHaveBeenCalledOnce()
  expect(actions.onRevoke).not.toHaveBeenCalled()
})
it('removes the open-position claim as soon as a fresh flat snapshot arrives', () => {
  const { rerender } = render(
    <FrontierOrderBlocker {...actions} run={run} observation={observation} />
  )
  expect(screen.getByText('BTC Short open · 5x')).toBeVisible()
  rerender(
    <FrontierOrderBlocker
      {...actions}
      run={run}
      observation={{ ...observation, positions: [], orders: [] }}
    />
  )
  expect(screen.queryByText('BTC Short open · 5x')).toBeNull()
  expect(
    screen.getByText('No open BTC position · previous orders need verification')
  ).toBeVisible()
  expect(screen.getByText(/You can still generate plans/)).toBeVisible()
  expect(screen.getByRole('button', { name: 'Verify status' })).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Cancel approval' })).toBeNull()
})
it('keeps verification errors next to the unresolved trade and allows another read-only check', () => {
  render(
    <FrontierOrderBlocker
      {...actions}
      run={run}
      observation={observation}
      checkError="Stop-loss status could not be confirmed."
    />
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Stop-loss status could not be confirmed.'
  )
  fireEvent.click(screen.getByRole('button', { name: 'Verify status' }))
  expect(actions.onCheck).toHaveBeenCalled()
})
it('does not claim a position is open using stale or other-account data', () => {
  const { rerender } = render(
    <FrontierOrderBlocker
      {...actions}
      run={run}
      observation={{ ...observation, checkedAt: 0 }}
    />
  )
  expect(screen.getByText('BTC trade status needs verification')).toBeVisible()
  rerender(
    <FrontierOrderBlocker
      {...actions}
      run={run}
      observation={{ ...observation, accountID: 'other' }}
    />
  )
  expect(screen.getByRole('button', { name: 'Verify status' })).toBeVisible()
  expect(screen.queryByText('BTC Short open · 5x')).toBeNull()
})
it('distinguishes an approved unsent order from a live position', () => {
  render(
    <FrontierOrderBlocker {...actions} run={{ ...run, state: 'approved' }} />
  )
  expect(screen.getByText('BTC order not submitted')).toBeVisible()
  expect(
    screen.getByText(/confirming it replaces this unused approval/)
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Cancel approval' })).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Verify status' })).toBeNull()
})
