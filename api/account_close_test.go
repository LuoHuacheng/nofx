package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/store"
	"nofx/trader/execution"
)

type fakeAccountCloser struct {
	positions []map[string]interface{}
	calls     int
	lookups   int
	fail      bool
	last      execution.Order
}

func (f *fakeAccountCloser) GetPositions() ([]map[string]interface{}, error) { return f.positions, nil }
func (f *fakeAccountCloser) RouteClientID(id string) (string, error)         { return "broker-" + id, nil }
func (f *fakeAccountCloser) SubmitOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	f.calls++
	f.last = o
	if f.fail {
		return o, errors.New("timeout")
	}
	o.Status = "FILLED"
	o.Filled = o.Quantity
	o.OrderID = "123"
	return o, nil
}
func (f *fakeAccountCloser) LookupOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	f.lookups++
	return o, errors.New("unconfirmed")
}
func closeFixture(t *testing.T) (*Server, *fakeAccountCloser, accountCloseRequest) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "close.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	st, e := store.NewFromGorm(db)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&store.Exchange{}, &store.Trader{}, &accountCloseIntent{}, &live.Run{}); e != nil {
		t.Fatal(e)
	}
	for _, ex := range []store.Exchange{
		{ID: "first", UserID: "alice", ExchangeType: "binance", APIKey: "first-key", Enabled: true},
		{ID: "second", UserID: "alice", ExchangeType: "binance", APIKey: "second-key", Enabled: true},
		{ID: "foreign", UserID: "bob", ExchangeType: "binance", APIKey: "foreign-key", Enabled: true},
	} {
		if e = db.Create(&ex).Error; e != nil {
			t.Fatal(e)
		}
	}
	f := &fakeAccountCloser{positions: []map[string]interface{}{{"symbol": "BTCUSDT", "side": "short", "positionAmt": -0.019, "entryPrice": 81798.3}}}
	in := accountCloseRequest{ExchangeID: "second", RequestID: uuid.NewString(), Symbol: "BTCUSDT", Side: "short", Quantity: "0.019", Entry: "81798.3"}
	return &Server{store: st}, f, in
}
func TestAccountCloseUsesSelectedBinanceWithoutAutopilot(t *testing.T) {
	s, f, in := closeFixture(t)
	factory := func(ex *store.Exchange) (accountCloser, error) {
		if ex.ID != "second" {
			t.Fatalf("wrong account: %s", ex.ID)
		}
		return f, nil
	}
	order, err := s.closeFuturesAccount(context.Background(), "alice", in, factory)
	if err != nil || order.Status != "FILLED" {
		t.Fatal(order, err)
	}
	if f.calls != 1 || !f.last.ReduceOnly || f.last.Type != "market" || f.last.Side != "long" || f.last.Quantity != "0.019" || f.last.VenueClientID == "" {
		t.Fatal(f.last)
	}
	var count int64
	s.store.GormDB().Model(&store.Trader{}).Count(&count)
	if count != 0 {
		t.Fatal("close created an Autopilot")
	}
	// Retransmission returns the persisted result, without sending a second order.
	if _, err = s.closeFuturesAccount(context.Background(), "alice", in, factory); err != nil || f.calls != 1 {
		t.Fatal(err, f.calls)
	}
}
func TestAccountCloseBlocksForeignChangedAndRunningAccounts(t *testing.T) {
	for _, name := range []string{"foreign", "missing", "disabled", "testnet", "increased", "new-entry", "running", "no-position", "wrong-side"} {
		t.Run(name, func(t *testing.T) {
			s, f, in := closeFixture(t)
			switch name {
			case "foreign":
				in.ExchangeID = "foreign"
			case "missing":
				in.ExchangeID = ""
			case "disabled":
				s.store.GormDB().Model(&store.Exchange{}).Where("id = ?", "second").Update("enabled", false)
			case "testnet":
				s.store.GormDB().Model(&store.Exchange{}).Where("id = ?", "second").Update("testnet", true)
			case "increased":
				f.positions[0]["positionAmt"] = -0.02
			case "new-entry":
				f.positions[0]["entryPrice"] = 81800.0
			case "running":
				s.store.GormDB().Create(&store.Trader{ID: "auto", UserID: "alice", ExchangeID: "second", IsRunning: true})
			case "no-position":
				f.positions = nil
			case "wrong-side":
				in.Side = "long"
			}
			_, err := s.closeFuturesAccount(context.Background(), "alice", in, func(*store.Exchange) (accountCloser, error) { return f, nil })
			if err == nil || f.calls != 0 {
				t.Fatal("unsafe close", err, f.calls)
			}
		})
	}
}
func TestAccountCloseTimeoutCannotRepeatWithNewRequestID(t *testing.T) {
	s, f, in := closeFixture(t)
	f.fail = true
	factory := func(*store.Exchange) (accountCloser, error) { return f, nil }
	if _, err := s.closeFuturesAccount(context.Background(), "alice", in, factory); err == nil {
		t.Fatal("timeout hidden")
	}
	in.RequestID = uuid.NewString()
	if _, err := s.closeFuturesAccount(context.Background(), "alice", in, factory); err == nil || f.calls != 1 || f.lookups != 1 {
		t.Fatal("ambiguous order repeated", err, f.calls, f.lookups)
	}
}
func TestAccountCloseWaitsForScheduledAndUnknownEntries(t *testing.T) {
	for _, status := range []string{"scheduled", "submitting", "NEW", "UNKNOWN", "PARTIALLY_FILLED"} {
		t.Run(status, func(t *testing.T) {
			s, f, in := closeFixture(t)
			ex, _ := s.store.Exchange().GetByID("alice", "second")
			plan, _ := json.Marshal(core.Plan{Snapshot: core.Snapshot{Symbol: "BTCUSDT"}})
			orders, _ := json.Marshal([]execution.Order{{Role: "entry", Status: status}})
			s.store.GormDB().Create(&live.Run{ID: "run", UserID: "alice", ExchangeID: "second", AccountKey: accountKey(ex), State: "stopping", PlanJSON: string(plan), OrdersJSON: string(orders)})
			_, err := s.closeFuturesAccount(context.Background(), "alice", in, func(*store.Exchange) (accountCloser, error) { return f, nil })
			if err == nil || f.calls != 0 {
				t.Fatal("entry can reopen position", status, err)
			}
		})
	}
}

func TestAccountCloseSupportsOKXAndBitgetWithoutAutopilot(t *testing.T) {
	for _, venue := range []string{"okx", "bitget"} {
		t.Run(venue, func(t *testing.T) {
			s, f, in := closeFixture(t)
			if e := s.store.GormDB().Model(&store.Exchange{}).Where("id = ?", "second").Update("exchange_type", venue).Error; e != nil {
				t.Fatal(e)
			}
			if venue == "okx" {
				in.Symbol = "BTC-USDT-SWAP"
				f.positions[0]["symbol"] = in.Symbol
			}
			factory := func(ex *store.Exchange) (accountCloser, error) {
				if ex.ID != "second" || ex.ExchangeType != venue {
					t.Fatal("wrong account")
				}
				return f, nil
			}
			order, e := s.closeFuturesAccount(context.Background(), "alice", in, factory)
			if e != nil || order.Status != "FILLED" || !f.last.ReduceOnly || f.last.Symbol != in.Symbol {
				t.Fatalf("close %+v %v", order, e)
			}
			if _, e = s.closeFuturesAccount(context.Background(), "alice", in, factory); e != nil || f.calls != 1 {
				t.Fatal("duplicate close", e)
			}
			ex, e := s.planningWorkspace("alice", "second", venue)
			if e != nil || ex.ExchangeType != venue {
				t.Fatal("workspace rejected", e)
			}
		})
	}
}

func TestAccountCloseAcceptsWeightedAverageWithoutRounding(t *testing.T) {
	for _, venue := range []string{"binance", "okx", "bitget"} {
		t.Run(venue, func(t *testing.T) {
			s, f, in := closeFixture(t)
			s.store.GormDB().Model(&store.Exchange{}).Where("id = ?", "second").Update("exchange_type", venue)
			in.Symbol, in.Quantity, in.Entry = "QUSDT", "12257", "0.1287878419678551"
			if venue == "okx" {
				in.Symbol = "Q-USDT-SWAP"
			}
			f.positions = []map[string]interface{}{{"symbol": in.Symbol, "side": "short", "positionAmt": float64(-12257), "entryPrice": 0.1287878419678551}}
			o, err := s.closeFuturesAccount(context.Background(), "alice", in, func(*store.Exchange) (accountCloser, error) { return f, nil })
			if err != nil || f.calls != 1 || o.Price != execution.Decimal(in.Entry) || o.Quantity != "12257" || !o.ReduceOnly {
				t.Fatal(o, err, f.calls)
			}
			if !execution.ValidOrderPrice(o) {
				t.Fatal("adapter would reject weighted entry")
			}
		})
	}
}

func TestAccountCloseErrorsDistinguishNotSentFromUnconfirmed(t *testing.T) {
	for _, scenario := range []string{"invalid", "changed", "timeout", "previous-timeout"} {
		t.Run(scenario, func(t *testing.T) {
			s, f, in := closeFixture(t)
			factory := func(*store.Exchange) (accountCloser, error) { return f, nil }
			switch scenario {
			case "invalid":
				in.Quantity = "0"
			case "changed":
				f.positions[0]["entryPrice"] = 82000.0
			case "timeout":
				f.fail = true
			case "previous-timeout":
				f.fail = true
				s.closeFuturesAccount(context.Background(), "alice", in, factory)
				in.RequestID = uuid.NewString()
			}
			_, err := s.closeFuturesAccount(context.Background(), "alice", in, factory)
			if err == nil {
				t.Fatal("expected close error")
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			writeAccountCloseError(c, err)
			var result map[string]string
			if json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatal(w.Body.String())
			}
			expected := "unconfirmed"
			if scenario == "invalid" || scenario == "changed" {
				expected = "not_submitted"
				if f.calls != 0 {
					t.Fatal("unexpected submission")
				}
			}
			if result["close_state"] != expected {
				t.Fatal(result)
			}
			if scenario == "previous-timeout" && f.calls != 1 {
				t.Fatal("duplicate submission")
			}
		})
	}
}
