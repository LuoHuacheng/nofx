import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Check, Copy, Eye, EyeOff, Wallet, ArrowRight } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { AIWalletBackup, downloadWalletBackup } from '../common/AIWalletBackup'
import { CLAW402_MODELS, DEFAULT_CLAW402_MODEL } from './model-constants'
import './Claw402ConfigForm.css'

type Phase = 'create' | 'import' | 'backup' | 'fund'
type WalletSource = 'saved' | 'generated' | 'imported'
type WalletDetails = { address: string; key: string; source: WalletSource }

async function walletRequest(path: string, init: RequestInit = {}) {
  const response = await fetch(`/api/wallet/${path}`, {
    ...init,
    cache: 'no-store',
    signal: init.signal || AbortSignal.timeout(15000),
  })
  if (!response.ok) throw new Error('Wallet request failed')
  return response.json()
}

export function Claw402ConfigForm({
  apiKey,
  modelName,
  editingModelId,
  walletModelID,
  hasExistingKey,
  initialWalletAddress,
  onApiKeyChange,
  onModelNameChange,
  onBack,
  onSubmit,
}: {
  apiKey: string
  modelName: string
  editingModelId: string | null
  walletModelID?: string
  hasExistingKey?: boolean
  initialWalletAddress?: string
  onApiKeyChange: (value: string) => void
  onModelNameChange: (value: string) => void
  onBack: () => void
  onSubmit: (event: FormEvent) => void
}) {
  const [phase, setPhase] = useState<Phase>(hasExistingKey ? 'fund' : 'create')
  const [wallet, setWallet] = useState<WalletDetails | null>(
    hasExistingKey
      ? { address: initialWalletAddress || '', key: '', source: 'saved' }
      : null
  )
  const [importKey, setImportKey] = useState(apiKey)
  const [backedUp, setBackedUp] = useState(false)
  const [revealed, setRevealed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const [balance, setBalance] = useState<number | null>(null)
  const [checking, setChecking] = useState(false)
  const [balanceError, setBalanceError] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const pending = useRef(false)
  const mounted = useRef(true)
  const copyTimer = useRef<ReturnType<typeof setTimeout>>()
  const model = CLAW402_MODELS.find(
    (item) => item.id === (modelName || DEFAULT_CLAW402_MODEL)
  )
  const address = phase === 'fund' ? wallet?.address : ''
  const canSave =
    phase === 'fund' &&
    Boolean(wallet && (wallet.source === 'saved' || wallet.key === apiKey))

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      clearTimeout(copyTimer.current)
    }
  }, [])

  // Keep transient keys in memory only. A reload must not silently discard a
  // generated wallet before the user has had a chance to save its backup.
  useEffect(() => {
    if (wallet?.source !== 'generated') return
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [wallet?.source])

  useEffect(() => {
    setBalance(null)
    setBalanceError(false)
    if (!address) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    let controller: AbortController
    const check = async () => {
      if (!active) return
      if (document.visibilityState === 'hidden') {
        timer = setTimeout(check, 15000)
        return
      }
      controller = new AbortController()
      const timeout = setTimeout(() => controller.abort(), 15000)
      setChecking(true)
      try {
        const data = await walletRequest(
          `balance?address=${encodeURIComponent(address)}`,
          { signal: controller.signal }
        )
        const value = Number(data.balance_usdc)
        if (
          typeof data.balance_usdc !== 'string' ||
          !data.balance_usdc.trim() ||
          !Number.isFinite(value) ||
          value < 0 ||
          data.address?.toLowerCase() !== address.toLowerCase()
        ) {
          throw new Error('Invalid balance')
        }
        if (active) {
          setBalance(value)
          setBalanceError(false)
        }
      } catch {
        if (active) {
          setBalance(null)
          setBalanceError(true)
        }
      } finally {
        clearTimeout(timeout)
        if (active) {
          setChecking(false)
          timer = setTimeout(check, 15000)
        }
      }
    }
    void check()
    return () => {
      active = false
      clearTimeout(timer)
      controller?.abort()
    }
  }, [address, refresh])

  const prepare = async (source: 'generated' | 'imported') => {
    if (pending.current) return
    const key = importKey.trim()
    if (source === 'imported' && !/^0x[0-9a-fA-F]{64}$/.test(key)) {
      setError(
        'Enter a valid private key: 0x followed by 64 hexadecimal characters.'
      )
      return
    }
    pending.current = true
    setBusy(true)
    setError('')
    try {
      const data =
        source === 'generated'
          ? await walletRequest('generate', { method: 'POST' })
          : await walletRequest('validate', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ private_key: key, derive_only: true }),
            })
      const privateKey = source === 'generated' ? data.private_key : key
      if (
        !/^0x[0-9a-fA-F]{40}$/.test(data.address || '') ||
        !/^0x[0-9a-fA-F]{64}$/.test(privateKey || '') ||
        (source === 'imported' && !data.valid)
      ) {
        throw new Error('Invalid wallet')
      }
      if (!mounted.current) return
      setWallet({ address: data.address, key: privateKey, source })
      setImportKey('')
      setBackedUp(false)
      setRevealed(false)
      setCopied('')
      if (source === 'generated') {
        onApiKeyChange('')
        setPhase('backup')
      } else {
        onApiKeyChange(privateKey)
        setPhase('fund')
      }
    } catch {
      if (mounted.current)
        setError(
          source === 'generated'
            ? 'Could not create a wallet. Check your NOFX connection and try again.'
            : 'Could not import this wallet. Check the private key and your NOFX connection.'
        )
    } finally {
      pending.current = false
      if (mounted.current) setBusy(false)
    }
  }

  const copy = async (value: string, kind: string) => {
    setError('')
    try {
      await navigator.clipboard.writeText(value)
      if (!mounted.current) return
      setCopied(kind)
      clearTimeout(copyTimer.current)
      copyTimer.current = setTimeout(() => setCopied(''), 2500)
    } catch {
      if (mounted.current)
        setError('Clipboard is unavailable. Select and copy the text manually.')
    }
  }

  const startOver = () => {
    setWallet(null)
    setPhase('create')
    setError('')
    setCopied('')
    setRevealed(false)
    setImportKey('')
    onApiKeyChange('')
  }

  const step = phase === 'backup' ? 1 : phase === 'fund' ? 2 : 0
  return (
    <form
      className="ai-wallet-setup"
      onSubmit={(event) => {
        event.preventDefault()
        if (canSave && !pending.current) onSubmit(event)
      }}
    >
      <details className="ai-wallet-model">
        <summary>
          <span>
            <small>AI model</small>
            <strong>{model?.name || modelName || DEFAULT_CLAW402_MODEL}</strong>
          </span>
          <span className="ai-wallet-muted">Change model</span>
        </summary>
        <p>One AI wallet for every model. Switch models whenever you like.</p>
        <div className="ai-wallet-model-grid">
          {CLAW402_MODELS.map((item) => (
            <button
              key={item.id}
              type="button"
              aria-pressed={item.id === (modelName || DEFAULT_CLAW402_MODEL)}
              onClick={() => onModelNameChange(item.id)}
            >
              <strong>{item.name}</strong>
              <span>
                ${item.priceIn} input · ${item.priceOut} output / 1M tokens
              </span>
            </button>
          ))}
        </div>
        <p className="ai-wallet-muted">
          Indicative rates plus a $0.0012 settlement fee per request. The live
          quote determines the spending limit; usage and model affect the final
          cost.
        </p>
      </details>

      {wallet?.source !== 'saved' && (
        <ol className="ai-wallet-steps" aria-label="AI wallet setup progress">
          {['Create wallet', 'Save private key', 'Add USDC'].map(
            (label, index) => (
              <li
                key={label}
                aria-current={step === index ? 'step' : undefined}
                data-complete={step > index}
              >
                <span>{step > index ? <Check size={14} /> : index + 1}</span>
                {label}
              </li>
            )
          )}
        </ol>
      )}

      {phase === 'create' && (
        <section className="ai-wallet-welcome">
          <div className="ai-wallet-icon">
            <Wallet size={28} />
          </div>
          <h2>A wallet for your AI.</h2>
          <p>
            Create a dedicated wallet, save its private key, then add a little
            USDC on Base to pay for AI and data. No browser wallet or exchange
            account needed.
          </p>
          <button
            className="ai-wallet-primary"
            type="button"
            disabled={busy}
            onClick={() => void prepare('generated')}
          >
            {busy ? 'Creating your wallet…' : 'Create my AI wallet'}
            <ArrowRight size={17} />
          </button>
          <button
            className="ai-wallet-link"
            type="button"
            disabled={busy}
            onClick={() => {
              setPhase('import')
              setError('')
            }}
          >
            I already have a wallet · Import private key
          </button>
          {hasExistingKey && (
            <button
              className="ai-wallet-link"
              type="button"
              disabled={busy}
              onClick={() => {
                setWallet({
                  address: initialWalletAddress || '',
                  key: '',
                  source: 'saved',
                })
                setPhase('fund')
              }}
            >
              Keep my saved wallet
            </button>
          )}
          <p className="ai-wallet-caption">
            Creating a wallet is free. No funds move until you use a paid
            feature.
          </p>
        </section>
      )}

      {phase === 'import' && (
        <section className="ai-wallet-panel">
          <h2>Use an existing wallet</h2>
          <p>Import a dedicated EVM wallet for AI payments on Base.</p>
          <label htmlFor="ai-wallet-import">Private key</label>
          <input
            id="ai-wallet-import"
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={importKey}
            onChange={(event) => {
              setImportKey(event.target.value)
              setError('')
            }}
            placeholder="0x…"
            disabled={busy}
          />
          <button
            className="ai-wallet-primary"
            type="button"
            disabled={busy || !importKey.trim()}
            onClick={() => void prepare('imported')}
          >
            {busy ? 'Checking wallet…' : 'Continue with this wallet'}
          </button>
          <button
            className="ai-wallet-link"
            type="button"
            disabled={busy}
            onClick={startOver}
          >
            Create a new wallet instead
          </button>
        </section>
      )}

      {phase === 'backup' && wallet && (
        <section className="ai-wallet-panel">
          <h2>Save your private key.</h2>
          <p>
            This key gives full control of your wallet. Save it in your password
            manager before adding funds. Keep it private; NOFX support cannot
            recover it for you.
          </p>
          <label htmlFor="ai-wallet-backup">Your private key</label>
          <div className="ai-wallet-key">
            <input
              id="ai-wallet-backup"
              type={revealed ? 'text' : 'password'}
              value={wallet.key}
              readOnly
              autoComplete="off"
              spellCheck={false}
            />
            <button
              type="button"
              onClick={() => setRevealed(!revealed)}
              aria-label={revealed ? 'Hide private key' : 'Show private key'}
            >
              {revealed ? <EyeOff size={18} /> : <Eye size={18} />}
            </button>
          </div>
          <button
            className="ai-wallet-secondary"
            type="button"
            onClick={() => void copy(wallet.key, 'key')}
          >
            <Copy size={16} />
            {copied === 'key' ? 'Private key copied' : 'Copy private key'}
          </button>
          <button
            className="ai-wallet-secondary"
            type="button"
            onClick={() => {
              try {
                downloadWalletBackup(wallet.address, wallet.key)
              } catch {
                setError(
                  'Download failed. Copy your key to a password manager instead.'
                )
              }
            }}
          >
            Download backup
          </button>
          <p className="ai-wallet-caption">
            The backup file contains your private key and is not encrypted.
            Store it securely.
          </p>
          <label className="ai-wallet-check">
            <input
              type="checkbox"
              checked={backedUp}
              onChange={(event) => setBackedUp(event.target.checked)}
            />
            I saved my private key somewhere safe.
          </label>
          <button
            className="ai-wallet-primary"
            type="button"
            disabled={!backedUp}
            onClick={() => {
              onApiKeyChange(wallet.key)
              setRevealed(false)
              setCopied('')
              setPhase('fund')
            }}
          >
            Continue to add USDC
            <ArrowRight size={17} />
          </button>
        </section>
      )}

      {phase === 'fund' && wallet && (
        <section className="ai-wallet-panel">
          <div className="ai-wallet-heading">
            <div>
              <h2>
                {wallet.source === 'saved'
                  ? 'Your AI wallet'
                  : 'Add USDC on Base.'}
              </h2>
              <p>For AI and data fees. Your trading funds stay separate.</p>
            </div>
            <span className="ai-wallet-network">Base network</span>
          </div>
          {address ? (
            <>
              <div className="ai-wallet-deposit">
                <div className="ai-wallet-qr">
                  <QRCodeSVG
                    value={address}
                    size={112}
                    title="AI wallet deposit address"
                  />
                </div>
                <div>
                  <label>Send USDC to this address</label>
                  <code>{address}</code>
                  <button
                    className="ai-wallet-secondary"
                    type="button"
                    onClick={() => void copy(address, 'address')}
                  >
                    <Copy size={16} />
                    {copied === 'address'
                      ? 'Address copied'
                      : 'Copy deposit address'}
                  </button>
                </div>
              </div>
              <ol className="ai-wallet-funding-guide">
                <li>Open Withdraw or Send in your exchange or wallet.</li>
                <li>
                  Select <strong>USDC</strong> and the <strong>Base</strong>{' '}
                  network.
                </li>
                <li>
                  Paste this address and send a small amount to start. Check the
                  sender’s minimum and withdrawal fee.
                </li>
              </ol>
              <p className="ai-wallet-network-note">
                Send USDC on Base only. Other networks or assets will not fund
                this AI wallet.
              </p>
              <div className="ai-wallet-balance" role="status">
                <div>
                  <small>USDC on Base</small>
                  <strong>
                    {balance !== null
                      ? `${balance.toLocaleString('en-US', { maximumFractionDigits: 6 })} USDC`
                      : balanceError
                        ? 'Balance unavailable'
                        : 'Checking balance…'}
                  </strong>
                  <span>
                    {balanceError
                      ? 'Could not reach Base. You can save and check again later.'
                      : balance !== null && balance > 0
                        ? 'Funds detected. Paid requests use this balance.'
                        : 'Waiting for USDC. Checks automatically every 15 seconds.'}
                  </span>
                </div>
                <button
                  className="ai-wallet-secondary"
                  type="button"
                  disabled={checking}
                  onClick={() => setRefresh((value) => value + 1)}
                >
                  {checking ? 'Checking…' : 'Check deposit'}
                </button>
              </div>
            </>
          ) : (
            <p role="status">
              The saved wallet address could not be loaded. You can still save
              model changes without replacing its private key.
            </p>
          )}
          <AIWalletBackup
            key={wallet.address}
            modelID={walletModelID || editingModelId || undefined}
            address={wallet.address}
            privateKey={wallet.source === 'saved' ? undefined : wallet.key}
          />
          <div className="ai-wallet-finish">
            <button
              className="ai-wallet-primary"
              type="submit"
              disabled={
                !canSave ||
                (wallet.source !== 'saved' &&
                  !(balance !== null && balance > 0))
              }
            >
              {wallet.source === 'saved' ? 'Save changes' : 'Finish AI setup'}
              <ArrowRight size={17} />
            </button>
            {wallet.source !== 'saved' &&
              !(balance !== null && balance > 0) && (
                <button
                  className="ai-wallet-link"
                  type="submit"
                  disabled={!canSave}
                >
                  Save wallet and fund later
                </button>
              )}
          </div>
          <p className="ai-wallet-caption">
            Saving connects AI only. It does not place trades or start
            Autopilot.
          </p>
          <details className="ai-wallet-advanced">
            <summary>Use a different wallet</summary>
            <p>
              Your current wallet and its funds remain yours. The replacement is
              saved only when you finish setup.
            </p>
            <button
              className="ai-wallet-secondary"
              type="button"
              onClick={startOver}
            >
              Set up a different wallet
            </button>
          </details>
        </section>
      )}
      {error && (
        <p role="alert" className="ai-wallet-error">
          {error}
        </p>
      )}
      <div className="ai-wallet-custody">
        <strong>Your wallet. Your control.</strong>
        <p>
          New wallets are generated by your own NOFX installation. On save, NOFX
          stores the private key encrypted on your server to sign AI payments.
          It is not sent to Claw402 or AI providers. Keep a backup; anyone with
          the key can spend the funds.
        </p>
      </div>
      <button
        className="ai-wallet-link"
        type="button"
        disabled={busy}
        onClick={onBack}
      >
        {editingModelId ? 'Cancel' : 'Choose another AI provider'}
      </button>
    </form>
  )
}
