import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { BrandLogo } from './BrandLogo'
import { Container } from './Container'
import { LanguageSwitcher } from './LanguageSwitcher'

interface HeaderProps {
  simple?: boolean // For login/register pages
}

export function Header({ simple = false }: HeaderProps) {
  const { language } = useLanguage()

  return (
    <header className="glass sticky top-0 z-50 backdrop-blur-xl">
      <Container className="py-4">
        <div className="flex items-center justify-between">
          {/* Left - Logo and Title */}
          <div className="flex items-center gap-3">
            <BrandLogo />
            <div>
              {!simple && (
                <p className="text-xs mono" style={{ color: '#707e92' }}>
                  {t('subtitle', language)}
                </p>
              )}
            </div>
          </div>
          {!simple && <LanguageSwitcher />}
        </div>
      </Container>
    </header>
  )
}
