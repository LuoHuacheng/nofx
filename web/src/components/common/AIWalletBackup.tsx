import { useCallback, useEffect, useRef, useState } from 'react'
import { Copy, Download, Eye, EyeOff, KeyRound } from 'lucide-react'
import { getAuthHeaders } from '../../lib/api/helpers'
import './AIWalletBackup.css'

export function downloadWalletBackup(address: string, privateKey: string) {
  const text = [
    'NOFX AI wallet backup — keep private',
    'Network: Base',
    `Address: ${address}`,
    `Private key: ${privateKey}`,
    '',
    'This file is not encrypted. Anyone with this private key can spend the wallet funds.',
    'Store it in a secure password manager or offline backup. Never share it with support.',
  ].join('\n')
  const url = URL.createObjectURL(
    new Blob([text], { type: 'text/plain;charset=utf-8' })
  )
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = `NOFX-AI-wallet-${address.slice(0, 8)}.txt`
    link.click()
  } finally {
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
}

export function AIWalletBackup({
  modelID,
  address,
  privateKey,
}: {
  modelID?: string
  address: string
  privateKey?: string
}) {
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout>>()
  const hide = useCallback(() => {
    pending.current?.abort()
    pending.current = null
    clearTimeout(hideTimer.current)
    setSecret('')
    setCopied(false)
    setBusy(false)
  }, [])

  useEffect(() => {
    hide()
    setError('')
    const onHidden = () => {
      if (document.visibilityState === 'hidden') hide()
    }
    document.addEventListener('visibilitychange', onHidden)
    return () => {
      pending.current?.abort()
      clearTimeout(hideTimer.current)
      document.removeEventListener('visibilitychange', onHidden)
    }
  }, [modelID, address, privateKey, hide])

  async function reveal() {
    if (pending.current) return
    const controller = new AbortController()
    pending.current = controller
    const timeout = setTimeout(() => controller.abort(), 15000)
    setBusy(true)
    setError('')
    try {
      let key = privateKey
      if (!key) {
        const response = await fetch('/api/wallet/export', {
          method: 'POST',
          headers: getAuthHeaders(),
          cache: 'no-store',
          signal: controller.signal,
          body: JSON.stringify({ model_id: modelID, address }),
        })
        if (!response.ok) throw new Error('Export unavailable')
        const data = await response.json()
        if (data.address?.toLowerCase() !== address.toLowerCase())
          throw new Error('Wallet changed')
        key = data.private_key
      }
      if (controller.signal.aborted) return
      if (!key || !/^0x[0-9a-fA-F]{64}$/.test(key))
        throw new Error('Invalid backup')
      setSecret(key)
      hideTimer.current = setTimeout(hide, 60000)
    } catch {
      if (pending.current === controller)
        setError(
          'Could not export this wallet. Check your login and reopen wallet settings.'
        )
    } finally {
      clearTimeout(timeout)
      if (pending.current === controller) {
        pending.current = null
        setBusy(false)
      }
    }
  }

  return (
    <section className="ai-wallet-backup" aria-label="Private key backup">
      <div className="ai-wallet-backup-heading">
        <KeyRound size={18} />
        <strong>Private key backup</strong>
      </div>
      <p>
        Back up this wallet to keep control of your funds. Anyone with the key
        can spend them. Never share it.
      </p>
      {secret ? (
        <>
          <label>
            Wallet private key
            <input
              aria-label="Wallet private key"
              readOnly
              value={secret}
              autoComplete="off"
              spellCheck={false}
            />
          </label>
          <div className="ai-wallet-backup-actions">
            <button
              type="button"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(secret)
                  setCopied(true)
                  setError('')
                } catch {
                  setError('Copy failed. Select the key and copy it manually.')
                }
              }}
            >
              <Copy size={15} />
              {copied ? 'Private key copied' : 'Copy private key'}
            </button>
            <button
              type="button"
              onClick={() => {
                try {
                  downloadWalletBackup(address, secret)
                } catch {
                  setError(
                    'Download failed. Copy the key to your password manager instead.'
                  )
                }
              }}
            >
              <Download size={15} />
              Download backup
            </button>
            <button type="button" onClick={hide}>
              <EyeOff size={15} />
              Hide private key
            </button>
          </div>
          <small>
            The downloaded file is not encrypted. The key hides here after 60
            seconds or when you leave this tab.
          </small>
        </>
      ) : (
        <button
          type="button"
          disabled={busy || (!modelID && !privateKey) || !address}
          onClick={() => void reveal()}
        >
          <Eye size={16} />
          {busy ? 'Loading private key…' : 'Show private key'}
        </button>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  )
}
