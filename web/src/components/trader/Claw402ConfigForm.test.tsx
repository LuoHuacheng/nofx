import { useState } from 'react'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Claw402ConfigForm } from './Claw402ConfigForm'
import { ModelConfigModal } from './ModelConfigModal'
const key = `0x${'1'.repeat(64)}`
const address = `0x${'2'.repeat(40)}`
const fetchMock = vi.fn(),
  save = vi.fn(),
  changed = vi.fn()
const reply = (body: unknown, ok = true) => ({ ok, json: async () => body })
function Form({ existing = false }: { existing?: boolean }) {
  const [apiKey, setApiKey] = useState(''),
    [modelName, setModelName] = useState('')
  return (
    <Claw402ConfigForm
      apiKey={apiKey}
      modelName={modelName}
      editingModelId={existing ? 'ai' : null}
      hasExistingKey={existing}
      initialWalletAddress={existing ? address : undefined}
      onApiKeyChange={(value) => {
        changed(value)
        setApiKey(value)
      }}
      onModelNameChange={setModelName}
      onBack={vi.fn()}
      onSubmit={save}
    />
  )
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('fetch', fetchMock)
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  })
  fetchMock.mockImplementation(async (url: string) => {
    if (url.includes('generate')) return reply({ private_key: key, address })
    if (url.includes('validate')) return reply({ valid: true, address })
    if (url.includes('balance'))
      return reply({ address, balance_usdc: '0.000000' })
    throw new Error('Unexpected request')
  })
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
const create = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Create my AI wallet' }))
  await screen.findByText('Save your private key.')
}
const backup = () => {
  fireEvent.click(
    screen.getByLabelText('I saved my private key somewhere safe.')
  )
  fireEvent.click(screen.getByRole('button', { name: 'Continue to add USDC' }))
}
it('makes creation primary without generating on mount or exposing an import field', () => {
  render(<Form />)
  expect(
    screen.getByRole('button', { name: 'Create my AI wallet' })
  ).toBeVisible()
  expect(screen.queryByPlaceholderText('0x…')).toBeNull()
  expect(fetchMock).not.toHaveBeenCalled()
  expect(save).not.toHaveBeenCalled()
})
it('requires backup before funding or saving and polls with only the public address', async () => {
  const view = render(<Form />)
  await create()
  expect(screen.getByLabelText('Your private key')).toHaveAttribute(
    'type',
    'password'
  )
  expect(
    screen.getByRole('button', { name: 'Continue to add USDC' })
  ).toBeDisabled()
  fireEvent.submit(view.container.querySelector('form')!)
  expect(save).not.toHaveBeenCalled()
  expect(changed).not.toHaveBeenCalledWith(key)
  expect(
    fetchMock.mock.calls.filter(([url]) => url.includes('balance'))
  ).toHaveLength(0)
  backup()
  expect(await screen.findByText('0 USDC')).toBeVisible()
  expect(screen.queryByLabelText('Your private key')).toBeNull()
  expect(changed).toHaveBeenLastCalledWith(key)
  const checks = fetchMock.mock.calls.filter(([url]) => url.includes('balance'))
  expect(checks).toHaveLength(1)
  expect(checks[0][0]).toContain(address)
  expect(checks[0][1].body).toBeUndefined()
  expect(screen.getByRole('button', { name: 'Finish AI setup' })).toBeDisabled()
  fireEvent.click(
    screen.getByRole('button', { name: 'Save wallet and fund later' })
  )
  expect(save).toHaveBeenCalledTimes(1)
})
it('detects funding on refresh and enables completion without a payment request', async () => {
  render(<Form />)
  await create()
  backup()
  await screen.findByText('0 USDC')
  fetchMock.mockResolvedValue(reply({ address, balance_usdc: '5.250000' }))
  fireEvent.click(screen.getByRole('button', { name: 'Check deposit' }))
  expect(await screen.findByText('5.25 USDC')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Finish AI setup' }))
  expect(save).toHaveBeenCalledTimes(1)
  expect(
    fetchMock.mock.calls.every(
      ([url]) => url.includes('balance') || url.endsWith('generate')
    )
  ).toBe(true)
})
it('polls while the deposit screen is open and stops after unmount', async () => {
  vi.useFakeTimers()
  const view = render(<Form existing />)
  await act(async () => {})
  expect(fetchMock).toHaveBeenCalledTimes(1)
  fetchMock.mockResolvedValue(reply({ address, balance_usdc: '2.000000' }))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000)
  })
  expect(screen.getByText('2 USDC')).toBeVisible()
  view.unmount()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60000)
  })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})
it('shows unavailable instead of a fake zero or ready state on RPC failure', async () => {
  fetchMock.mockResolvedValue(reply({ error: 'RPC unavailable' }, false))
  render(<Form existing />)
  expect(await screen.findByText('Balance unavailable')).toBeVisible()
  expect(screen.queryByText('0 USDC')).toBeNull()
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
})
it('imports explicitly without validating every keystroke or generating a new wallet', async () => {
  render(<Form />)
  fireEvent.click(
    screen.getByRole('button', { name: /I already have a wallet/ })
  )
  fireEvent.change(screen.getByLabelText('Private key', { exact: true }), {
    target: { value: key },
  })
  expect(fetchMock).not.toHaveBeenCalled()
  fireEvent.click(
    screen.getByRole('button', { name: 'Continue with this wallet' })
  )
  await screen.findByText('0 USDC')
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/wallet/validate',
    expect.objectContaining({
      body: JSON.stringify({ private_key: key, derive_only: true }),
    })
  )
  expect(fetchMock.mock.calls.some(([url]) => url.includes('generate'))).toBe(
    false
  )
  expect(changed).toHaveBeenLastCalledWith(key)
})
it('preserves a stored key when changing the model', async () => {
  render(<Form existing />)
  await screen.findByText('0 USDC')
  fireEvent.click(screen.getByText('Change model'))
  fireEvent.click(screen.getByRole('button', { name: /DeepSeek-V4 Pro/ }))
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(save).toHaveBeenCalledTimes(1)
  expect(changed).not.toHaveBeenCalled()
  expect(fetchMock.mock.calls.every(([url]) => url.includes('balance'))).toBe(
    true
  )
})
it('handles generation and clipboard failures without pretending success', async () => {
  fetchMock.mockRejectedValueOnce(new Error('offline'))
  render(<Form />)
  fireEvent.click(screen.getByRole('button', { name: 'Create my AI wallet' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Could not create a wallet'
  )
  await create()
  vi.mocked(navigator.clipboard.writeText).mockRejectedValueOnce(
    new Error('blocked')
  )
  fireEvent.click(screen.getByRole('button', { name: 'Copy private key' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Clipboard is unavailable'
  )
  expect(screen.queryByText('Private key copied')).toBeNull()
})
it('does not generate duplicate wallets while a request is pending', async () => {
  let resolve: (value: unknown) => void = () => {}
  fetchMock.mockReturnValueOnce(
    new Promise((done) => {
      resolve = done
    })
  )
  render(<Form />)
  const button = screen.getByRole('button', { name: 'Create my AI wallet' })
  fireEvent.click(button)
  fireEvent.click(button)
  expect(fetchMock).toHaveBeenCalledTimes(1)
  await act(async () => {
    resolve(reply({ address, private_key: key }))
  })
  expect(screen.getByText('Save your private key.')).toBeVisible()
})
it('opens Add AI on wallet creation and reuses a configured wallet instead of replacing it', async () => {
  const template = {
    id: 'claw402',
    name: 'Claw402',
    provider: 'claw402',
    enabled: false,
  }
  const props = {
    allModels: [template],
    editingModelId: null,
    onSave: save,
    onDelete: vi.fn(),
    onClose: vi.fn(),
    language: 'en' as const,
  }
  const view = render(<ModelConfigModal {...props} configuredModels={[]} />)
  expect(
    screen.getByRole('button', { name: 'Create my AI wallet' })
  ).toBeVisible()
  view.unmount()
  render(
    <ModelConfigModal
      {...props}
      configuredModels={[
        {
          ...template,
          id: 'saved-wallet',
          has_api_key: true,
          walletAddress: address,
        },
      ]}
    />
  )
  expect(await screen.findByText('Your AI wallet')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith('saved-wallet', '', undefined, undefined)
  )
  expect(fetchMock.mock.calls.every(([url]) => url.includes('balance'))).toBe(
    true
  )
})
it('keeps the setup open and retryable when saving fails', async () => {
  const template = {
    id: 'claw402',
    name: 'Claw402',
    provider: 'claw402',
    enabled: true,
  }
  save.mockRejectedValueOnce(new Error('save failed'))
  render(
    <ModelConfigModal
      allModels={[template]}
      configuredModels={[
        { ...template, has_api_key: true, walletAddress: address },
      ]}
      editingModelId="claw402"
      onSave={save}
      onDelete={vi.fn()}
      onClose={vi.fn()}
      language="en"
    />
  )
  await screen.findByText('0 USDC')
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Could not save this AI connection'
  )
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  expect(screen.getByText('Your AI wallet')).toBeVisible()
})
it('does not carry a wallet private key into a different AI provider form', async () => {
  const claw = {
    id: 'claw402',
    name: 'Claw402',
    provider: 'claw402',
    enabled: false,
  }
  const direct = {
    id: 'deepseek',
    name: 'DeepSeek',
    provider: 'deepseek',
    enabled: false,
  }
  const view = render(
    <ModelConfigModal
      allModels={[claw, direct]}
      configuredModels={[]}
      editingModelId={null}
      onSave={save}
      onDelete={vi.fn()}
      onClose={vi.fn()}
      language="en"
    />
  )
  await create()
  backup()
  await screen.findByText('0 USDC')
  fireEvent.click(
    screen.getByRole('button', { name: 'Choose another AI provider' })
  )
  fireEvent.click(
    screen.getByText('DeepSeek', { exact: true }).closest('button')!
  )
  expect(
    Array.from(view.container.querySelectorAll('input')).some(
      (input) => input.value === key
    )
  ).toBe(false)
  expect(save).not.toHaveBeenCalled()
})
