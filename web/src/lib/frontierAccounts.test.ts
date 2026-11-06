import { beforeEach, expect, it } from 'vitest'
import type { Exchange } from '../types'
import {
  accountLabel,
  preferredAccount,
  rememberAccount,
} from './frontierAccounts'
const accounts = [
  {
    id: 'aaaa1111-1',
    name: 'Binance Futures',
    account_name: 'Main',
    exchange_type: 'binance',
  },
  {
    id: 'bbbb2222-2',
    name: 'Binance Futures',
    account_name: 'Main',
    exchange_type: 'binance',
  },
] as Exchange[]
beforeEach(() => localStorage.clear())
it('disambiguates duplicate account names without displaying credentials', () => {
  expect(accountLabel(accounts[0], accounts)).toBe('Main · aaaa1111')
  expect(accountLabel(accounts[1], accounts)).toBe('Main · bbbb2222')
})
it('isolates saved selections by user and falls back when an account is removed', () => {
  localStorage.setItem('user_id', 'alice')
  rememberAccount('binance', accounts[1].id)
  expect(preferredAccount(accounts, 'binance')).toBe(accounts[1].id)
  localStorage.setItem('user_id', 'bob')
  expect(preferredAccount(accounts, 'binance')).toBe(accounts[0].id)
  localStorage.setItem('user_id', 'alice')
  expect(preferredAccount([accounts[0]], 'binance')).toBe(accounts[0].id)
  rememberAccount('binance', '')
  expect(preferredAccount(accounts, 'binance')).toBe('')
})
