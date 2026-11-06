import { describe, expect, it } from 'vitest'
import { formatRequestError, isPaymentOutcomeUnknown } from './requestErrors'

const legacyProtocolError =
  'failed to build trading context: vergex request failed: paid request outcome unknown; refusing automatic replay: Get "https://claw402.ai/api/v1/vergex/holder-winrate-map?secret=hidden": net/http: HTTP/1.x transport connection broken: malformed HTTP response "\\x00\\x00\\x12\\x04"'

describe('readable payment errors', () => {
  it('replaces historical binary dumps without claiming success or no charge', () => {
    for (const error of [
      legacyProtocolError,
      new Error(legacyProtocolError),
      { message: legacyProtocolError },
    ]) {
      expect(isPaymentOutcomeUnknown(error)).toBe(true)
      const text = formatRequestError(error)
      expect(text).toContain('network protocol error')
      expect(text).toContain('Payment result is unconfirmed')
      expect(text).toContain('check the original payment')
      expect(text).not.toMatch(/\\x00|https:|secret=|not charged|successful/)
      expect(text.length).toBeLessThan(300)
    }
  })

  it('preserves unknown payment status for current errors and Chinese UI', () => {
    const error = new Error(
      'paid request outcome unknown; refusing automatic replay: payment response or settlement receipt could not be verified'
    )
    expect(formatRequestError(error)).toContain('Payment result is unconfirmed')
    expect(formatRequestError(error, 'zh')).toContain('支付结果待核对')
    expect(formatRequestError(error, 'id')).toContain(
      'Payment result is unconfirmed'
    )
    expect(formatRequestError(legacyProtocolError, 'zh')).toContain(
      '连接协议异常'
    )
  })

  it('does not turn non-payment network failures into payment failures', () => {
    const error = new Error('malformed HTTP response "\\x00\\x04"')
    expect(isPaymentOutcomeUnknown(error)).toBe(false)
    expect(formatRequestError(error)).toContain('network protocol error')
    expect(formatRequestError(error)).not.toContain('Payment result')
  })

  it('preserves actionable ordinary errors and bounds unexpected output', () => {
    expect(formatRequestError(new Error('snapshot expired'))).toBe(
      'snapshot expired'
    )
    expect(formatRequestError('missing symbol')).toBe('missing symbol')
    expect(formatRequestError('long '.repeat(500)).length).toBeLessThanOrEqual(
      501
    )
    expect(formatRequestError(null)).toBe(
      'Request failed. Check the service status.'
    )
    expect(formatRequestError('bad\u0000response')).toBe('bad response')
  })
})

it('preserves a safe payment diagnostic code', () => {
  expect(
    formatRequestError(
      'paid request outcome unknown [diagnostic:settled_amount_missing]',
      'en'
    )
  ).toContain('Diagnostic: settled_amount_missing')
})

it('preserves safe diagnostics when formatting an error twice', () => {
  const raw = 'paid request outcome unknown [diagnostic:gateway_http_502]'
  expect(formatRequestError(formatRequestError(raw, 'en'), 'en')).toContain(
    'Diagnostic: gateway_http_502'
  )
})

it('explains unsupported model tools without claiming a free failed request', () => {
  const text = formatRequestError(
    'paid request outcome unknown [diagnostic:gateway_tools_unsupported]'
  )
  expect(text).toContain('does not support native tool calls')
  expect(text).toContain('DeepSeek')
  expect(text).toContain('Payment result is unconfirmed')
})
