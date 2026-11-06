import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { RegistrationDisabled } from './RegistrationDisabled'
import { LanguageProvider } from '../../contexts/LanguageContext'

const mockNavigate = vi.fn()

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

// Mock useLanguage hook
vi.mock('../../contexts/LanguageContext', async () => {
  const actual = await vi.importActual('../../contexts/LanguageContext')
  return {
    ...actual,
    useLanguage: () => ({ language: 'en' }),
  }
})

/**
 * RegistrationDisabled Component Tests
 *
 * Tests the component that displays when registration is disabled
 * This is part of the registration toggle feature
 */
describe('RegistrationDisabled Component', () => {
  const renderComponent = () => {
    return render(
      <MemoryRouter>
        <LanguageProvider>
          <RegistrationDisabled />
        </LanguageProvider>
      </MemoryRouter>
    )
  }

  describe('Rendering', () => {
    it('should render the component without errors', () => {
      const { container } = renderComponent()
      expect(container).toBeTruthy()
    })

    it('should display the NoFx logo', () => {
      renderComponent()
      const logo = screen.getByAltText('NoFx Logo')
      expect(logo).toBeTruthy()
      expect(logo.getAttribute('src')).toBe('/icons/nofx-flow.svg')
    })

    it('should display registration closed heading', () => {
      renderComponent()
      const heading = screen.getByText('Registration Closed')
      expect(heading).toBeTruthy()
    })

    it('should display registration closed message', () => {
      renderComponent()
      const message = screen.getByText(
        /User registration is currently disabled/i
      )
      expect(message).toBeTruthy()
    })

    it('should display back to login button', () => {
      renderComponent()
      const button = screen.getByRole('button', { name: /back to login/i })
      expect(button).toBeTruthy()
    })
  })

  describe('Navigation', () => {
    it('should navigate to login page when button is clicked', () => {
      renderComponent()
      const button = screen.getByRole('button', { name: /back to login/i })

      fireEvent.click(button)

      expect(mockNavigate).toHaveBeenCalledWith('/login')
    })
  })

  describe('Styling', () => {
    it('should have correct background color', () => {
      const { container } = renderComponent()
      const mainDiv = container.firstChild as HTMLElement
      // Browser converts hex to rgb (Flow canvas theme)
      expect(mainDiv.style.background).toMatch(
        /rgb\(246,\s*248,\s*252\)|#F6F8FC/i
      )
    })

    it('should have correct text color', () => {
      const { container } = renderComponent()
      const mainDiv = container.firstChild as HTMLElement
      // Browser converts hex to rgb (ink text)
      expect(mainDiv.style.color).toMatch(/rgb\(25,\s*36,\s*52\)|#192434/i)
    })

    it('should have centered layout', () => {
      const { container } = renderComponent()
      const mainDiv = container.firstChild as HTMLElement
      expect(mainDiv.className).toContain('flex')
      expect(mainDiv.className).toContain('items-center')
      expect(mainDiv.className).toContain('justify-center')
    })
  })
})
