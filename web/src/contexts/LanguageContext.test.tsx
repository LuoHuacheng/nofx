import { render, screen, fireEvent } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { LanguageProvider, useLanguage } from './LanguageContext'
import { Header } from '../components/common/Header'

function LanguageProbe() {
  const { language } = useLanguage()
  return <div>{language}</div>
}

describe('language switching (zh/en)', () => {
  beforeEach(() => localStorage.clear())

  it('defaults to English without saved state', () => {
    render(
      <LanguageProvider>
        <LanguageProbe />
      </LanguageProvider>
    )
    expect(screen.getByText('en')).toBeTruthy()
    expect(localStorage.getItem('language')).toBe('en')
  })

  it('restores a saved Chinese preference', () => {
    localStorage.setItem('language', 'zh')
    render(
      <LanguageProvider>
        <LanguageProbe />
      </LanguageProvider>
    )
    expect(screen.getByText('zh')).toBeTruthy()
  })

  it('persists the chosen language to storage', () => {
    localStorage.setItem('language', 'en')
    const SwitchProbe = () => {
      const { setLanguage } = useLanguage()
      return <button onClick={() => setLanguage('zh')}>switch-zh</button>
    }
    render(
      <LanguageProvider>
        <SwitchProbe />
      </LanguageProvider>
    )
    fireEvent.click(screen.getByText('switch-zh'))
    expect(localStorage.getItem('language')).toBe('zh')
  })

  it('renders the language switcher with Chinese and English but no Indonesian', () => {
    const { container } = render(
      <LanguageProvider>
        <Header />
      </LanguageProvider>
    )
    const labels = Array.from(container.querySelectorAll('button')).map(
      (b) => b.textContent
    )
    expect(labels).toContain('中文')
    expect(labels).toContain('EN')
    expect(labels).not.toContain('ID')
  })
})
