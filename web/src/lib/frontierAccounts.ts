import type { Exchange } from '../types'

export function accountLabel(account: Exchange, accounts: Exchange[] = []) {
  const name =
    account.account_name?.trim() || account.name || account.exchange_type
  const duplicate = accounts.some(
    (other) =>
      other.id !== account.id &&
      other.exchange_type === account.exchange_type &&
      (other.account_name?.trim() || other.name || other.exchange_type) === name
  )
  return duplicate ? `${name} · ${account.id.slice(0, 8)}` : name
}

function preferenceKey(venue: string) {
  return [
    'frontier-account-v1',
    localStorage.getItem('user_id') || '',
    venue,
  ].join('/')
}

export function preferredAccount(accounts: Exchange[], venue: string): string {
  const matching = accounts.filter((account) => account.exchange_type === venue)
  try {
    const saved: unknown = JSON.parse(
      localStorage.getItem(preferenceKey(venue)) || 'null'
    )
    if (saved === '' || matching.some((account) => account.id === saved))
      return saved as string
  } catch {
    /* Storage may be unavailable. */
  }
  return matching[0]?.id || ''
}

export function rememberAccount(venue: string, id: string) {
  try {
    localStorage.setItem(preferenceKey(venue), JSON.stringify(id))
  } catch {
    /* Storage may be unavailable. */
  }
}

export const frontierVenues = ['binance', 'hyperliquid', 'okx', 'bitget']
export const apiKeyVenues = ['binance', 'okx', 'bitget']
export function venueName(venue: string) {
  return (
    (
      {
        binance: 'Binance',
        hyperliquid: 'Hyperliquid',
        okx: 'OKX',
        bitget: 'Bitget',
      } as Record<string, string>
    )[venue] || venue
  )
}
export function defaultMarket(venue: string) {
  return venue === 'hyperliquid'
    ? 'BTC'
    : venue === 'okx'
      ? 'BTC-USDT-SWAP'
      : 'BTCUSDT'
}
