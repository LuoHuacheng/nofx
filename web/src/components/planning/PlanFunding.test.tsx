import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import type { Plan } from '../../lib/planning'
import { PlanFunding } from './PlanFunding'

afterEach(cleanup)
const plan = {
  idea: { entry: '100' },
  quantity: '3',
  constraints: { leverage: 5, available_margin: '100' },
  snapshot: { quote_asset: 'USDC' },
  orders: [
    { price: '100', quantity: '1' },
    { price: '200', quantity: '2' },
  ],
} as Plan

it('labels equity previews without presenting equity as free margin', () => {
  const preview = {
    ...plan,
    constraints: {
      ...plan.constraints,
      available_margin: '0',
      planning_only: true,
    },
  }
  const view = render(<PlanFunding plan={preview} />)
  expect(
    screen.getByText(/Equity preview only: no free margin/)
  ).toBeInTheDocument()
  expect(screen.queryByText(/Available margin at sizing/)).toBeNull()
  view.rerender(<PlanFunding plan={preview} compact />)
  expect(
    screen.getByText('Equity preview · refresh before trading')
  ).toBeInTheDocument()
})

it('uses weighted child notional and the plan leverage, not the current selector', () => {
  render(<PlanFunding plan={plan} />)
  expect(screen.getByText('500.00')).toBeInTheDocument()
  expect(screen.getByText('100.00')).toBeInTheDocument()
  expect(screen.getByText('5x')).toBeInTheDocument()
  expect(
    screen.getByText(/Available margin at sizing: 100.00/)
  ).toBeInTheDocument()
})

it('identifies a saved legacy size cap without implying the position was resized', () => {
  render(<PlanFunding plan={{ ...plan, sizing_limit: 'liquidity' }} />)
  expect(
    screen.getByText(/This saved plan used the previous top-of-book size cap/)
  ).toBeInTheDocument()
})

it('does not invent an available balance for an older saved plan', () => {
  render(
    <PlanFunding
      plan={{
        ...plan,
        orders: [],
        constraints: { ...plan.constraints, available_margin: undefined },
      }}
    />
  )
  expect(screen.getByText('300.00')).toBeInTheDocument()
  expect(screen.getByText(/Saved plan size/)).toBeInTheDocument()
  expect(screen.queryByText(/Available margin at sizing/)).toBeNull()
})
