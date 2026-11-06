import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { AIWalletBackup } from './AIWalletBackup'
vi.mock('../../lib/api/helpers', () => ({
  getAuthHeaders: () => ({
    Authorization: 'Bearer fixture',
    'Content-Type': 'application/json',
  }),
}))
const address = `0x${'2'.repeat(40)}`
const privateKey = `0x${'1'.repeat(64)}`
const request = vi.fn()
beforeEach(() => {
  vi.stubGlobal('fetch', request)
  request.mockReset().mockResolvedValue({
    ok: true,
    json: async () => ({ address, private_key: privateKey }),
  })
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  })
  URL.createObjectURL = vi.fn().mockReturnValue('blob:backup')
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
function open() {
  return render(<AIWalletBackup modelID="owned-wallet" address={address} />)
}
it('exports only on explicit reveal, then offers copy and a clearly unencrypted download', async () => {
  open()
  expect(request).not.toHaveBeenCalled()
  expect(screen.queryByLabelText('Wallet private key')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  expect(await screen.findByLabelText('Wallet private key')).toHaveValue(
    privateKey
  )
  expect(request).toHaveBeenCalledWith(
    '/api/wallet/export',
    expect.objectContaining({
      method: 'POST',
      cache: 'no-store',
      headers: expect.objectContaining({ Authorization: 'Bearer fixture' }),
      body: JSON.stringify({ model_id: 'owned-wallet', address }),
    })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Copy private key' }))
  await screen.findByText('Private key copied')
  expect(navigator.clipboard.writeText).toHaveBeenCalledWith(privateKey)
  fireEvent.click(screen.getByRole('button', { name: 'Download backup' }))
  const blob = vi.mocked(URL.createObjectURL).mock.calls[0][0] as Blob
  const contents = await new Promise<string>((resolve) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.readAsText(blob)
  })
  expect(contents).toContain(privateKey)
  expect(contents).toContain(address)
  expect(contents).toContain('not encrypted')
  expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Hide private key' }))
  expect(screen.queryByLabelText('Wallet private key')).toBeNull()
  expect(request).toHaveBeenCalledTimes(1)
})
it('clears the key after 60 seconds and on a wallet change', async () => {
  vi.useFakeTimers()
  const view = open()
  await act(async () =>
    fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  )
  expect(screen.getByLabelText('Wallet private key')).toHaveValue(privateKey)
  await act(async () => vi.advanceTimersByTimeAsync(60000))
  expect(screen.queryByLabelText('Wallet private key')).toBeNull()
  await act(async () =>
    fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  )
  view.rerender(<AIWalletBackup modelID="other-wallet" address={address} />)
  expect(screen.queryByLabelText('Wallet private key')).toBeNull()
})
it('rejects another wallet response and leaves no secret or download visible', async () => {
  request.mockResolvedValue({
    ok: true,
    json: async () => ({
      address: `0x${'3'.repeat(40)}`,
      private_key: privateKey,
    }),
  })
  open()
  fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  await screen.findByRole('alert')
  expect(screen.queryByLabelText('Wallet private key')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Download backup' })).toBeNull()
})
it('ignores a delayed export after leaving the tab', async () => {
  let resolve!: (value: unknown) => void
  request.mockImplementation(
    () =>
      new Promise((r) => {
        resolve = r
      })
  )
  open()
  fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    value: 'hidden',
  })
  fireEvent(document, new Event('visibilitychange'))
  await act(async () =>
    resolve({
      ok: true,
      json: async () => ({ address, private_key: privateKey }),
    })
  )
  expect(screen.queryByLabelText('Wallet private key')).toBeNull()
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    value: 'visible',
  })
})
it('shows export failure without retry or false copy success', async () => {
  request.mockResolvedValueOnce({ ok: false })
  open()
  fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  await screen.findByRole('alert')
  expect(request).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Show private key' }))
  await screen.findByLabelText('Wallet private key')
  vi.mocked(navigator.clipboard.writeText).mockRejectedValue(
    new Error('denied')
  )
  fireEvent.click(screen.getByRole('button', { name: 'Copy private key' }))
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent('Copy failed')
  )
  expect(screen.queryByText('Private key copied')).toBeNull()
})
