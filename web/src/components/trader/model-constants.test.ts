import { describe, expect, it } from 'vitest'
import { CLAW402_MODELS, DEFAULT_CLAW402_MODEL } from './model-constants'

describe('Claw402 model catalog', () => {
  it('lists verified new models without changing the saved/default selection', () => {
    const models = new Map(CLAW402_MODELS.map((m) => [m.id, m]))
    expect(models.size).toBe(CLAW402_MODELS.length)
    expect(DEFAULT_CLAW402_MODEL).toBe('gpt-5.6')
    for (const id of ['gpt-6.1-sol', 'gpt-6-sol', 'gpt-6-luna']) {
      expect(models.has(id)).toBe(true)
    }
    expect(models.get('deepseek-v4-flash')?.name).toBe('DeepSeek V4.1 Flash')
    for (const id of ['claude-fable-5-1', 'glm-5.3']) {
      expect(models.has(id)).toBe(false)
    }
  })

  it('shows retail rates including markup, not upstream wholesale prices', () => {
    const prices = Object.fromEntries(
      CLAW402_MODELS.map((m) => [m.id, [m.priceIn, m.priceOut]])
    )
    expect(prices['gpt-6.1-sol']).toEqual([2.875, 11.5])
    expect(prices['gpt-6-luna']).toEqual([0.14375, 0.575])
    expect(prices['gpt-6']).toEqual([14.375, 57.5])
    expect(prices['deepseek-v4-flash']).toEqual([0.345, 1.38])
    expect(prices['glm-5']).toEqual([1.15, 3.68])
  })
})
