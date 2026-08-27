export type Page =
  | 'competition'
  | 'traders'
  | 'trader'
  | 'strategy'
  | 'strategy-market'
  | 'data'
  | 'backtest'
  | 'faq'
  | 'login'
  | 'register'

export const ROUTES = {
  home: '/',
  login: '/login',
  register: '/register',
  setup: '/setup',
  welcome: '/welcome',
  faq: '/faq',
  resetPassword: '/reset-password',
  settings: '/settings',
  data: '/data',
  backtest: '/backtest',
  competition: '/competition',
  traders: '/traders',
  dashboard: '/dashboard',
  strategy: '/strategy',
  strategyMarket: '/strategy-market',
} as const

export const PAGE_PATHS: Record<Page, string> = {
  competition: ROUTES.competition,
  traders: ROUTES.traders,
  trader: ROUTES.dashboard,
  strategy: ROUTES.strategy,
  'strategy-market': ROUTES.strategyMarket,
  data: ROUTES.data,
  backtest: ROUTES.backtest,
  faq: ROUTES.faq,
  login: ROUTES.login,
  register: ROUTES.register,
}

export const LEGACY_HASH_ROUTES: Record<string, string> = {
  competition: ROUTES.competition,
  traders: ROUTES.traders,
  trader: ROUTES.dashboard,
  details: ROUTES.dashboard,
  strategy: ROUTES.strategy,
  'strategy-market': ROUTES.strategyMarket,
  data: ROUTES.data,
  backtest: ROUTES.backtest,
}

export function getCurrentPageForPath(pathname: string): Page | undefined {
  switch (pathname) {
    case ROUTES.welcome:
    case ROUTES.traders:
      return 'traders'
    case ROUTES.dashboard:
      return 'trader'
    case ROUTES.strategy:
      return 'strategy'
    case ROUTES.strategyMarket:
      return 'strategy-market'
    case ROUTES.data:
      return 'data'
    case ROUTES.backtest:
      return 'backtest'
    case ROUTES.faq:
      return 'faq'
    case ROUTES.login:
      return 'login'
    case ROUTES.register:
      return 'register'
    case ROUTES.competition:
      return 'competition'
    default:
      return undefined
  }
}

export function buildDashboardPath(traderSlug?: string): string {
  if (!traderSlug) {
    return ROUTES.dashboard
  }

  return `${ROUTES.dashboard}?trader=${encodeURIComponent(traderSlug)}`
}
