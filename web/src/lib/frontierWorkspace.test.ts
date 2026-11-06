import { beforeEach, expect, it } from 'vitest'
import type { Plan, PlanningState } from './planning'
import {
  batchPlans,
  comparablePlans,
  readBatch,
  saveBatch,
  workspaceKey,
} from './frontierWorkspace'

const make = (id: string, kind: string, equity = '1000') =>
  ({
    state: 'draft',
    plan: {
      id,
      idea: { execution: { kind } },
      constraints: { equity },
      expires: new Date(Date.now() + 60000).toISOString(),
    } as Plan,
  }) as PlanningState['records'][string]
beforeEach(() => localStorage.clear())
it('does not assemble a comparison from different historical generations', () => {
  const records = [
    make('new-market', 'market'),
    make('old-limit', 'limit'),
    make('old-twap', 'twap'),
  ]
  expect(batchPlans(records, null).map((p) => p.id)).toEqual(['new-market'])
  expect(
    batchPlans(records, {
      id: 'new',
      ids: ['new-market', 'not-saved'],
      created: 'now',
    }).map((p) => p.id)
  ).toEqual(['new-market'])
})
it('restores only the saved batch for the same user, account and market', () => {
  localStorage.setItem('user_id', 'alice')
  const key = workspaceKey('account-a', 'binance', 'BTCUSDT')
  const batch = { id: 'turn', ids: ['a', 'b'], created: 'now' }
  saveBatch(key, batch)
  expect(readBatch(key)).toEqual(batch)
  expect(readBatch(workspaceKey('account-b', 'binance', 'BTCUSDT'))).toBeNull()
  localStorage.setItem('user_id', 'bob')
  expect(readBatch(workspaceKey('account-a', 'binance', 'BTCUSDT'))).toBeNull()
  localStorage.setItem(key, '{bad')
  expect(readBatch(key)).toBeNull()
})
it('excludes expired and differently sized plans from a comparison', () => {
  const a = make('a', 'market').plan,
    b = make('b', 'limit').plan
  const resized = make('resized', 'twap', '250').plan
  const expired = {
    ...make('expired', 'ladder').plan,
    expires: '2000-01-01T00:00:00Z',
  }
  expect(
    comparablePlans([a, b, resized, expired], Date.now()).map((p) => p.id)
  ).toEqual(['a', 'b'])
})
