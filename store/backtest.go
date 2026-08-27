package store

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ============================================================================
// Backtest schema (P3.2 — designed against the current project schema, not a
// copy of the nofxplus 725-line migration)
// ============================================================================

// BacktestRun is the backtest run metadata plus final metrics.
type BacktestRun struct {
	ID                 string    `gorm:"primaryKey" json:"id"`
	Name               string    `gorm:"not null;default:''" json:"name"`
	UserID             string    `gorm:"column:user_id;not null;default:'';index:idx_backtest_run_user" json:"user_id"`
	Status             string    `gorm:"not null;default:'pending';index:idx_backtest_run_status" json:"status"`
	StrategyConfigJSON string    `gorm:"column:strategy_config_json;type:text" json:"strategy_config_json"` // strategy config snapshot
	ModelConfigJSON    string    `gorm:"column:model_config_json;type:text" json:"model_config_json"`       // model config snapshot
	Symbols            string    `gorm:"type:text" json:"symbols"`                                          // JSON array
	StartTime          time.Time `json:"start_time"`
	EndTime            time.Time `json:"end_time"`
	Timeframe          string    `gorm:"not null;default:''" json:"timeframe"`
	ExchangeType       string    `gorm:"column:exchange_type;not null;default:''" json:"exchange_type"`
	FeeMaker           float64   `gorm:"not null;default:0" json:"fee_maker"`
	FeeTaker           float64   `gorm:"not null;default:0" json:"fee_taker"`
	InitialBalance     float64   `gorm:"not null;default:0" json:"initial_balance"`
	FinalBalance       float64   `gorm:"not null;default:0" json:"final_balance"`
	TotalReturnPct     float64   `gorm:"not null;default:0" json:"total_return_pct"`
	MaxDrawdownPct     float64   `gorm:"not null;default:0" json:"max_drawdown_pct"`
	SharpeRatio        float64   `gorm:"not null;default:0" json:"sharpe_ratio"`
	WinRate            float64   `gorm:"not null;default:0" json:"win_rate"`
	ProfitFactor       float64   `gorm:"not null;default:0" json:"profit_factor"`
	TradeCount         int       `gorm:"not null;default:0" json:"trade_count"`
	Error              string    `gorm:"type:text" json:"error,omitempty"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	EndedAt            time.Time `json:"ended_at,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TableName implements gorm.Tabler.
func (BacktestRun) TableName() string { return "backtest_runs" }

// BacktestEquityPoint is one equity-curve sample of a run.
type BacktestEquityPoint struct {
	ID      uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RunID   string    `gorm:"column:run_id;not null;index:idx_backtest_equity_run_time" json:"run_id"`
	Time    time.Time `gorm:"index:idx_backtest_equity_run_time,sort:asc" json:"time"`
	Balance float64   `gorm:"not null;default:0" json:"balance"`
	Equity  float64   `gorm:"not null;default:0" json:"equity"` // includes unrealized PnL
}

// TableName implements gorm.Tabler.
func (BacktestEquityPoint) TableName() string { return "backtest_equity_points" }

// BacktestTradeEvent is one filled trade of a run.
type BacktestTradeEvent struct {
	ID       uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RunID    string    `gorm:"column:run_id;not null;index:idx_backtest_trade_run_time" json:"run_id"`
	Time     time.Time `gorm:"index:idx_backtest_trade_run_time,sort:asc" json:"time"`
	Symbol   string    `gorm:"not null;default:''" json:"symbol"`
	Side     string    `gorm:"not null;default:''" json:"side"`     // buy/sell
	Position string    `gorm:"not null;default:''" json:"position"` // open/close/liquidation
	Price    float64   `gorm:"not null;default:0" json:"price"`
	Size     float64   `gorm:"not null;default:0" json:"size"`
	Fee      float64   `gorm:"not null;default:0" json:"fee"`
	PnL      float64   `gorm:"not null;default:0" json:"pnl"` // meaningful on close
}

// TableName implements gorm.Tabler.
func (BacktestTradeEvent) TableName() string { return "backtest_trade_events" }

// BacktestDecisionLog snapshots the decision input/output for
// reproducibility (optional, §P3.2).
type BacktestDecisionLog struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RunID        string    `gorm:"column:run_id;not null;index:idx_backtest_decision_run_time" json:"run_id"`
	Time         time.Time `gorm:"index:idx_backtest_decision_run_time,sort:asc" json:"time"`
	ContextJSON  string    `gorm:"column:context_json;type:text" json:"context_json"`
	DecisionJSON string    `gorm:"column:decision_json;type:text" json:"decision_json"`
}

// TableName implements gorm.Tabler.
func (BacktestDecisionLog) TableName() string { return "backtest_decision_logs" }

// ============================================================================
// Store
// ============================================================================

// BacktestStore persists backtest runs and their results.
type BacktestStore struct {
	db *gorm.DB
}

// NewBacktestStore creates a new BacktestStore.
func NewBacktestStore(db *gorm.DB) *BacktestStore {
	return &BacktestStore{db: db}
}

// initTables migrates the backtest tables (AutoMigrate, SQLite + PG).
func (s *BacktestStore) initTables() error {
	return s.db.AutoMigrate(
		&BacktestRun{},
		&BacktestEquityPoint{},
		&BacktestTradeEvent{},
		&BacktestDecisionLog{},
	)
}

// CreateRun inserts a new run.
func (s *BacktestStore) CreateRun(run *BacktestRun) error {
	return s.db.Create(run).Error
}

// UpdateRun saves run metadata (status/metrics/error/timestamps).
func (s *BacktestStore) UpdateRun(run *BacktestRun) error {
	return s.db.Save(run).Error
}

// GetRun loads a run by ID (user-scoped).
func (s *BacktestStore) GetRun(userID, runID string) (*BacktestRun, error) {
	var run BacktestRun
	err := s.db.Where("id = ? AND user_id = ?", runID, userID).First(&run).Error
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// GetRunAnyUser loads a run by ID regardless of owner (internal use).
func (s *BacktestStore) GetRunAnyUser(runID string) (*BacktestRun, error) {
	var run BacktestRun
	if err := s.db.Where("id = ?", runID).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

// ListRuns lists a user's runs, newest first, with pagination and an
// optional status filter. Returns the total count (unfiltered by page).
func (s *BacktestStore) ListRuns(userID string, page, pageSize int, status string) ([]BacktestRun, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	q := s.db.Model(&BacktestRun{}).Where("user_id = ?", userID)
	if status != "" && status != "all" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var runs []BacktestRun
	err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&runs).Error
	return runs, total, err
}

// DeleteRun removes a run and all its result rows.
func (s *BacktestStore) DeleteRun(userID, runID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND user_id = ?", runID, userID).Delete(&BacktestRun{}).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", runID).Delete(&BacktestEquityPoint{}).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", runID).Delete(&BacktestTradeEvent{}).Error; err != nil {
			return err
		}
		return tx.Where("run_id = ?", runID).Delete(&BacktestDecisionLog{}).Error
	})
}

// AppendEquityPoints batch-inserts equity samples.
func (s *BacktestStore) AppendEquityPoints(points []BacktestEquityPoint) error {
	if len(points) == 0 {
		return nil
	}
	return s.db.CreateInBatches(points, 500).Error
}

// GetEquityPoints loads equity samples in [from, to], capped at limit.
func (s *BacktestStore) GetEquityPoints(runID string, from, to *time.Time, limit int) ([]BacktestEquityPoint, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	q := s.db.Where("run_id = ?", runID)
	if from != nil {
		q = q.Where("time >= ?", *from)
	}
	if to != nil {
		q = q.Where("time <= ?", *to)
	}
	var pts []BacktestEquityPoint
	err := q.Order("time ASC").Limit(limit).Find(&pts).Error
	return pts, err
}

// CountEquityPoints counts equity samples for a run (cap enforcement).
func (s *BacktestStore) CountEquityPoints(runID string) (int64, error) {
	var n int64
	err := s.db.Model(&BacktestEquityPoint{}).Where("run_id = ?", runID).Count(&n).Error
	return n, err
}

// AppendTrades batch-inserts trade events.
func (s *BacktestStore) AppendTrades(trades []BacktestTradeEvent) error {
	if len(trades) == 0 {
		return nil
	}
	return s.db.CreateInBatches(trades, 500).Error
}

// GetTrades loads trade events in [from, to], capped at limit.
func (s *BacktestStore) GetTrades(runID string, from, to *time.Time, limit int) ([]BacktestTradeEvent, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	q := s.db.Where("run_id = ?", runID)
	if from != nil {
		q = q.Where("time >= ?", *from)
	}
	if to != nil {
		q = q.Where("time <= ?", *to)
	}
	var trades []BacktestTradeEvent
	err := q.Order("time ASC").Limit(limit).Find(&trades).Error
	return trades, err
}

// CountTrades counts trade events for a run.
func (s *BacktestStore) CountTrades(runID string) (int64, error) {
	var n int64
	err := s.db.Model(&BacktestTradeEvent{}).Where("run_id = ?", runID).Count(&n).Error
	return n, err
}

// AppendDecisionLogs batch-inserts decision snapshots.
func (s *BacktestStore) AppendDecisionLogs(logs []BacktestDecisionLog) error {
	if len(logs) == 0 {
		return nil
	}
	return s.db.CreateInBatches(logs, 500).Error
}

// GetDecisionLogs loads decision snapshots newest-first, capped at limit.
func (s *BacktestStore) GetDecisionLogs(runID string, limit int) ([]BacktestDecisionLog, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	var logs []BacktestDecisionLog
	err := s.db.Where("run_id = ?", runID).Order("time DESC").Limit(limit).Find(&logs).Error
	return logs, err
}

// ListRetainedRuns returns completed/failed/cancelled runs for the
// retention task (older than retentionDays).
func (s *BacktestStore) ListRetainedRuns(retentionDays int) ([]BacktestRun, error) {
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var runs []BacktestRun
	err := s.db.Where("status IN ? AND ended_at < ?", []string{"completed", "failed", "cancelled"}, cutoff).
		Find(&runs).Error
	return runs, err
}

// DeleteRunData removes equity/trades/decisions of a run without the
// metadata row (used by the retention task).
func (s *BacktestStore) DeleteRunData(runID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("run_id = ?", runID).Delete(&BacktestEquityPoint{}).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", runID).Delete(&BacktestTradeEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", runID).Delete(&BacktestDecisionLog{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", runID).Delete(&BacktestRun{}).Error
	})
}

// MarshalSymbols serializes a symbol list for the Symbols column.
func MarshalSymbols(symbols []string) string {
	b, _ := json.Marshal(symbols)
	return string(b)
}

// UnmarshalSymbols parses the Symbols column.
func UnmarshalSymbols(s string) ([]string, error) {
	var out []string
	if s == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("invalid symbols json: %w", err)
	}
	return out, nil
}
