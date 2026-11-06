import { useNavigate } from 'react-router-dom'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

export function RegistrationDisabled() {
  const { language } = useLanguage()
  const navigate = useNavigate()

  const handleBackToLogin = () => {
    navigate('/login')
  }

  return (
    <div
      className="min-h-screen flex items-center justify-center"
      style={{ background: '#f6f8fc', color: '#192434' }}
    >
      <div className="text-center max-w-md px-6">
        <img
          src="/icons/nofx-flow.svg"
          alt="NoFx Logo"
          className="w-16 h-16 mx-auto mb-4"
        />
        <h1 className="text-2xl font-semibold mb-3">
          {t('registrationClosed', language)}
        </h1>
        <p className="text-sm text-nofx-text-muted">
          {t('registrationClosedMessage', language)}
        </p>
        <button
          className="mt-6 px-4 py-2 rounded-sm text-sm font-semibold transition-colors hover:opacity-90"
          style={{ background: '#295be8', color: '#f6f8fc' }}
          onClick={handleBackToLogin}
        >
          {t('backToLogin', language)}
        </button>
      </div>
    </div>
  )
}
