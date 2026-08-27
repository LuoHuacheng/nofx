// Backtest API client (P3.4).
import { API_BASE, httpClient } from './helpers'

export interface BacktestRun {
  id: string
  name: string
  user_id: string
  status: string
  strategy_config_json?: string
  model_config_json?: string
  symbols: string
  start_time?: string
  end_time?: string
  timeframe?: string
  exchange_type?: string
  fee_maker?: number
  fee_taker?: number
  initial_balance?: number
  final_balance?: number
  total_return_pct?: number
  max_drawdown_pct?: number
  sharpe_ratio?: number
  win_rate?: number
  profit_factor?: number
  trade_count?: number
  error?: string
  created_at?: string
  updated_at?: string
}

export interface BacktestEquityPoint {
  id: number
  run_id: string
  time: string
  balance: number
  equity: number
}

export interface BacktestTrade {
  id: number
  run_id: string
  time: string
  symbol: string
  side: string
  position: string
  price: number
  size: number
  fee: number
  pnl: number
}

export interface BacktestTraceEntry {
  time: string
  kind: string
  symbol: string
  message: string
  data?: string
}

export interface BacktestMetrics {
  run_id: string
  name: string
  status: string
  symbols: string[]
  timeframe: string
  initial_balance: number
  final_balance: number
  total_return_pct: number
  max_drawdown_pct: number
  sharpe_ratio: number
  win_rate: number
  profit_factor: number
  trade_count: number
  error?: string
}

export interface BacktestStartRequest {
  name?: string
  symbols: string[]
  timeframe: string
  start_time: string
  end_time: string
  initial_balance: number
  strategy_id: string
  model_id: string
  exchange_id?: string
  fee_maker?: number
  fee_taker?: number
}

export interface BacktestKline {
  openTime: number
  open: number
  high: number
  low: number
  close: number
  volume: number
  closeTime: number
}

export const backtestApi = {
  async start(
    req: BacktestStartRequest
  ): Promise<{ run_id: string; status: string }> {
    const result = await httpClient.post<{ run_id: string; status: string }>(
      `${API_BASE}/backtest/start`,
      req
    )
    if (!result.success) {
      throw new Error(
        result.message || result.errorKey || 'Failed to start backtest'
      )
    }
    return result.data!
  },

  async lifecycle(
    action: 'pause' | 'resume' | 'stop' | 'delete',
    runId: string
  ): Promise<void> {
    const result = await httpClient.post<{ status: string }>(
      `${API_BASE}/backtest/${action}`,
      { run_id: runId }
    )
    if (!result.success) {
      throw new Error(
        result.message || result.errorKey || `Failed to ${action} backtest`
      )
    }
  },

  async status(
    runId: string
  ): Promise<{ run_id: string; status: string; error?: string }> {
    const result = await httpClient.get<{
      run_id: string
      status: string
      error?: string
    }>(`${API_BASE}/backtest/status?run_id=${encodeURIComponent(runId)}`)
    if (!result.success)
      throw new Error(result.message || 'Failed to get status')
    return result.data!
  },

  async list(
    page = 1,
    pageSize = 20,
    status = ''
  ): Promise<{ runs: BacktestRun[]; total: number }> {
    const params = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    })
    if (status) params.set('status', status)
    const result = await httpClient.get<{ runs: BacktestRun[]; total: number }>(
      `${API_BASE}/backtest/runs?${params}`
    )
    if (!result.success)
      throw new Error(result.message || 'Failed to list runs')
    return result.data!
  },

  async metrics(runId: string): Promise<BacktestMetrics> {
    const result = await httpClient.get<BacktestMetrics>(
      `${API_BASE}/backtest/metrics?run_id=${encodeURIComponent(runId)}`
    )
    if (!result.success)
      throw new Error(result.message || 'Failed to get metrics')
    return result.data!
  },

  async equity(runId: string, limit = 5000): Promise<BacktestEquityPoint[]> {
    const result = await httpClient.get<BacktestEquityPoint[]>(
      `${API_BASE}/backtest/equity?run_id=${encodeURIComponent(runId)}&limit=${limit}`
    )
    return Array.isArray(result.data) ? result.data : []
  },

  async trades(runId: string, limit = 1000): Promise<BacktestTrade[]> {
    const result = await httpClient.get<BacktestTrade[]>(
      `${API_BASE}/backtest/trades?run_id=${encodeURIComponent(runId)}&limit=${limit}`
    )
    return Array.isArray(result.data) ? result.data : []
  },

  async trace(runId: string, limit = 100): Promise<BacktestTraceEntry[]> {
    const result = await httpClient.get<BacktestTraceEntry[]>(
      `${API_BASE}/backtest/trace?run_id=${encodeURIComponent(runId)}&limit=${limit}`
    )
    return Array.isArray(result.data) ? result.data : []
  },

  async klines(
    symbol: string,
    timeframe: string,
    from: string,
    to: string
  ): Promise<BacktestKline[]> {
    const result = await httpClient.get<BacktestKline[]>(
      `${API_BASE}/backtest/klines?symbol=${encodeURIComponent(symbol)}&timeframe=${encodeURIComponent(timeframe)}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
    )
    return Array.isArray(result.data) ? result.data : []
  },
}
