import { expect, it } from 'vitest'
import { validationSummary } from './planningPresentation'

it('explains rounded ratio failures without implying an equal ratio failed', () => {
  expect(validationSummary('net reward/risk 1.999999 is below minimum 2')).toBe(
    'Net R:R is just below 2.00 : 1 after costs.'
  )
  expect(validationSummary('net reward/risk 1.246912 is below minimum 2')).toBe(
    'Net R:R 1.25 : 1 is below the required 2.00 : 1 after costs.'
  )
  expect(validationSummary('invalid stop/target ordering')).toBe(
    'invalid stop/target ordering'
  )
})

it('keeps immutable chart plan references when only execution timestamps refresh', async () => {
  const { preservePlanningPlans } = await import('./planningPresentation')
  const previous = {
    records: {
      plan: { plan: { id: 'plan', hash: 'fixed' }, state: 'approved' },
    },
    runs: [
      {
        id: 'run',
        plan: { id: 'plan', hash: 'fixed' },
        state: 'needs_attention',
        updated_at: 'before',
      },
    ],
    accounts: {},
    mode: 'live',
    now: 'before',
  } as unknown as import('./planning').PlanningState
  const incoming = JSON.parse(JSON.stringify(previous))
  incoming.now = 'after'
  incoming.runs[0].updated_at = 'after'
  const result = preservePlanningPlans(previous, incoming)
  expect(result.records).toBe(previous.records)
  expect(result.runs[0].plan).toBe(previous.runs[0].plan)
  expect(result.runs[0].updated_at).toBe('after')
  incoming.records.plan.state = 'completed'
  expect(preservePlanningPlans(previous, incoming).records).not.toBe(
    previous.records
  )
})
