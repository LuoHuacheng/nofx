function errorText(error: unknown): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string') return error
  if (
    error &&
    typeof error === 'object' &&
    'message' in error &&
    typeof error.message === 'string'
  ) {
    return error.message
  }
  return ''
}

export function isPaymentOutcomeUnknown(error: unknown): boolean {
  return /paid request outcome unknown|payment result is unconfirmed/i.test(
    errorText(error)
  )
}

// Covers both current responses and historical decision records containing raw
// HTTP/2 frames. Displaying a readable error must never imply a successful call
// or an unpaid request, nor modify the stored audit record.
export function formatRequestError(
  error: unknown,
  language: Language = 'en'
): string {
  const raw = errorText(error)
  const protocol =
    /malformed http response|http protocol error|http\/1\.x transport connection broken/i.test(
      raw
    )
  if (isPaymentOutcomeUnknown(raw)) {
    const diagnostic = raw.match(
      /(?:\[diagnostic:|Diagnostic: )([a-z_0-9]+)/i
    )?.[1]
    const detail = diagnostic ? ` Diagnostic: ${diagnostic}.` : ''
    if (language === 'zh') {
      return `${protocol ? 'NOFX 与支付网关的连接协议异常，未能读取响应。' : '未能确认付费请求的响应或结算结果。'}支付结果待核对；不会自动重试，请先核对原付款，勿重复付款。`
    }
    const cause =
      diagnostic === 'gateway_tools_unsupported'
        ? 'The selected model endpoint does not support native tool calls. Use a compatible planning model such as DeepSeek.'
        : protocol
          ? 'NOFX could not read the gateway response because of a network protocol error.'
          : 'The paid response or settlement could not be verified.'
    return `${cause} Payment result is unconfirmed. No automatic retry; check the original payment before making another request.${detail}`
  }
  if (protocol || /(?:\\x[\da-f]{2}){2,}/i.test(raw)) {
    return language === 'zh'
      ? '网络协议异常，未能读取响应。请检查客户端连接配置。'
      : 'A network protocol error prevented the response from being read. Check the client connection configuration.'
  }
  const text = raw
    .replace(/\p{Cc}/gu, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  if (!text)
    return language === 'zh'
      ? '请求失败，请检查服务状态。'
      : 'Request failed. Check the service status.'
  return text.length > 500 ? `${text.slice(0, 500)}…` : text
}
import type { Language } from '../i18n/translations'
