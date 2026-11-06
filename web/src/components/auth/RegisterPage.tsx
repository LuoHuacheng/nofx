import React, { useEffect, useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import PasswordChecklist from 'react-password-checklist'
import { toast } from 'sonner'
import { useAuth } from '../../contexts/AuthContext'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { getSystemConfig } from '../../lib/config'
import { BrandLogo } from '../common/BrandLogo'
import { DeepVoidBackground } from '../common/DeepVoidBackground'
import { RegistrationDisabled } from './RegistrationDisabled'
import { WhitelistFullPage } from '../common/WhitelistFullPage'

export function RegisterPage() {
  const { language } = useLanguage()
  const { register } = useAuth()
  const navigate = useNavigate()
  const [view, setView] = useState<'register' | 'whitelist-full'>('register')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [betaCode, setBetaCode] = useState('')
  const [betaMode, setBetaMode] = useState(false)
  const [registrationEnabled, setRegistrationEnabled] = useState(true)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [passwordValid, setPasswordValid] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [showConfirmPassword, setShowConfirmPassword] = useState(false)

  useEffect(() => {
    getSystemConfig()
      .then((config) => {
        setBetaMode(config.beta_mode || false)
        setRegistrationEnabled(config.initialized === false)
      })
      .catch((err) => {
        console.error('Failed to fetch system config:', err)
      })
  }, [])

  if (!registrationEnabled) {
    return <RegistrationDisabled />
  }

  if (view === 'whitelist-full') {
    return <WhitelistFullPage onBack={() => setView('register')} />
  }

  const handleRegister = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (!passwordValid) {
      setError(t('passwordNotMeetRequirements', language))
      return
    }

    if (betaMode && !betaCode.trim()) {
      setError('A beta code is required to register during the closed beta')
      return
    }

    setLoading(true)
    try {
      const result = await register(
        email,
        password,
        betaCode.trim() || undefined
      )

      const isWhitelistError = (msg: string) => {
        const lowerMsg = msg.toLowerCase()
        return (
          lowerMsg.includes('whitelist') ||
          lowerMsg.includes('capacity') ||
          lowerMsg.includes('limit') ||
          lowerMsg.includes('permission denied') ||
          lowerMsg.includes('not on whitelist')
        )
      }

      if (!result.success) {
        const msg = result.message || t('registrationFailed', language)
        if (isWhitelistError(msg)) {
          setView('whitelist-full')
          return
        }
        setError(msg)
        toast.error(msg)
      }
      // success path is handled in AuthContext (auto login + navigation)
    } catch (e) {
      console.error('Registration error:', e)
      const errorMsg =
        e instanceof Error
          ? e.message
          : 'Registration failed due to server error'
      const lowerMsg = errorMsg.toLowerCase()
      if (
        lowerMsg.includes('whitelist') ||
        lowerMsg.includes('capacity') ||
        lowerMsg.includes('limit') ||
        lowerMsg.includes('permission denied') ||
        lowerMsg.includes('not on whitelist')
      ) {
        setView('whitelist-full')
        return
      }
      setError(errorMsg)
      toast.error(errorMsg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <DeepVoidBackground
      className="min-h-screen flex items-center justify-center py-12 font-sans nofx-auth"
      disableAnimation
    >
      <div className="w-full max-w-lg relative z-10 px-6">
        <div className="flex justify-between items-center mb-8">
          <button
            onClick={() => navigate('/')}
            className="flex items-center gap-2 text-nofx-text-muted hover:text-nofx-text transition-colors group px-3 py-1.5 rounded-lg border border-transparent hover:border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper backdrop-blur-xs"
          >
            <span className="text-xs font-sans tracking-normal">
              Back to home
            </span>
          </button>
        </div>

        <div className="mb-8 text-center">
          <div className="flex justify-center mb-6">
            <BrandLogo />
          </div>
          <h1 className="text-3xl font-bold tracking-tighter text-nofx-text mb-2">
            Create your account
          </h1>
          <p className="text-nofx-text-muted text-sm">
            Set up your local NOFX workspace. Connect your AI wallet and trading
            accounts when you are ready.
          </p>
        </div>

        <div className="bg-nofx-bg-lighter backdrop-blur-md border border-[rgba(25,36,52,0.14)] rounded-lg overflow-hidden shadow-lg relative group">
          <div className="p-6 md:p-8 relative">
            <form onSubmit={handleRegister} className="space-y-5">
              <div>
                <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                  {t('email', language)}
                </label>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  className="w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden transition-all placeholder-nofx-text-muted text-nofx-text font-sans"
                  placeholder="you@example.com"
                  required
                />
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                    {t('password', language)}
                  </label>
                  <div className="relative">
                    <input
                      type={showPassword ? 'text' : 'password'}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      className="w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden transition-all placeholder-nofx-text-muted text-nofx-text font-sans pr-10"
                      placeholder="••••••••"
                      required
                    />
                    <button
                      type="button"
                      onClick={() => setShowPassword(!showPassword)}
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-nofx-text-muted hover:text-nofx-text transition-colors"
                    >
                      {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                    </button>
                  </div>
                </div>

                <div>
                  <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                    {t('confirmPassword', language)}
                  </label>
                  <div className="relative">
                    <input
                      type={showConfirmPassword ? 'text' : 'password'}
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      className="w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden transition-all placeholder-nofx-text-muted text-nofx-text font-sans pr-10"
                      placeholder="••••••••"
                      required
                    />
                    <button
                      type="button"
                      onClick={() =>
                        setShowConfirmPassword(!showConfirmPassword)
                      }
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-nofx-text-muted hover:text-nofx-text transition-colors"
                    >
                      {showConfirmPassword ? (
                        <EyeOff size={16} />
                      ) : (
                        <Eye size={16} />
                      )}
                    </button>
                  </div>
                </div>
              </div>

              <div className="bg-nofx-bg-deeper p-3 rounded-lg border border-[rgba(25,36,52,0.14)]">
                <div className="text-[10px] tracking-normal text-nofx-text-muted mb-2 font-bold flex items-center gap-2">
                  <div className="w-1 h-1 rounded-full bg-nofx-text-muted"></div>
                  Password requirements
                </div>
                <div className="text-xs font-sans text-nofx-text-muted">
                  <PasswordChecklist
                    rules={[
                      'minLength',
                      'capital',
                      'lowercase',
                      'number',
                      'specialChar',
                      'match',
                    ]}
                    minLength={8}
                    value={password}
                    valueAgain={confirmPassword}
                    messages={{
                      minLength: t('passwordRuleMinLength', language),
                      capital: t('passwordRuleUppercase', language),
                      lowercase: t('passwordRuleLowercase', language),
                      number: t('passwordRuleNumber', language),
                      specialChar: t('passwordRuleSpecial', language),
                      match: t('passwordRuleMatch', language),
                    }}
                    className="grid grid-cols-2 gap-x-4 gap-y-1"
                    onChange={(isValid) => setPasswordValid(isValid)}
                    iconSize={10}
                  />
                </div>
              </div>

              {betaMode && (
                <div>
                  <label className="block text-xs tracking-normal text-nofx-gold mb-1.5 ml-1 font-bold">
                    Priority Access Code
                  </label>
                  <input
                    type="text"
                    value={betaCode}
                    onChange={(e) =>
                      setBetaCode(
                        e.target.value.replace(/[^a-z0-9]/gi, '').toLowerCase()
                      )
                    }
                    className="w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden transition-all placeholder-nofx-text-muted text-nofx-text font-sans tracking-widest"
                    placeholder="XXXXXX"
                    maxLength={6}
                    required={betaMode}
                  />
                  <p className="text-[10px] text-nofx-text-muted font-sans mt-1 ml-1">
                    Enter your six-character access code.
                  </p>
                </div>
              )}

              {error && (
                <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg font-sans">
                  {error}
                </div>
              )}

              <button
                type="submit"
                disabled={
                  loading || (betaMode && !betaCode.trim()) || !passwordValid
                }
                className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 px-4 rounded-lg text-sm tracking-normal hover:bg-nofx-gold-highlight transition-all transform active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed font-sans flex items-center justify-center gap-2 group mt-4"
              >
                {loading ? (
                  <span className="animate-pulse">Creating account…</span>
                ) : (
                  <>
                    <span>Create account</span>
                    <span className="group-hover:translate-x-1 transition-transform">
                      -&gt;
                    </span>
                  </>
                )}
              </button>
            </form>
          </div>
        </div>

        <div className="text-center mt-8 space-y-4">
          <p className="text-xs font-sans text-nofx-text-muted">
            Already have an account?{' '}
            <button
              onClick={() => navigate('/login')}
              className="text-nofx-gold hover:underline hover:text-nofx-gold-highlight transition-colors ml-1"
            >
              Sign in
            </button>
          </p>
          <button
            onClick={() => navigate('/')}
            className="text-[10px] text-nofx-text-muted hover:text-nofx-danger transition-colors tracking-normal hover:underline decoration-nofx-danger/30 font-sans"
          >
            Back to home
          </button>
        </div>
      </div>
    </DeepVoidBackground>
  )
}
