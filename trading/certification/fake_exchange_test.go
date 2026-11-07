package certification

// This file is a local exchange simulator plus the fixture the contract tests
// drive. Nothing here talks to a venue: both adapters are pointed at an
// httptest server that speaks the venue wire format, so the real signing,
// serialisation and receipt-validation code runs unchanged.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/trader/binance"
	"nofx/trader/execution"
	"nofx/trader/okx"
	"nofx/trading/risk"
)

const (
	venueBinance = "binance"
	venueOKX     = "okx"

	binanceSymbol = "BTCUSDT"
	okxSymbol     = "BTC-USDT-SWAP"
	okxCtVal      = "0.01"

	// venueTakerRate is the taker rate both venues publish. The cost replay is
	// priced from it, so a changed venue rate breaks the assertion instead of
	// shifting both sides of the comparison together.
	venueTakerRate = "0.0005"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("certification: not a number: " + s)
	}
	return r
}

func dec(r *big.Rat) string { return r.FloatString(8) }

// requiredRat parses a request number. A malformed request is answered with the
// venue's own bad-parameter error instead of crashing the test process.
func (f *fakeExchange) requiredRat(w http.ResponseWriter, raw string) (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(raw)
	if !ok || raw == "" {
		f.badParameter(w, raw)
		return nil, false
	}
	return r, true
}

func (f *fakeExchange) badParameter(w http.ResponseWriter, raw string) {
	if f.venue == venueOKX {
		okxError(w, "51000", "Parameter sz or px error: "+raw)
		return
	}
	binanceError(w, 400, -1102, "Illegal characters found in parameter '"+raw+"'.")
}

// nativeOrder is what the venue record holds, independent of adapter types.
type nativeOrder struct {
	clientID   string
	orderID    string
	role       string // entry, sl, tp, exit
	side       string // buy, sell
	kind       string // limit, market, stop_market
	quantity   *big.Rat
	price      *big.Rat
	filled     *big.Rat
	status     string
	reduceOnly bool
}

func (o *nativeOrder) terminal() bool {
	switch o.status {
	case "FILLED", "CANCELED", "REJECTED", "EXPIRED":
		return true
	}
	return false
}

func (o *nativeOrder) live() bool {
	switch o.status {
	case "NEW", "PARTIALLY_FILLED", "TRIGGERED":
		return true
	}
	return false
}

// exchangeFaults is the fault plan a scenario installs. Every fault is an
// exchange-side behaviour: no product code carries a switch for any of them.
type exchangeFaults struct {
	dropEntryResponse string // accept and fill, then lose the response
	entryResting      bool   // accept the entry without filling it
	entryPartialFill  string // fraction of the entry filled on accept
	dropProtectionAck bool   // accept the protection order, then fail the response
	rejectSL          bool
	rejectTP          bool
	fillOnCancel      bool // the resting entry fills while its cancel is in flight
	readRejected      bool // every signed read is rejected
	marketStale       bool
}

type fakeExchange struct {
	t     *testing.T
	venue string
	srv   *httptest.Server
	now   func() time.Time
	mu    sync.Mutex

	position *big.Rat
	entry    *big.Rat

	orders map[string]*nativeOrder
	nextID int

	entryPOSTs   int
	protectPOSTs int
	exitPOSTs    int
	cancels      int

	entryRecord   string // last rendered entry receipt
	protectRecord string // rendered protective-stop receipt

	faults exchangeFaults
}

func newFakeExchange(t *testing.T, venue string, now func() time.Time) *fakeExchange {
	t.Helper()
	if venue != venueBinance && venue != venueOKX {
		t.Fatalf("unknown venue %q", venue)
	}
	f := &fakeExchange{t: t, venue: venue, now: now, position: new(big.Rat), entry: new(big.Rat), orders: map[string]*nativeOrder{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeExchange) url() string          { return f.srv.URL }
func (f *fakeExchange) client() *http.Client { return f.srv.Client() }

// takerBPS is the taker rate the venue publishes, in basis points.
func (f *fakeExchange) takerBPS() execution.Decimal {
	bps := new(big.Rat).Mul(rat(venueTakerRate), big.NewRat(10_000, 1))
	value, err := execution.ExactDecimal(bps)
	if err != nil {
		panic("certification: taker rate is not exact: " + err.Error())
	}
	return value
}

func (f *fakeExchange) setFaults(plan exchangeFaults) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults = plan
}

func (f *fakeExchange) setPosition(q *big.Rat, entry *big.Rat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.position = new(big.Rat).Set(q)
	f.entry = new(big.Rat).Set(entry)
}

func (f *fakeExchange) positionNow() *big.Rat {
	f.mu.Lock()
	defer f.mu.Unlock()
	return new(big.Rat).Set(f.position)
}

func (f *fakeExchange) liveNow() []*nativeOrder {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*nativeOrder{}
	for _, o := range f.orders {
		if o.live() {
			out = append(out, o)
		}
	}
	return out
}

func (f *fakeExchange) countsNow() (entry, protect, exit, cancels int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entryPOSTs, f.protectPOSTs, f.exitPOSTs, f.cancels
}

func (f *fakeExchange) byRole(role string) []*nativeOrder {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*nativeOrder{}
	for _, o := range f.orders {
		if o.role == role {
			out = append(out, o)
		}
	}
	return out
}

func (f *fakeExchange) entryReceipt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entryRecord
}

func (f *fakeExchange) protectionReceipt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.protectRecord
}

// applyFill books a fill against the venue record. Caller holds the lock.
func (f *fakeExchange) applyFill(o *nativeOrder, quantity *big.Rat) {
	o.filled.Add(o.filled, quantity)
	if o.filled.Cmp(o.quantity) >= 0 {
		o.filled = new(big.Rat).Set(o.quantity)
		o.status = "FILLED"
	} else {
		o.status = "PARTIALLY_FILLED"
	}
	if o.reduceOnly {
		f.position.Sub(f.position, quantity)
		return
	}
	f.position.Add(f.position, quantity)
	if f.entry.Sign() == 0 && o.price != nil && o.price.Sign() > 0 {
		f.entry = new(big.Rat).Set(o.price)
	}
}

func newNativeOrder(clientID, role, side, kind string, quantity, price *big.Rat, reduceOnly bool) *nativeOrder {
	return &nativeOrder{
		clientID: clientID, role: role, side: side, kind: kind,
		quantity: quantity, price: price, filled: new(big.Rat),
		status: "NEW", reduceOnly: reduceOnly,
	}
}

// param reads a wire parameter from both the query string and the form body.
func param(r *http.Request, name string) string {
	if v := r.PostForm.Get(name); v != "" {
		return v
	}
	return r.URL.Query().Get(name)
}

func (f *fakeExchange) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/api/v5/") {
		f.parseBinanceParams(r)
		f.serveBinance(w, r)
		return
	}
	f.serveOKX(w, r)
}

// parseBinanceParams reads the signed form body, which go-binance sends for
// POST and DELETE alike.
func (f *fakeExchange) parseBinanceParams(r *http.Request) {
	if r.Method == http.MethodGet {
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		return
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return
	}
	if r.PostForm == nil {
		r.PostForm = url.Values{}
	}
	for key, value := range values {
		r.PostForm[key] = value
	}
}

func (f *fakeExchange) json(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// ---------------------------------------------------------------- Binance

func (f *fakeExchange) serveBinance(w http.ResponseWriter, r *http.Request) {
	if f.faults.readRejected && r.Method == http.MethodGet && r.URL.Path != "/fapi/v1/time" {
		binanceError(w, 400, -1021, "Timestamp for this request is outside of the recvWindow.")
		return
	}
	switch r.URL.Path {
	case "/fapi/v1/time":
		f.json(w, 200, fmt.Sprintf(`{"serverTime":%d}`, f.now().UnixMilli()))
	case "/fapi/v1/positionSide/dual":
		f.json(w, 200, `{"dualSidePosition":false}`)
	case "/fapi/v2/account":
		f.json(w, 200, f.binanceAccount())
	case "/fapi/v2/positionRisk":
		f.json(w, 200, "["+f.binanceRisk()+"]")
	case "/fapi/v1/openOrders":
		f.json(w, 200, f.binanceOpenOrders())
	case "/fapi/v1/openAlgoOrders":
		f.json(w, 200, f.binanceOpenAlgos())
	case "/fapi/v1/commissionRate":
		f.json(w, 200, `{"symbol":"BTCUSDT","makerCommissionRate":"0.0002","takerCommissionRate":"`+venueTakerRate+`"}`)
	case "/fapi/v1/leverage":
		f.json(w, 200, fmt.Sprintf(`{"symbol":%q,"leverage":3,"maxNotionalValue":"1000000"}`, binanceSymbol))
	case "/fapi/v1/order":
		f.binanceOrder(w, r)
	case "/fapi/v1/algoOrder":
		f.binanceAlgo(w, r)
	default:
		f.t.Errorf("certification: unexpected Binance endpoint %s %s", r.Method, r.URL.Path)
		binanceError(w, 404, -1, "not found")
	}
}

func binanceError(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%d,"msg":%q}`, code, msg)
}

func (f *fakeExchange) binanceAccount() string {
	positions := "[]"
	if f.position.Sign() != 0 {
		positions = "[" + f.binanceRisk() + "]"
	}
	return fmt.Sprintf(`{"feeTier":0,"canTrade":true,"canDeposit":true,"canWithdraw":true,`+
		`"updateTime":%d,"multiAssetsMargin":false,"totalInitialMargin":"0","totalMaintMargin":"0",`+
		`"totalWalletBalance":"10000","totalUnrealizedProfit":"0","totalMarginBalance":"10000",`+
		`"totalPositionInitialMargin":"0","totalOpenOrderInitialMargin":"0",`+
		`"totalCrossWalletBalance":"10000","totalCrossUnPnl":"0","availableBalance":"10000",`+
		`"maxWithdrawAmount":"10000","positions":%s}`,
		f.now().UnixMilli(), positions)
}

func (f *fakeExchange) binanceRisk() string {
	return fmt.Sprintf(`{"symbol":%q,"positionAmt":%q,"entryPrice":%q,"markPrice":%q,`+
		`"unRealizedProfit":"0","liquidationPrice":"0","leverage":"3","maxNotionalValue":"1000000",`+
		`"marginType":"isolated","isolatedMargin":"0","isAutoAddMargin":"false","positionSide":"BOTH",`+
		`"notional":"0","isolatedWallet":"0","updateTime":%d}`,
		binanceSymbol, dec(f.position), dec(f.entry), dec(f.entry), f.now().UnixMilli())
}

func (f *fakeExchange) binanceOrderJSON(o *nativeOrder) string {
	typ := "LIMIT"
	if o.kind == "market" {
		typ = "MARKET"
	}
	price := "0"
	if o.price != nil {
		price = dec(o.price)
	}
	return fmt.Sprintf(`{"symbol":%q,"orderId":%s,"clientOrderId":%q,"price":%q,"origQty":%q,`+
		`"executedQty":%q,"cumQty":%q,"cumQuote":"0","avgPrice":%q,"status":%q,"timeInForce":"GTC",`+
		`"type":%q,"side":%q,"stopPrice":"0","time":%d,"updateTime":%d,"workingType":"CONTRACT_PRICE",`+
		`"origType":%q,"positionSide":"BOTH","reduceOnly":%v,"closePosition":false,"priceProtect":false}`,
		binanceSymbol, o.orderID, o.clientID, price, dec(o.quantity), dec(o.filled), dec(o.filled),
		dec(f.entry), o.status, typ, strings.ToUpper(o.side), f.now().UnixMilli(), f.now().UnixMilli(),
		typ, o.reduceOnly)
}

func (f *fakeExchange) binanceAlgoJSON(o *nativeOrder) string {
	typ := "STOP_MARKET"
	if o.role == "tp" {
		typ = "TAKE_PROFIT_MARKET"
	}
	price := "0"
	if o.price != nil {
		price = dec(o.price)
	}
	return fmt.Sprintf(`{"algoId":%s,"clientAlgoId":%q,"algoType":"CONDITIONAL","orderType":%q,`+
		`"symbol":%q,"side":%q,"positionSide":"BOTH","timeInForce":"GTC","quantity":%q,"algoStatus":%q,`+
		`"triggerPrice":%q,"price":"0","closePosition":false,"reduceOnly":%v,"workingType":"CONTRACT_PRICE",`+
		`"priceProtect":false,"createTime":%d,"updateTime":%d,"triggerTime":0,"actualOrderId":""}`,
		o.orderID, o.clientID, typ, binanceSymbol, strings.ToUpper(o.side), dec(o.quantity), o.status,
		price, o.reduceOnly, f.now().UnixMilli(), f.now().UnixMilli())
}

func (f *fakeExchange) binanceOpenOrders() string {
	parts := []string{}
	for _, o := range f.orders {
		if (o.role == "entry" || o.role == "exit") && o.live() {
			parts = append(parts, f.binanceOrderJSON(o))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (f *fakeExchange) binanceOpenAlgos() string {
	parts := []string{}
	for _, o := range f.orders {
		if (o.role == "sl" || o.role == "tp") && o.live() {
			parts = append(parts, f.binanceAlgoJSON(o))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (f *fakeExchange) binanceOrder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		kind := "limit"
		if strings.EqualFold(param(r, "type"), "MARKET") {
			kind = "market"
		}
		reduceOnly := strings.EqualFold(param(r, "reduceOnly"), "true")
		role := "entry"
		if reduceOnly || kind == "market" {
			role = "exit"
		}
		if role == "entry" {
			f.entryPOSTs++
		} else {
			f.exitPOSTs++
		}
		price := new(big.Rat)
		if raw := param(r, "price"); raw != "" {
			price = rat(raw)
		}
		quantity, ok := f.requiredRat(w, param(r, "quantity"))
		if !ok {
			return
		}
		client := param(r, "newClientOrderId")
		f.nextID++
		o := newNativeOrder(client, role, strings.ToLower(param(r, "side")), kind, quantity, price, reduceOnly)
		o.orderID = strconv.Itoa(f.nextID)
		switch {
		case role == "entry" && f.faults.entryResting:
		case role == "entry" && f.faults.entryPartialFill != "":
			f.applyFill(o, new(big.Rat).Mul(o.quantity, rat(f.faults.entryPartialFill)))
		default:
			f.applyFill(o, o.quantity)
		}
		f.orders[client] = o
		if role == "entry" {
			f.entryRecord = f.binanceOrderJSON(o)
		}
		// The order is on the venue record either way: a lost response does not
		// undo an accepted order.
		if role == "entry" && f.faults.dropEntryResponse != "" {
			binanceError(w, 500, -1007, f.faults.dropEntryResponse)
			return
		}
		f.json(w, 200, f.binanceOrderJSON(o))
	case http.MethodGet:
		o := f.orders[param(r, "origClientOrderId")]
		if o == nil {
			binanceError(w, 400, -2013, "Order does not exist.")
			return
		}
		f.json(w, 200, f.binanceOrderJSON(o))
	case http.MethodDelete:
		client := param(r, "origClientOrderId")
		o := f.orders[client]
		f.cancels++
		if o == nil {
			binanceError(w, 400, -2011, "Unknown order sent.")
			return
		}
		if f.faults.fillOnCancel && o.live() && !o.terminal() {
			f.applyFill(o, new(big.Rat).Sub(o.quantity, o.filled))
		}
		if !o.terminal() {
			o.status = "CANCELED"
		}
		f.json(w, 200, f.binanceOrderJSON(o))
	}
}

func (f *fakeExchange) binanceAlgo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		typ := strings.ToUpper(param(r, "type"))
		role := "sl"
		if strings.HasPrefix(typ, "TAKE_PROFIT") {
			role = "tp"
		}
		client := param(r, "clientAlgoId")
		f.protectPOSTs++
		if (role == "sl" && f.faults.rejectSL) || (role == "tp" && f.faults.rejectTP) {
			binanceError(w, 400, -2021, "Order would immediately trigger.")
			return
		}
		quantity, ok := f.requiredRat(w, param(r, "quantity"))
		if !ok {
			return
		}
		trigger, ok := f.requiredRat(w, param(r, "triggerPrice"))
		if !ok {
			return
		}
		f.nextID++
		o := newNativeOrder(client, role, strings.ToLower(param(r, "side")), "stop_market",
			quantity, trigger, strings.EqualFold(param(r, "reduceOnly"), "true"))
		o.orderID = strconv.Itoa(100 + f.nextID)
		f.orders[client] = o
		if o.role == "sl" {
			f.protectRecord = f.binanceAlgoJSON(o)
		}
		if f.faults.dropProtectionAck {
			binanceError(w, 500, -1007, "Timeout waiting for response from backend server.")
			return
		}
		f.json(w, 200, f.binanceAlgoJSON(o))
	case http.MethodGet:
		o := f.orders[param(r, "clientAlgoId")]
		if o == nil {
			binanceError(w, 400, -2013, "Order does not exist.")
			return
		}
		f.json(w, 200, f.binanceAlgoJSON(o))
	case http.MethodDelete:
		o := f.orders[param(r, "clientAlgoId")]
		f.cancels++
		if o == nil {
			binanceError(w, 400, -2011, "Unknown order sent.")
			return
		}
		if !o.terminal() {
			o.status = "CANCELED"
		}
		f.json(w, 200, fmt.Sprintf(`{"algoId":%s,"clientAlgoId":%q,"code":"200","msg":"success"}`,
			o.orderID, o.clientID))
	}
}

// -------------------------------------------------------------------- OKX

func okxData(data string) string { return `{"code":"0","msg":"","data":` + data + `}` }

func okxError(w http.ResponseWriter, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"code":%q,"msg":%q,"data":[]}`, code, msg)
}

func (f *fakeExchange) serveOKX(w http.ResponseWriter, r *http.Request) {
	if f.faults.readRejected && r.Method == http.MethodGet {
		okxError(w, "50011", "Too Many Requests")
		return
	}
	switch r.URL.Path {
	case "/api/v5/public/instruments":
		f.json(w, 200, okxData(`[{"instId":"BTC-USDT-SWAP","instFamily":"BTC-USDT","groupId":"","instType":"SWAP","state":"live","ctType":"linear","settleCcy":"USDT","ctValCcy":"BTC","ctVal":"0.01","ctMult":"1","lotSz":"0.01","minSz":"0.01","tickSz":"0.1","maxMktSz":"10000","maxLmtSz":"10000"}]`))
	case "/api/v5/account/config":
		f.json(w, 200, okxData(`[{"posMode":"net_mode","acctLv":"2","uid":"venue-1"}]`))
	case "/api/v5/account/balance":
		f.json(w, 200, okxData(`[{"details":[{"ccy":"USDT","eq":"10000","availEq":"10000","availBal":"10000"}]}]`))
	case "/api/v5/account/trade-fee":
		f.json(w, 200, okxData(`[{"takerU":"-`+venueTakerRate+`"}]`))
	case "/api/v5/account/positions":
		f.json(w, 200, okxData(f.okxPositions()))
	case "/api/v5/trade/orders-pending":
		f.json(w, 200, okxData(f.okxPendingOrders()))
	case "/api/v5/trade/orders-algo-pending":
		f.json(w, 200, okxData(f.okxPendingAlgos(r.URL.Query().Get("ordType"))))
	case "/api/v5/account/set-leverage":
		f.json(w, 200, okxData(`[{"lever":"3"}]`))
	case "/api/v5/trade/order":
		f.okxOrder(w, r)
	case "/api/v5/trade/order-algo":
		f.okxAlgo(w, r)
	case "/api/v5/trade/cancel-order":
		f.okxCancel(w, r)
	case "/api/v5/trade/cancel-algos":
		f.okxCancelAlgo(w, r)
	default:
		f.t.Errorf("certification: unexpected OKX endpoint %s %s", r.Method, r.URL.Path)
		okxError(w, "404", "not found")
	}
}

func (f *fakeExchange) okxContracts(q *big.Rat) string {
	return new(big.Rat).Quo(q, rat(okxCtVal)).FloatString(2)
}

func (f *fakeExchange) okxPositions() string {
	if f.position.Sign() == 0 {
		return "[]"
	}
	return fmt.Sprintf(`[{"instId":"BTC-USDT-SWAP","pos":%q,"posSide":"net","mgnMode":"cross","avgPx":%q,`+
		`"markPx":%q,"upl":"0","lever":"3","liqPx":"0","imr":"0"}]`,
		f.okxContracts(f.position), dec(f.entry), dec(f.entry))
}

func (f *fakeExchange) okxPendingOrders() string {
	parts := []string{}
	for _, o := range f.orders {
		if (o.role == "entry" || o.role == "exit") && o.live() {
			parts = append(parts, f.okxOrderJSON(o))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (f *fakeExchange) okxPendingAlgos(ordType string) string {
	if ordType != "conditional" {
		return "[]"
	}
	parts := []string{}
	for _, o := range f.orders {
		if (o.role == "sl" || o.role == "tp") && o.live() {
			parts = append(parts, f.okxAlgoJSON(o))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (f *fakeExchange) okxOrderJSON(o *nativeOrder) string {
	price := "0"
	if o.price != nil {
		price = dec(o.price)
	}
	kind := "limit"
	if o.kind == "market" {
		kind = "market"
	}
	return fmt.Sprintf(`{"ordId":%q,"clOrdId":%q,"instId":"BTC-USDT-SWAP","side":%q,"posSide":"net",`+
		`"ordType":%q,"px":%q,"sz":%q,"accFillSz":%q,"avgPx":%q,"state":%q,"reduceOnly":%v}`,
		o.orderID, o.clientID, o.side, kind, price,
		f.okxContracts(o.quantity), f.okxContracts(o.filled), dec(f.entry), okxOrderState(o), o.reduceOnly)
}

func (f *fakeExchange) okxAlgoJSON(o *nativeOrder) string {
	prefix := o.role
	price := "0"
	if o.price != nil {
		price = dec(o.price)
	}
	return fmt.Sprintf(`{"algoId":%q,"algoClOrdId":%q,"instId":"BTC-USDT-SWAP","side":%q,"posSide":"net",`+
		`"ordType":"conditional","sz":%q,"%sTriggerPx":%q,"%sTriggerPxType":"mark","%sOrdPx":"-1",`+
		`"state":%q,"reduceOnly":%t}`,
		o.orderID, o.clientID, o.side, f.okxContracts(o.quantity),
		prefix, price, prefix, prefix, okxAlgoState(o), o.reduceOnly)
}

func okxOrderState(o *nativeOrder) string {
	switch o.status {
	case "PARTIALLY_FILLED":
		return "partially_filled"
	case "FILLED":
		return "filled"
	case "CANCELED", "REJECTED", "EXPIRED":
		return "canceled"
	}
	return "live"
}

func okxAlgoState(o *nativeOrder) string {
	switch o.status {
	case "TRIGGERED", "FILLED":
		return "effective"
	case "CANCELED", "REJECTED", "EXPIRED":
		return "canceled"
	}
	return "live"
}

func (f *fakeExchange) okxOrder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		body := f.okxBody(r)
		client := stringOf(body["clOrdId"])
		kind := stringOf(body["ordType"])
		reduceOnly := body["reduceOnly"] == true
		role := "entry"
		if reduceOnly || kind != "limit" {
			role = "exit"
		}
		if role == "entry" {
			f.entryPOSTs++
		} else {
			f.exitPOSTs++
		}
		size, ok := f.requiredRat(w, stringOf(body["sz"]))
		if !ok {
			return
		}
		quantity := new(big.Rat).Mul(size, rat(okxCtVal))
		price := new(big.Rat)
		if raw := stringOf(body["px"]); raw != "" {
			price = rat(raw)
		}
		f.nextID++
		o := newNativeOrder(client, role, stringOf(body["side"]), kind, quantity, price, reduceOnly)
		o.orderID = strconv.Itoa(f.nextID)
		switch {
		case role == "entry" && f.faults.entryResting:
		case role == "entry" && f.faults.entryPartialFill != "":
			f.applyFill(o, new(big.Rat).Mul(o.quantity, rat(f.faults.entryPartialFill)))
		default:
			f.applyFill(o, o.quantity)
		}
		f.orders[client] = o
		if role == "entry" {
			f.entryRecord = f.okxOrderJSON(o)
		}
		if role == "entry" && f.faults.dropEntryResponse != "" {
			okxError(w, "50013", f.faults.dropEntryResponse)
			return
		}
		f.json(w, 200, okxData(fmt.Sprintf(`[{"ordId":%q,"clOrdId":%q,"sCode":"0","sMsg":""}]`, o.orderID, client)))
	case http.MethodGet:
		o := f.orders[r.URL.Query().Get("clOrdId")]
		if o == nil {
			f.json(w, 200, okxData(`[]`))
			return
		}
		f.json(w, 200, okxData("["+f.okxOrderJSON(o)+"]"))
	}
}

func (f *fakeExchange) okxAlgo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		body := f.okxBody(r)
		client := stringOf(body["algoClOrdId"])
		role := "sl"
		price := stringOf(body["slTriggerPx"])
		if price == "" {
			role, price = "tp", stringOf(body["tpTriggerPx"])
		}
		f.protectPOSTs++
		if (role == "sl" && f.faults.rejectSL) || (role == "tp" && f.faults.rejectTP) {
			f.json(w, 200, okxData(`[{"algoId":"","sCode":"51169","sMsg":"Order would immediately trigger."}]`))
			return
		}
		size, ok := f.requiredRat(w, stringOf(body["sz"]))
		if !ok {
			return
		}
		quantity := new(big.Rat).Mul(size, rat(okxCtVal))
		trigger, ok := f.requiredRat(w, price)
		if !ok {
			return
		}
		f.nextID++
		o := newNativeOrder(client, role, stringOf(body["side"]), "stop_market", quantity, trigger, true)
		o.orderID = strconv.Itoa(100 + f.nextID)
		f.orders[client] = o
		if o.role == "sl" {
			f.protectRecord = f.okxAlgoJSON(o)
		}
		if f.faults.dropProtectionAck {
			okxError(w, "50013", "Systems are busy. Please try again later.")
			return
		}
		f.json(w, 200, okxData(fmt.Sprintf(`[{"algoId":%q,"algoClOrdId":%q,"sCode":"0","sMsg":""}]`, o.orderID, client)))
	case http.MethodGet:
		o := f.orders[r.URL.Query().Get("algoClOrdId")]
		if o == nil {
			f.json(w, 200, okxData(`[]`))
			return
		}
		f.json(w, 200, okxData("["+f.okxAlgoJSON(o)+"]"))
	}
}

func (f *fakeExchange) okxCancel(w http.ResponseWriter, r *http.Request) {
	body := f.okxBody(r)
	client := stringOf(body["clOrdId"])
	o := f.orders[client]
	f.cancels++
	if o != nil && f.faults.fillOnCancel && o.live() && !o.terminal() {
		f.applyFill(o, new(big.Rat).Sub(o.quantity, o.filled))
	}
	if o != nil && !o.terminal() {
		o.status = "CANCELED"
	}
	orderID := ""
	if o != nil {
		orderID = o.orderID
	}
	f.json(w, 200, okxData(fmt.Sprintf(`[{"ordId":%q,"clOrdId":%q,"sCode":"0","sMsg":""}]`, orderID, client)))
}

func (f *fakeExchange) okxCancelAlgo(w http.ResponseWriter, r *http.Request) {
	var rows []map[string]any
	if err := jsonUnmarshal(r, &rows); err != nil {
		f.t.Errorf("certification: bad cancel-algos body: %v", err)
	}
	parts := []string{}
	for _, row := range rows {
		algoID := stringOf(row["algoId"])
		f.cancels++
		for _, o := range f.orders {
			if o.orderID == algoID && !o.terminal() {
				o.status = "CANCELED"
			}
		}
		parts = append(parts, fmt.Sprintf(`{"algoId":%q,"sCode":"0","sMsg":""}`, algoID))
	}
	f.json(w, 200, okxData("["+strings.Join(parts, ",")+"]"))
}

func (f *fakeExchange) okxBody(r *http.Request) map[string]any {
	body := map[string]any{}
	if err := jsonUnmarshal(r, &body); err != nil {
		f.t.Errorf("certification: bad OKX body: %v", err)
	}
	return body
}

func jsonUnmarshal(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}

func stringOf(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	}
	return ""
}

// ---------------------------------------------------------------- fixture

type fixtureMarket struct {
	mu    sync.Mutex
	base  core.Snapshot
	stale bool
}

func (m *fixtureMarket) Snapshot(_ context.Context, venue, symbol string) (core.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.base
	s.Venue = venue
	s.Symbol = symbol
	s.Observed = time.Now().UTC()
	if m.stale {
		s.Observed = s.Observed.Add(-30 * time.Second)
	}
	s.SourceTime = s.Observed
	return s, nil
}

func (m *fixtureMarket) setStale(stale bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stale = stale
}

func fixtureSnapshot(venue string) core.Snapshot {
	s := core.Snapshot{
		Kind: "perpetual", BaseAsset: "BTC", QuoteAsset: "USDT",
		Bid: "99999", Ask: "100000", BidSize: "1000", AskSize: "1000",
		Bids: []core.BookLevel{{Price: "99999", Quantity: "1000"}},
		Asks: []core.BookLevel{{Price: "100000", Quantity: "1000"}},
		Tick: "1", Step: "0.001", MinQty: "0.001", MinNotional: "10",
	}
	if venue == venueOKX {
		s.ContractValue = okxCtVal
		s.Tick = "0.1"
		s.Step = "0.01"
		s.MinQty = "0.01"
	}
	return s
}

type exchangeFixture struct {
	t      *testing.T
	venue  string
	symbol string

	clock  time.Time
	fake   *fakeExchange
	db     *gorm.DB
	dbPath string
	ctrl   *live.Controller
	risks  *risk.Store
	book   *fixtureMarket
	venue2 execution.ManagedTrader

	binding     execution.Binding
	policy      risk.Policy
	plan        core.Plan
	constraints core.Constraints
	idea        core.Idea
	account     execution.ManagedAccount
	drafts      planning.DraftStore

	approved   live.View
	executeErr error

	dbFailure  *atomic.Bool
	armFailure bool
}

func newExchangeFixture(t *testing.T, venue string) *exchangeFixture {
	t.Helper()
	f := &exchangeFixture{t: t, venue: venue, clock: time.Now().UTC(), dbFailure: new(atomic.Bool)}
	f.symbol = binanceSymbol
	if venue == venueOKX {
		f.symbol = okxSymbol
	}
	ctx := context.Background()

	f.fake = newFakeExchange(t, venue, f.now)

	// The path is kept so a helper process can open the same file and prove
	// recovery against state the kernel actually persisted.
	dbPath := filepath.Join(t.TempDir(), "certification.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open certification db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("certification db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	f.db = db
	f.dbPath = dbPath

	f.book = &fixtureMarket{base: fixtureSnapshot(venue)}
	risks, err := risk.NewStore(db)
	if err != nil {
		t.Fatalf("risk store: %v", err)
	}
	f.risks = risks

	f.binding = execution.Binding{
		ID: "account", UserID: "alice", ExchangeID: "exchange",
		Venue: venue, Environment: execution.EnvironmentSandbox,
		VenueAccountID: "venue-1", VerifiedAt: f.clock, Enabled: true,
	}

	switch venue {
	case venueBinance:
		tr, err := binance.NewManagedAccountTraderWithEndpoint(ctx, "test-key", "test-secret", "",
			execution.EnvironmentSandbox, &binance.ManagedEndpoint{BaseURL: f.fake.url(), Client: f.fake.client()})
		if err != nil {
			t.Fatalf("binance adapter: %v", err)
		}
		f.venue2 = tr
	case venueOKX:
		tr, err := okx.NewManagedAccountTraderWithEndpoint(ctx, "test-key", "test-secret", "pass",
			execution.EnvironmentSandbox, &okx.ManagedEndpoint{BaseURL: f.fake.url(), Client: f.fake.client()})
		if err != nil {
			t.Fatalf("okx adapter: %v", err)
		}
		f.venue2 = tr
	}

	ctrl, err := live.New(db, f.factory(), f.book)
	if err != nil {
		t.Fatalf("live controller: %v", err)
	}
	ctrl.Now = f.now
	ctrl.Interval = -1
	ctrl.Risk = risks
	f.ctrl = ctrl
	t.Cleanup(ctrl.Shutdown)

	// live.New owns the schema for runs and account bindings.
	bound, err := execution.BindManagedAccount(ctx, db, f.binding)
	if err != nil {
		t.Fatalf("bind managed account: %v", err)
	}
	f.binding = bound

	f.policy = risk.DefaultPolicy("alice", f.binding.ID, "portfolio", venue, execution.EnvironmentSandbox)
	f.policy.Symbols = []string{f.symbol}
	f.policy.Enabled = true
	version, err := risk.PolicyVersion(f.policy)
	if err != nil {
		t.Fatalf("policy version: %v", err)
	}
	f.policy.Version = version
	stored, err := f.risks.PutPolicy(ctx, f.policy)
	if err != nil {
		t.Fatalf("put policy: %v", err)
	}
	f.policy = stored
	account := execution.ManagedAccount{
		AccountKey: f.binding.ID, Venue: venue, Environment: execution.EnvironmentSandbox,
		PositionMode: "one-way", MarginMode: "isolated",
		Equity: "10000", Available: "10000", MarginUsed: "0", ObservedAt: f.clock,
	}
	f.account = account
	dayStart := f.clock.Truncate(24 * time.Hour).Add(time.Second)
	if err := f.risks.Observe(ctx, account, nil, dayStart); err != nil {
		t.Fatalf("observe day start: %v", err)
	}
	if err := f.risks.Observe(ctx, account, nil, f.clock); err != nil {
		t.Fatalf("observe now: %v", err)
	}

	f.drafts = planning.DraftStore{DB: db, UserID: "alice", ExchangeID: "exchange", Venue: venue}
	snapshot := fixtureSnapshot(venue)
	snapshot.Venue = venue
	snapshot.Symbol = f.symbol
	snapshot.Observed = time.Now().UTC()
	snapshot.SourceTime = snapshot.Observed
	constraints := core.Constraints{
		Leverage: 3, AvailableMargin: "10000", Equity: "10000",
		MaxRisk: "50", MaxNotional: "30000", MinRewardRisk: "2",
		FeeBPS: "5", SlippageBPS: "10", FundingReserveBPS: "10",
	}
	f.constraints = constraints
	f.idea = core.Idea{Name: "signal-1", Side: "long", Entry: "100000", Stop: "99000", Target: "104000"}
	plan, err := core.Compile(snapshot, f.constraints, f.idea, time.Now().UTC())
	if err != nil {
		t.Fatalf("compile plan: %v", err)
	}
	f.plan = plan

	if err := f.drafts.Add([]core.Plan{plan}); err != nil {
		t.Fatalf("store draft: %v", err)
	}

	// A single injected commit failure: it proves nothing is sent when the
	// intent is not durable.
	if err := db.Callback().Update().Before("gorm:update").Register("certification:fail-once", func(tx *gorm.DB) {
		if f.dbFailure.Load() && tx.Statement.Table == "planning_live_runs" {
			f.dbFailure.Store(false)
			_ = tx.AddError(errors.New("database is locked"))
		}
	}); err != nil {
		t.Fatalf("register db failure hook: %v", err)
	}
	return f
}

func (f *exchangeFixture) now() time.Time { return f.clock }

func (f *exchangeFixture) factory() live.Factory {
	return func(context.Context, string, string) (live.Broker, string, error) {
		return live.NOFXBroker{Trader: f.venue2}, f.binding.ID, nil
	}
}

// Scenario installs the exchange-side fault plan for one contract scenario.
func (f *exchangeFixture) Scenario(name string) *exchangeFixture {
	f.t.Helper()
	var faults exchangeFaults
	switch name {
	case "accepted_response_lost":
		faults.dropEntryResponse = "Timeout waiting for response from backend server."
	case "partial_fill_restart":
		faults.entryPartialFill = "0.5"
	case "sl_accepted_response_lost":
		faults.dropProtectionAck = true
	case "sl_rejected":
		faults.rejectSL = true
	case "tp_rejected":
		faults.rejectTP = true
	case "cancel_races_fill":
		faults.entryResting = true
		faults.fillOnCancel = true
	case "manual_close_races_sl":
	case "stale_snapshot_and_ws_gap":
		faults.marketStale = true
	case "rate_limit_and_clock_skew":
		faults.readRejected = true
	case "db_commit_failure":
		f.armFailure = true
	default:
		f.t.Fatalf("certification: unknown scenario %q", name)
	}
	f.fake.setFaults(faults)
	f.book.setStale(faults.marketStale)
	return f
}

// Run approves and executes the locked plan. The controller runs with a
// negative interval, so no worker starts and the caller drives every cycle.
func (f *exchangeFixture) Run() {
	f.t.Helper()
	view, err := f.ctrl.Approve(context.Background(), "alice", f.plan.ID, f.plan.Hash, "APPROVE LIVE")
	if err != nil {
		f.t.Fatalf("approve plan: %v", err)
	}
	f.approved = view
	f.dbFailure.Store(f.armFailure)
	_, f.executeErr = f.ctrl.Execute(context.Background(), "alice", view.ID, view.ApprovalHash)
}

// Step advances one supervision cycle.
func (f *exchangeFixture) Step() bool {
	f.t.Helper()
	return f.ctrl.StepContext(context.Background(), "alice", f.approved.ID)
}

// Steps advances at most n cycles, advancing the fixture clock one second per
// cycle, which is what a real supervision loop does between ticks.
func (f *exchangeFixture) Steps(n int) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		f.clock = f.clock.Add(time.Second)
		if f.Step() {
			return
		}
	}
}

func (f *exchangeFixture) Advance(d time.Duration) { f.clock = f.clock.Add(d) }

func (f *exchangeFixture) run() live.View {
	f.t.Helper()
	view, err := f.ctrl.Get("alice", f.approved.ID)
	if err != nil {
		f.t.Fatalf("read run: %v", err)
	}
	return view
}

func (f *exchangeFixture) Flatten() error {
	_, err := f.ctrl.Flatten(context.Background(), "alice", f.approved.ID, "certification scenario")
	return err
}

func (f *exchangeFixture) Stop() error {
	_, err := f.ctrl.Stop("alice", f.approved.ID, f.approved.ApprovalHash)
	return err
}

// AssertFlatAndNoRiskOrders fails when the venue record still carries exposure
// or a live order that could open one. A reduce-only order left behind by a
// halt cannot open exposure on its own, so the run must keep its lease instead
// (AssertHalted covers that).
func (f *exchangeFixture) AssertFlatAndNoRiskOrders(t *testing.T) {
	t.Helper()
	if position := f.fake.positionNow(); position.Sign() != 0 {
		t.Fatalf("%s position is %s, want flat", f.venue, dec(position))
	}
	for _, o := range f.fake.liveNow() {
		if !o.reduceOnly {
			t.Fatalf("%s order %s (%s) is still %s and could open exposure", f.venue, o.clientID, o.role, o.status)
		}
	}
}

// AssertNoOpenOrders fails when any order is still working at the venue.
func (f *exchangeFixture) AssertNoOpenOrders(t *testing.T) {
	t.Helper()
	for _, o := range f.fake.liveNow() {
		t.Fatalf("%s order %s (%s) is still %s", f.venue, o.clientID, o.role, o.status)
	}
}

// AssertHalted fails unless the run stopped on the venue record and kept its
// account lease, which is what blocks the next entry.
func (f *exchangeFixture) AssertHalted(t *testing.T, want string) {
	t.Helper()
	v := f.run()
	if v.State != want {
		t.Fatalf("%s run state is %q, want %q", f.venue, v.State, want)
	}
	var leases int64
	if err := f.db.Model(&live.Lease{}).Count(&leases).Error; err != nil {
		t.Fatalf("count leases: %v", err)
	}
	if leases != 1 {
		t.Fatalf("%s holds %d leases after a halt, want 1", f.venue, leases)
	}
}

// AssertAtMostOneEntry fails when the same signal produced more than one entry
// submission at the venue.
func (f *exchangeFixture) AssertAtMostOneEntry(t *testing.T, signalID string) {
	t.Helper()
	entry, _, _, _ := f.fake.countsNow()
	if entry > 1 {
		t.Fatalf("%s signal %s submitted %d entry orders, want at most 1", f.venue, signalID, entry)
	}
}

// ClosePosition flattens the venue record the way an operator would, keeping
// the entry price the venue keeps for the closed trade.
func (f *exchangeFixture) ClosePosition() {
	f.fake.mu.Lock()
	f.fake.position = new(big.Rat)
	f.fake.mu.Unlock()
}

// Replan compiles a second plan and approves it. A retired approval cannot be
// reused, so a scenario that retries needs a fresh draft.
func (f *exchangeFixture) Replan() {
	f.t.Helper()
	f.clock = f.clock.Add(time.Minute)
	// A live account reports fresh equity; without it the risk baseline is
	// stale and every plan is refused.
	f.account.ObservedAt = f.now()
	if err := f.risks.Observe(context.Background(), f.account, nil, f.now()); err != nil {
		f.t.Fatalf("observe replanned equity: %v", err)
	}
	snapshot, err := f.book.Snapshot(context.Background(), f.venue, f.symbol)
	if err != nil {
		f.t.Fatalf("replan snapshot: %v", err)
	}
	snapshot.Venue, snapshot.Symbol = f.venue, f.symbol
	snapshot.Observed, snapshot.SourceTime = f.now(), f.now()
	plan, err := core.Compile(snapshot, f.constraints, f.idea, f.now())
	if err != nil {
		f.t.Fatalf("recompile plan: %v", err)
	}
	if err := f.drafts.Add([]core.Plan{plan}); err != nil {
		f.t.Fatalf("store replanned draft: %v", err)
	}
	f.plan = plan
	f.Run()
}

// ClearReads lets signed reads through again after a rate-limit or clock fault.
func (f *exchangeFixture) ClearReads() {
	f.fake.mu.Lock()
	f.fake.faults.readRejected = false
	f.fake.mu.Unlock()
}

// Restart builds a second controller over the same database, the way a process
// restart would, and keeps the fixture pointed at it.
func (f *exchangeFixture) Restart() {
	f.t.Helper()
	c, err := live.New(f.db, f.factory(), f.book)
	if err != nil {
		f.t.Fatalf("restart controller: %v", err)
	}
	c.Now = f.now
	c.Interval = -1
	c.Risk = f.risks
	f.ctrl = c
	f.t.Cleanup(c.Shutdown)
}

// ReservationState reports the run's risk reservation, which must survive a
// restart so the account stays inside its budget.
func (f *exchangeFixture) ReservationState() string {
	f.t.Helper()
	r, err := f.risks.Reservation(context.Background(), f.approved.ID)
	if err != nil {
		f.t.Fatalf("reservation: %v", err)
	}
	return r.State
}

// FinishManualClose removes the exits after an operator closed the position.
func (f *exchangeFixture) FinishManualClose() error {
	_, err := f.ctrl.FinishManualClose(context.Background(), "alice", f.approved.ID)
	return err
}

// AssertOneEntry fails unless the signal produced exactly one entry submission.
func (f *exchangeFixture) AssertOneEntry(t *testing.T, signalID string) {
	t.Helper()
	entry, _, _, _ := f.fake.countsNow()
	if entry != 1 {
		t.Fatalf("%s signal %s submitted %d entry orders, want exactly 1", f.venue, signalID, entry)
	}
}

// AssertNoReversePosition fails when a risk-reducing action left the venue
// record short beyond flat.
func (f *exchangeFixture) AssertNoReversePosition(t *testing.T) {
	t.Helper()
	if position := f.fake.positionNow(); position.Sign() < 0 {
		t.Fatalf("%s risk-reducing action opened a reverse position of %s", f.venue, dec(position))
	}
}
