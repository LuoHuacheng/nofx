import type { Plan, PlanningState } from './planning'
import type { AccountPosition } from './frontierAccount'

export interface AccountObservation {
  accountID: string
  symbol: string
  positions: AccountPosition[]
  orders:
    | { type: string; status: string; quantity: number; side: string }[]
    | null
  checkedAt: number
}

export interface PlanBatch {
  id: string
  ids: string[]
  created: string
  waiting?: boolean
  deferred?: { kind: string; reason: string }[]
}

export function workspaceKey(account: string, venue: string, symbol: string) {
  // Scope browser presentation preferences to the signed-in user as well as market.
  let user = ''
  try {
    user = localStorage.getItem('user_id') || ''
  } catch {
    /* Private browsing may disable storage. */
  }
  return ['frontier-batch-v1', user, account, venue, symbol].join('/')
}

export function readBatch(key: string): PlanBatch | null {
  try {
    const batch = JSON.parse(localStorage.getItem(key) || 'null')
    return batch &&
      typeof batch.id === 'string' &&
      typeof batch.created === 'string' &&
      (batch.waiting === undefined || typeof batch.waiting === 'boolean') &&
      (batch.deferred === undefined ||
        (Array.isArray(batch.deferred) &&
          batch.deferred.length <= 4 &&
          batch.deferred.every(
            (entry: { kind?: unknown; reason?: unknown } | null) =>
              entry &&
              ['market', 'limit', 'ladder', 'twap'].includes(
                String(entry.kind)
              ) &&
              typeof entry.reason === 'string' &&
              entry.reason.length <= 2000
          ))) &&
      Array.isArray(batch.ids) &&
      batch.ids.every((id: unknown) => typeof id === 'string')
      ? batch
      : null
  } catch {
    return null
  }
}

export function saveBatch(key: string, batch: PlanBatch) {
  try {
    localStorage.setItem(key, JSON.stringify(batch))
  } catch {
    /* Storage may be unavailable. */
  }
}

// Never infer a generation batch from time proximity or entry type. Legacy
// drafts have no batch identity, so show one saved idea instead of mixing rounds.
export function batchPlans(
  records: PlanningState['records'][string][],
  batch: PlanBatch | null
): Plan[] {
  const drafts = records.filter((record) => record.state === 'draft')
  if (!batch) return drafts.length ? [drafts[0].plan] : []
  const found = batch.ids.flatMap((id) => {
    const record = drafts.find((row) => row.plan.id === id)
    return record ? [record.plan] : []
  })
  return ['market', 'limit', 'ladder', 'twap'].flatMap((kind) => {
    const candidates = found.filter((plan) => plan.idea.execution.kind === kind)
    return candidates.length ? [candidates[candidates.length - 1]] : []
  })
}

export function comparablePlans(plans: Plan[], now: number) {
  const fresh = plans.filter((plan) => Date.parse(plan.expires) > now)
  if (!fresh.length) return []
  const basis = JSON.stringify(fresh[0].constraints)
  return fresh.filter((plan) => JSON.stringify(plan.constraints) === basis)
}

export interface AccountCheck {
  accountID: string
  status: 'checking' | 'verified' | 'unavailable'
  equity?: string
  available?: string
  position?: string
  open_orders?: number
  error?: string
  checkedAt?: number
}
