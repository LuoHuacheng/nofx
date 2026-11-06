package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/instrument"
	"nofx/planning/live"
	"nofx/store"
	"nofx/trader/binance"
	"nofx/trader/bitget"
	"nofx/trader/execution"
	"nofx/trader/okx"
)

// Persist before submission. A timeout, reload or duplicate click must never
// authorize another close without reconciling the original exchange order.
type accountCloseIntent struct {
	ID         string `gorm:"primaryKey"`
	UserID     string
	ExchangeID string
	AccountKey string `gorm:"index"`
	Symbol     string
	Side       string
	Quantity   string
	Entry      string
	State      string
	OrderJSON  string
	UpdatedAt  time.Time
}

func (accountCloseIntent) TableName() string { return "planning_account_closes" }

type accountCloseRequest struct {
	ExchangeID string `json:"exchange_id"`
	RequestID  string `json:"request_id"`
	Symbol     string `json:"symbol"`
	Side       string `json:"side"`
	Quantity   string `json:"quantity"`
	Entry      string `json:"entry_price"`
}

type accountCloser interface {
	GetPositions() ([]map[string]interface{}, error)
	RouteClientID(string) (string, error)
	SubmitOrder(context.Context, execution.Order) (execution.Order, error)
	LookupOrder(context.Context, execution.Order) (execution.Order, error)
}

type closeNotSubmittedError struct{ error }

func writeAccountCloseError(c *gin.Context, err error) {
	state := "unconfirmed"
	var notSent closeNotSubmittedError
	if errors.As(err, &notSent) {
		state = "not_submitted"
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "close_state": state})
}

func (s *Server) handlePlanningAccountClose(c *gin.Context) {
	var in accountCloseRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if c.ShouldBindJSON(&in) != nil {
		writeAccountCloseError(c, closeNotSubmittedError{errors.New("Invalid position close request; no close was sent")})
		return
	}
	p, err := s.planningService()
	if err != nil {
		writeAccountCloseError(c, closeNotSubmittedError{err})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	user := c.GetString("user_id")
	order, err := s.closeFuturesAccount(ctx, user, in, func(ex *store.Exchange) (accountCloser, error) {
		if ex.ExchangeType == "okx" {
			return okx.NewAccountTrader(string(ex.APIKey), string(ex.SecretKey), string(ex.Passphrase)), nil
		}
		if ex.ExchangeType == "bitget" {
			return bitget.NewAccountTrader(string(ex.APIKey), string(ex.SecretKey), string(ex.Passphrase)), nil
		}
		return binance.NewAccountTrader(ctx, string(ex.APIKey), string(ex.SecretKey), ex.UserID)
	})
	if err != nil {
		writeAccountCloseError(c, err)
		return
	}
	message := "Close order accepted. Verify the refreshed position and remaining exchange orders."
	if order.Status == "FILLED" {
		runs, readErr := p.live.List(user)
		if readErr != nil {
			message += " Execution records could not be refreshed."
		}
		for _, run := range runs {
			if run.State == "completed" || run.State == "cancelled" || run.State == "expired" || run.ExchangeID != in.ExchangeID || run.Plan.Snapshot.Symbol != in.Symbol || run.Plan.Idea.Side != in.Side {
				continue
			}
			if _, err := p.live.FinishManualClose(ctx, user, run.ID); err != nil {
				message += " " + err.Error()
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": message, "order": order})
}

func (s *Server) closeFuturesAccount(ctx context.Context, user string, in accountCloseRequest, factory func(*store.Exchange) (accountCloser, error)) (out execution.Order, err error) {
	// Only a definite pre-submission failure may release the UI's close latch.
	// Existing durable intents and ambiguous submissions must stay protected.
	mayHaveSubmitted := false
	defer func() {
		if err != nil && !mayHaveSubmitted {
			err = closeNotSubmittedError{err}
		}
	}()
	id, err := uuid.Parse(in.RequestID)
	if err != nil || id == uuid.Nil || !instrument.ValidSymbol(in.Symbol) || (in.Side != "long" && in.Side != "short") || !execution.Positive(execution.Decimal(in.Quantity)) || !execution.ValidOrderPrice(execution.Order{Role: "entry", Type: "market", ReduceOnly: true, Price: execution.Decimal(in.Entry)}) {
		return out, errors.New("Review a valid position and quantity before closing")
	}
	in.RequestID = id.String()
	ex, err := s.accountReadConfig(user, "", in.ExchangeID)
	if err != nil {
		return out, err
	}
	if in.ExchangeID == "" || (ex.ExchangeType != "binance" && ex.ExchangeType != "okx" && ex.ExchangeType != "bitget") || ex.Testnet {
		return out, errors.New("Select a Binance, OKX or Bitget mainnet USDT futures account")
	}
	key := accountKey(ex)
	unlock := planning.LockAccount(key)
	defer unlock()
	// Include duplicate configurations of the same credential, not only its UI ID.
	accounts, err := s.store.Exchange().List(user)
	if err != nil {
		return out, errors.New("Could not verify account state")
	}
	for _, account := range accounts {
		if accountKey(account) != key {
			continue
		}
		traders, err := s.store.Trader().ListByExchangeID(user, account.ID)
		if err != nil {
			return out, errors.New("Could not verify Autopilot state")
		}
		for _, trader := range traders {
			running := trader.IsRunning
			if s.traderManager != nil {
				if runtime, e := s.traderManager.GetTrader(trader.ID); e == nil {
					active, _ := runtime.GetStatus()["is_running"].(bool)
					running = running || active
				}
			}
			if running {
				return out, errors.New("Stop Autopilot on this account before closing manually")
			}
		}
	}
	if err := s.checkCloseEntries(key, in.Symbol); err != nil {
		return out, err
	}
	client, err := factory(ex)
	if err != nil {
		return out, err
	}
	db := s.store.GormDB()
	var previous accountCloseIntent
	query := db.Where("id = ?", in.RequestID).First(&previous)
	if query.Error == nil {
		mayHaveSubmitted = true
		if previous.UserID != user || previous.ExchangeID != in.ExchangeID || previous.Symbol != in.Symbol || previous.Side != in.Side || previous.Quantity != in.Quantity || previous.Entry != in.Entry {
			return out, errors.New("Close request identity mismatch")
		}
		return reconcileAccountClose(ctx, db, client, &previous)
	}
	if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
		return out, errors.New("Could not verify the earlier close request")
	}
	var pending []accountCloseIntent
	if err := db.Where("account_key = ? AND symbol = ? AND side = ? AND state = ?", key, in.Symbol, in.Side, "unconfirmed").Find(&pending).Error; err != nil {
		return out, err
	}
	for i := range pending {
		mayHaveSubmitted = true
		if _, err := reconcileAccountClose(ctx, db, client, &pending[i]); err != nil {
			return out, err
		}
	}
	positions, err := client.GetPositions()
	if err != nil {
		return out, errors.New("Could not verify the current exchange position; no close was sent")
	}
	quantity, err := reviewedCloseQuantity(positions, in)
	if err != nil {
		return out, err
	}
	side := "short"
	if in.Side == "short" {
		side = "long"
	}
	out = execution.Order{ClientID: strings.ReplaceAll(id.String(), "-", ""), Symbol: in.Symbol, Role: "entry", Type: "market", Side: side, Price: execution.Decimal(in.Entry), Quantity: execution.Decimal(quantity), ReduceOnly: true, Status: "submitting", Filled: "0"}
	out.VenueClientID, err = client.RouteClientID(out.ClientID)
	if err != nil {
		return out, err
	}
	raw, _ := json.Marshal(out)
	intent := accountCloseIntent{ID: in.RequestID, UserID: user, ExchangeID: in.ExchangeID, AccountKey: key, Symbol: in.Symbol, Side: in.Side, Quantity: in.Quantity, Entry: in.Entry, State: "unconfirmed", OrderJSON: string(raw), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&intent).Error; err != nil {
		return out, errors.New("Could not record close intent; no order was sent")
	}
	mayHaveSubmitted = true
	out, err = client.SubmitOrder(ctx, out)
	if err != nil {
		return out, errors.New("The exchange close is unconfirmed. Check the position and exchange order history before retrying; no automatic repeat was sent")
	}
	if err := saveAccountClose(db, &intent, out); err != nil {
		return out, err
	}
	return out, nil
}

func reviewedCloseQuantity(positions []map[string]interface{}, in accountCloseRequest) (string, error) {
	maximum, _ := strconv.ParseFloat(in.Quantity, 64)
	entry, _ := strconv.ParseFloat(in.Entry, 64)
	for _, p := range positions {
		if p["symbol"] != in.Symbol || p["side"] != in.Side {
			continue
		}
		qty, ok := p["positionAmt"].(float64)
		price, priceOK := p["entryPrice"].(float64)
		qty = math.Abs(qty)
		if !ok || !priceOK || math.IsNaN(qty) || math.IsInf(qty, 0) || qty <= 0 || qty > maximum || math.IsNaN(price) || math.Abs(price-entry) > math.Max(1, entry)*1e-12 {
			return "", errors.New("Position changed. Refresh and review its current size before closing")
		}
		return strconv.FormatFloat(qty, 'f', -1, 64), nil
	}
	return "", errors.New("Position is no longer open; no close was sent")
}

func (s *Server) checkCloseEntries(key, symbol string) error {
	var runs []live.Run
	if err := s.store.GormDB().Where("account_key = ? AND state NOT IN ?", key, []string{"completed", "cancelled", "expired"}).Find(&runs).Error; err != nil {
		return errors.New("Could not verify remaining Frontier entries")
	}
	for _, run := range runs {
		var plan core.Plan
		var orders []execution.Order
		if json.Unmarshal([]byte(run.PlanJSON), &plan) != nil || json.Unmarshal([]byte(run.OrdersJSON), &orders) != nil {
			return errors.New("Execution record needs reconciliation before closing")
		}
		if plan.Snapshot.Symbol != symbol {
			continue
		}
		if run.State == "approved" || run.State == "starting" || run.State == "running" {
			return errors.New("Stop remaining Frontier entries before closing this position")
		}
		for _, order := range orders {
			if order.Role == "entry" && !closeOrderTerminal(order.Status) {
				return errors.New("An entry is still open or unconfirmed. Stop and reconcile it before closing")
			}
		}
	}
	return nil
}

func closeOrderTerminal(status string) bool {
	return status == "FILLED" || status == "CANCELED" || status == "REJECTED" || status == "EXPIRED"
}

func saveAccountClose(db *gorm.DB, intent *accountCloseIntent, order execution.Order) error {
	raw, _ := json.Marshal(order)
	intent.OrderJSON, intent.UpdatedAt = string(raw), time.Now().UTC()
	if closeOrderTerminal(order.Status) {
		intent.State = "confirmed"
	}
	if err := db.Save(intent).Error; err != nil {
		return errors.New("Close receipt could not be saved; check the exchange before another close")
	}
	return nil
}

func reconcileAccountClose(ctx context.Context, db *gorm.DB, client accountCloser, intent *accountCloseIntent) (execution.Order, error) {
	var order execution.Order
	if json.Unmarshal([]byte(intent.OrderJSON), &order) != nil {
		return order, errors.New("Close record needs reconciliation")
	}
	if intent.State == "confirmed" {
		return order, nil
	}
	result, err := client.LookupOrder(ctx, order)
	if err != nil || !closeOrderTerminal(result.Status) {
		return order, errors.New("An earlier close is still unconfirmed. Check exchange order history; it will not be sent again")
	}
	return result, saveAccountClose(db, intent, result)
}
