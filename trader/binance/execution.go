package binance

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/adshao/go-binance/v2/common"
	"github.com/adshao/go-binance/v2/futures"
	"math/big"
	"net/http"
	"nofx/hook"
	"nofx/trader/execution"
	"strconv"
	"strings"
	"time"
)

// RouteClientID retains all 128 bits of the durable ID and shares NOFX's
// Binance attribution prefix. Raw URL base64 needs 22 characters, so the
// complete tagged ID fits the existing 32-character bound without truncation.
func (b *FuturesTrader) RouteClientID(id string) (string, error) {
	raw, e := hex.DecodeString(id)
	if e != nil || len(raw) != 16 {
		return "", errors.New("invalid execution client ID")
	}
	return futuresBrokerPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}
func (b *FuturesTrader) nativeClientID(o execution.Order) (string, error) {
	if o.VenueClientID == "" {
		return o.ClientID, nil
	} // Historical untagged orders: query their original ID only.
	expected, e := b.RouteClientID(o.ClientID)
	if e != nil || expected != o.VenueClientID {
		return "", errors.New("persisted Broker route mismatch")
	}
	return expected, nil
}

func (b *FuturesTrader) Venue() string { return "binance" }

// VenueAccountID reads Binance's native futures account code, independent of API credentials.
func (b *FuturesTrader) VenueAccountID(ctx context.Context) (string, error) {
	balances, err := b.client.NewGetBalanceService().Do(ctx)
	if err != nil {
		return "", binanceReadError("verify the Binance futures account identity", err)
	}
	id := ""
	for _, balance := range balances {
		if balance == nil || strings.TrimSpace(balance.AccountAlias) == "" {
			return "", errors.New("Binance did not return a complete futures account identity")
		}
		if id != "" && id != balance.AccountAlias {
			return "", errors.New("Binance returned conflicting futures account identities")
		}
		id = balance.AccountAlias
	}
	if id == "" {
		return "", errors.New("Binance did not return a futures account identity")
	}
	return id, nil
}

var _ execution.AccountIdentity = (*FuturesTrader)(nil)

// NewExecutionTrader initializes NOFX without changing exchange account settings.
func NewExecutionTrader(key, secret, userID string) *FuturesTrader {
	c := futures.NewClient(key, secret)
	c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	if result := hook.HookExec[hook.NewBinanceTraderResult](hook.NEW_BINANCE_TRADER, userID, c); result != nil && result.GetResult() != nil {
		c = result.GetResult()
	}
	return &FuturesTrader{client: c, cacheDuration: 15 * time.Second}
}

// NewAccountTrader calibrates signed reads without changing position mode,
// leverage, margin mode, or orders. Callers replace long-lived cached clients.
func NewAccountTrader(ctx context.Context, key, secret, userID string) (*FuturesTrader, error) {
	b := NewExecutionTrader(key, secret, userID)
	if err := b.calibrateClock(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *FuturesTrader) calibrateClock(ctx context.Context) error {
	start := time.Now().UnixMilli()
	serverTime, err := b.client.NewServerTimeService().Do(ctx)
	if err != nil {
		return binanceReadError("synchronize the Binance clock", err)
	}
	b.client.TimeOffset = (start+time.Now().UnixMilli())/2 - serverTime
	return nil
}

// Never expose signed request URLs or upstream credential-bearing errors.
func binanceReadError(action string, err error) error {
	var apiErr *common.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case -1021:
			return errors.New("Binance rejected the request timestamp. Refresh the connection to synchronize the server clock (code -1021).")
		case -2014, -2015:
			return fmt.Errorf("Binance rejected account access. Check the API key, server IP allowlist and Futures permissions (code %d).", apiErr.Code)
		case -1022:
			return errors.New("Binance rejected the API signature. Check the saved API secret (code -1022).")
		default:
			return fmt.Errorf("Could not %s (Binance code %d).", action, apiErr.Code)
		}
	}
	return fmt.Errorf("Could not %s. Check the NOFX server connection and try again.", action)
}
func ratString(s string) (*big.Rat, error) {
	n, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("invalid exchange decimal")
	}
	return n, nil
}
func sameNativeDecimal(a, b string) bool {
	x, e := ratString(a)
	if e != nil {
		return false
	}
	y, e := ratString(b)
	return e == nil && x.Cmp(y) == 0
}
func (b *FuturesTrader) AccountSnapshot(ctx context.Context, symbol string) (execution.Account, error) {
	var out execution.Account
	out.Position = "0"
	account, e := b.client.NewGetAccountService().Do(ctx)
	if e != nil {
		return out, binanceReadError("read the Binance account", e)
	}
	if !account.CanTrade || account.MultiAssetsMargin {
		return out, errors.New("Account cannot trade or uses unsupported multi-asset margin")
	}
	out.Equity = execution.Decimal(account.TotalMarginBalance)
	out.Available = execution.Decimal(account.AvailableBalance)
	qty := new(big.Rat)
	for _, p := range account.Positions {
		if p.Symbol == symbol {
			n, e := ratString(p.PositionAmt)
			if e != nil {
				return out, e
			}
			qty.Add(qty, n.Abs(n))
		}
	}
	out.Position = execution.Decimal(qty.FloatString(12))
	orders, e := b.client.NewListOpenOrdersService().Symbol(symbol).Do(ctx)
	if e != nil {
		return out, binanceReadError("read Binance open orders", e)
	}
	algos, e := b.client.NewListOpenAlgoOrdersService().Symbol(symbol).Do(ctx)
	if e != nil {
		return out, binanceReadError("read Binance protective orders", e)
	}
	out.OpenOrders = len(orders) + len(algos)
	fee, e := b.client.NewCommissionRateService().Symbol(symbol).Do(ctx)
	if e != nil {
		return out, binanceReadError("read Binance actual fees", e)
	}
	maker, e := ratString(fee.MakerCommissionRate)
	if e != nil {
		return out, e
	}
	taker, e := ratString(fee.TakerCommissionRate)
	if e != nil {
		return out, e
	}
	if maker.Cmp(taker) > 0 {
		taker = maker
	}
	out.FeeBPS = execution.Decimal(new(big.Rat).Mul(taker, big.NewRat(10000, 1)).FloatString(12))
	return out, nil
}
func (b *FuturesTrader) PrepareExecution(ctx context.Context, symbol string) error {
	return b.PrepareExecutionLeverage(ctx, symbol, 1)
}
func (b *FuturesTrader) PrepareExecutionLeverage(ctx context.Context, symbol string, leverage int) error {
	if leverage < 1 || leverage > 20 {
		return errors.New("unsupported planning leverage")
	}
	_, e := b.client.NewGetPositionModeService().Do(ctx)
	if e != nil {
		return errors.New("cannot verify Binance position mode")
	}
	result, e := b.client.NewChangeLeverageService().Symbol(symbol).Leverage(leverage).Do(ctx)
	if e != nil || result == nil || result.Leverage != leverage {
		return errors.New("cannot set the approved leverage; order blocked")
	}
	return nil
}
func executionPosition(side string, hedge bool) futures.PositionSideType {
	if !hedge {
		return futures.PositionSideTypeBoth
	}
	if side == "long" {
		return futures.PositionSideTypeLong
	}
	return futures.PositionSideTypeShort
}
func (b *FuturesTrader) SubmitOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	if o.Type != "" && (o.Type != "market" || (o.Role != "entry" && o.Role != "exit") || o.PostOnly) {
		return o, errors.New("unsupported execution order type")
	}
	nativeID, err := b.nativeClientID(o)
	if err != nil {
		return o, err
	}
	if o.VenueClientID == "" {
		return o, errors.New("Broker route must be persisted before submitting a new order")
	}

	if len(o.ClientID) != 32 || !execution.ValidOrderPrice(o) || !execution.Positive(o.Quantity) || (o.Side != "long" && o.Side != "short") {
		return o, errors.New("invalid locked order")
	}
	mode, e := b.client.NewGetPositionModeService().Do(ctx)
	if e != nil {
		return o, errors.New("position mode unavailable")
	}
	side := futures.SideTypeBuy
	if o.Side == "short" {
		side = futures.SideTypeSell
	}
	if o.Role != "entry" {
		if side == futures.SideTypeBuy {
			side = futures.SideTypeSell
		} else {
			side = futures.SideTypeBuy
		}
	}
	// A closing order travels the regular order path like an entry: it closes
	// the position side it names instead of opening the opposite one.
	if o.Role == "entry" || o.Role == "exit" {
		tif := futures.TimeInForceTypeGTC
		if o.PostOnly {
			tif = futures.TimeInForceTypeGTX
		}
		positionSide := executionPosition(o.Side, mode.DualSidePosition)
		if o.Role == "entry" && o.ReduceOnly && mode.DualSidePosition {
			if o.Side == "long" {
				positionSide = futures.PositionSideTypeShort
			} else {
				positionSide = futures.PositionSideTypeLong
			}
		}
		orderType := futures.OrderTypeLimit
		service := b.client.NewCreateOrderService().Symbol(o.Symbol).Side(side).PositionSide(positionSide).Quantity(string(o.Quantity)).NewClientOrderID(nativeID)
		if o.Type == "market" {
			orderType = futures.OrderTypeMarket
			if o.ReduceOnly {
				service.NewOrderResponseType(futures.NewOrderRespTypeRESULT)
			}
		} else {
			service.TimeInForce(tif).Price(string(o.Price))
		}
		service.Type(orderType)
		if o.ReduceOnly && !mode.DualSidePosition {
			service.ReduceOnly(true)
		}
		r, e := service.Do(ctx)
		if e != nil {
			return o, errors.New("Binance entry submission is unconfirmed; reconcile by client ID")
		}
		limitMismatch := o.Type != "market" && (!sameNativeDecimal(r.Price, string(o.Price)) || r.TimeInForce != tif)
		if r.OrderID <= 0 || r.ClientOrderID != nativeID || r.Symbol != o.Symbol || !sameNativeDecimal(r.OrigQuantity, string(o.Quantity)) || limitMismatch || r.Side != side || r.Type != orderType || r.PositionSide != positionSide || (o.ReduceOnly && !mode.DualSidePosition && !r.ReduceOnly) {
			return o, errors.New("invalid Binance order receipt")
		}
		o.OrderID = fmt.Sprint(r.OrderID)
		o.Status = string(r.Status)
		o.Filled = execution.Decimal(r.ExecutedQuantity)
		o.Average = execution.Decimal(r.AvgPrice)
		return o, nil
	}
	typ := futures.AlgoOrderTypeStopMarket
	if o.Role == "tp" {
		typ = futures.AlgoOrderTypeTakeProfitMarket
	} else if o.Role != "sl" {
		return o, errors.New("unsupported protective order")
	}
	service := b.client.NewCreateAlgoOrderService().Symbol(o.Symbol).Side(side).PositionSide(executionPosition(o.Side, mode.DualSidePosition)).Type(typ).Quantity(string(o.Quantity)).TriggerPrice(string(o.Price)).WorkingType(futures.WorkingTypeContractPrice).ClientAlgoId(nativeID)
	if !mode.DualSidePosition {
		service.ReduceOnly(true)
	}
	r, e := service.Do(ctx)
	if e != nil {
		return o, errors.New("Binance protective order submission is unconfirmed")
	}
	if r.AlgoId <= 0 || r.ClientAlgoId != nativeID || r.Symbol != o.Symbol || !sameNativeDecimal(r.Quantity, string(o.Quantity)) || !sameNativeDecimal(r.TriggerPrice, string(o.Price)) || r.Side != side || r.OrderType != typ || (!mode.DualSidePosition && !r.ReduceOnly) {
		return o, errors.New("invalid Binance protection receipt")
	}
	o.OrderID = fmt.Sprint(r.AlgoId)
	o.Status = "NEW"
	o.Filled = "0"
	return o, nil
}
func (b *FuturesTrader) LookupOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	nativeID, err := b.nativeClientID(o)
	if err != nil {
		return o, err
	}

	if o.Role != "entry" && o.Role != "exit" {
		r, e := b.client.NewGetAlgoOrderService().ClientAlgoID(nativeID).Do(ctx)
		if e != nil {
			return o, errors.New("Binance protective order lookup is unconfirmed")
		}
		if r.AlgoId <= 0 || r.ClientAlgoId != nativeID || r.Symbol != o.Symbol || !sameNativeDecimal(r.Quantity, string(o.Quantity)) || !sameNativeDecimal(r.TriggerPrice, string(o.Price)) {
			return o, errors.New("protection identity mismatch")
		}
		o.OrderID = fmt.Sprint(r.AlgoId)
		if r.ActualOrderId != "" && r.ActualOrderId != "0" {
			id, ok := new(big.Int).SetString(r.ActualOrderId, 10)
			if !ok || !id.IsInt64() {
				return o, errors.New("invalid triggered order ID")
			}
			fill, e := b.client.NewGetOrderService().Symbol(o.Symbol).OrderID(id.Int64()).Do(ctx)
			if e != nil {
				return o, errors.New("triggered fill not yet confirmed")
			}
			o.Status = string(fill.Status)
			o.Filled = execution.Decimal(fill.ExecutedQuantity)
			o.Average = execution.Decimal(fill.AvgPrice)
			return o, nil
		}
		switch r.AlgoStatus {
		case "NEW":
			o.Status = "NEW"
		case "CANCELED":
			o.Status = "CANCELED"
		case "REJECTED":
			o.Status = "REJECTED"
		case "EXPIRED":
			o.Status = "EXPIRED"
		default:
			return o, errors.New("unknown Binance algo state")
		}
		return o, nil
	}
	r, e := b.client.NewGetOrderService().Symbol(o.Symbol).OrigClientOrderID(nativeID).Do(ctx)
	if e != nil {
		return o, errors.New("Binance order lookup is unconfirmed; missing does not mean filled")
	}
	expected := futures.SideTypeBuy
	if o.Side == "short" {
		expected = futures.SideTypeSell
	}
	if o.Role != "entry" { // closing orders trade against the position they reduce
		if expected == futures.SideTypeBuy {
			expected = futures.SideTypeSell
		} else {
			expected = futures.SideTypeBuy
		}
	}
	priceMismatch := o.Type != "market" && !sameNativeDecimal(r.Price, string(o.Price))
	marketMismatch := o.Type == "market" && (r.Type != futures.OrderTypeMarket || r.Side != expected)
	if r.OrderID <= 0 || r.ClientOrderID != nativeID || r.Symbol != o.Symbol || !sameNativeDecimal(r.OrigQuantity, string(o.Quantity)) || priceMismatch || marketMismatch {
		return o, errors.New("order identity mismatch")
	}
	o.OrderID = fmt.Sprint(r.OrderID)
	o.Status = string(r.Status)
	o.Filled = execution.Decimal(r.ExecutedQuantity)
	o.Average = execution.Decimal(r.AvgPrice)
	return o, nil
}
func (b *FuturesTrader) CancelExecutionOrder(ctx context.Context, o execution.Order) error {
	nativeID, err := b.nativeClientID(o)
	if err != nil {
		return err
	}

	if o.Role == "entry" || o.Role == "exit" {
		r, e := b.client.NewCancelOrderService().Symbol(o.Symbol).OrigClientOrderID(nativeID).Do(ctx)
		if e != nil || r.ClientOrderID != nativeID {
			return errors.New("Binance entry cancellation is unconfirmed")
		}
		return nil
	}
	r, e := b.client.NewCancelAlgoOrderService().ClientAlgoID(nativeID).Do(ctx)
	if e != nil || r.ClientAlgoId != nativeID {
		return errors.New("Binance protective order cancellation is unconfirmed")
	}
	return nil
}

var _ execution.Trader = (*FuturesTrader)(nil)

// managedEndpoint is a test seam only: no production caller can supply a URL,
// so a sandbox trader cannot be redirected at production or vice versa.
type managedEndpoint struct {
	baseURL string
	client  *http.Client
	// streamURL overrides the user-data socket. Only a test supplies it: no
	// production caller can point a sandbox account at a live stream.
	streamURL string
}

// managedBinanceEndpoint pins each environment to one host. Binance USDⓈ-M hosts
// are the pinned SDK constants (v2.8.9, checked 2026-11-06).
func managedBinanceEndpoint(env execution.Environment) (string, error) {
	switch env {
	case execution.EnvironmentSandbox:
		return futures.BaseApiTestnetUrl, nil
	case execution.EnvironmentLive:
		return futures.BaseApiMainUrl, nil
	}
	return "", errors.New("managed Binance environment must be sandbox or live")
}

// NewManagedAccountTrader builds an account reader for unattended execution.
// It never changes account settings, leverage or orders.
func NewManagedAccountTrader(ctx context.Context, key, secret, userID string, env execution.Environment) (*FuturesTrader, error) {
	return newManagedAccountTrader(ctx, key, secret, userID, env, nil)
}

// ManagedEndpoint is the exported form of the test seam below: a certification
// fixture points a managed account at a local venue. No production caller
// supplies one, so a sandbox account cannot be redirected at production or
// vice versa. Nil means the pinned production endpoint.
type ManagedEndpoint struct {
	BaseURL   string
	Client    *http.Client
	StreamURL string
}

// NewManagedAccountTraderWithEndpoint builds a managed account against a
// supplied endpoint. It exists for certification fixtures only: production
// callers never supply one. A live account must never be pinned to a
// caller-supplied host, so live+seam is rejected instead of trusting the URL.
func NewManagedAccountTraderWithEndpoint(ctx context.Context, key, secret, userID string, env execution.Environment, seam *ManagedEndpoint) (*FuturesTrader, error) {
	if env == execution.EnvironmentLive && seam != nil {
		return nil, errors.New("a live managed trader cannot be pinned to a custom endpoint")
	}
	var internal *managedEndpoint
	if seam != nil {
		internal = &managedEndpoint{baseURL: seam.BaseURL, client: seam.Client, streamURL: seam.StreamURL}
	}
	return newManagedAccountTrader(ctx, key, secret, userID, env, internal)
}

func newManagedAccountTrader(ctx context.Context, key, secret, userID string, env execution.Environment, seam *managedEndpoint) (*FuturesTrader, error) {
	endpoint, err := managedBinanceEndpoint(env)
	if err != nil {
		return nil, err
	}
	b := NewExecutionTrader(key, secret, userID)
	b.managedEnvironment = env
	b.client.SetApiEndpoint(endpoint)
	b.managedStreamURL, err = binanceStreamEndpoint(env)
	if err != nil {
		return nil, err
	}
	if seam != nil {
		b.client.BaseURL = seam.baseURL
		b.client.HTTPClient = seam.client
		if seam.streamURL != "" {
			b.managedStreamURL = seam.streamURL
		}
	}
	if err := b.calibrateClock(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *FuturesTrader) ManagedCapabilities() execution.Capabilities {
	return execution.Capabilities{PrivateEvents: true}
}

func (b *FuturesTrader) AmendStop(context.Context, execution.Order, execution.Decimal) (execution.Order, error) {
	return execution.Order{}, execution.ErrUnsupported
}

func (b *FuturesTrader) Events(ctx context.Context, fn func(execution.AccountEvent) error) error {
	return b.ManagedEvents(ctx, fn)
}

func binanceSideName(side futures.SideType) string {
	if side == futures.SideTypeBuy {
		return "short" // one-way BUY reduces a short position
	}
	return "long"
}

func binanceManagedSide(positionSide futures.PositionSideType, side futures.SideType) string {
	switch positionSide {
	case futures.PositionSideTypeLong:
		return "long"
	case futures.PositionSideTypeShort:
		return "short"
	}
	return binanceSideName(side)
}

func binanceRiskFor(risks []*futures.PositionRisk, symbol string, side futures.PositionSideType) *futures.PositionRisk {
	for _, r := range risks {
		if r.Symbol != symbol {
			continue
		}
		if side == futures.PositionSideTypeBoth || r.PositionSide == string(side) {
			return r
		}
	}
	return nil
}

// ManagedSnapshot reads the venue state used to verify unattended positions.
// Any unreadable field fails the whole snapshot instead of reporting empty state.
func (b *FuturesTrader) ManagedSnapshot(ctx context.Context) (execution.ManagedAccount, error) {
	out := execution.ManagedAccount{
		Venue:       b.Venue(),
		Environment: b.managedEnvironment,
		Positions:   []execution.ManagedPosition{},
		Orders:      []execution.Order{},
		ObservedAt:  time.Now().UTC(),
	}
	if !b.managedEnvironment.Valid() {
		return out, errors.New("managed Binance environment is not set")
	}
	mode, err := b.client.NewGetPositionModeService().Do(ctx)
	if err != nil {
		return out, binanceReadError("verify the Binance position mode", err)
	}
	out.PositionMode = "one_way"
	if mode.DualSidePosition {
		out.PositionMode = "hedge"
	}
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return out, binanceReadError("read the Binance account", err)
	}
	if out.Equity, err = execution.BalanceDecimal(account.TotalMarginBalance); err != nil {
		return out, fmt.Errorf("Binance account equity is unavailable: %w", err)
	}
	if out.Available, err = execution.BalanceDecimal(account.AvailableBalance); err != nil {
		return out, fmt.Errorf("Binance account available balance is unavailable: %w", err)
	}
	if out.MarginUsed, err = execution.BalanceDecimal(account.TotalPositionInitialMargin); err != nil {
		return out, fmt.Errorf("Binance account margin usage is unavailable: %w", err)
	}
	risks, err := b.client.NewGetPositionRiskService().Do(ctx)
	if err != nil {
		return out, binanceReadError("read Binance position risk", err)
	}
	open, err := b.client.NewListOpenOrdersService().Do(ctx)
	if err != nil {
		return out, binanceReadError("read Binance open orders", err)
	}
	algos, err := b.client.NewListOpenAlgoOrdersService().Do(ctx)
	if err != nil {
		return out, binanceReadError("read Binance protective orders", err)
	}

	marginModes := map[string]bool{}
	for _, p := range account.Positions {
		amt, err := execution.Number(p.PositionAmt)
		if err != nil {
			return out, fmt.Errorf("Binance position quantity is invalid: %w", err)
		}
		if amt.Sign() == 0 {
			continue
		}
		risk := binanceRiskFor(risks, p.Symbol, p.PositionSide)
		if risk == nil {
			return out, fmt.Errorf("Binance position %s has no risk data", p.Symbol)
		}
		margin := risk.IsolatedMargin
		if margin == "" {
			margin = p.InitialMargin
		}
		if margin == "" {
			margin = p.IsolatedWallet
		}
		side := ""
		switch risk.PositionSide {
		case string(futures.PositionSideTypeLong):
			side = "long"
		case string(futures.PositionSideTypeShort):
			side = "short"
		case string(futures.PositionSideTypeBoth):
			side = "long"
			if strings.HasPrefix(risk.PositionAmt, "-") {
				side = "short"
			}
		default:
			return out, fmt.Errorf("unknown Binance position side %q", risk.PositionSide)
		}
		out.Positions = append(out.Positions, execution.ManagedPosition{
			Symbol:      p.Symbol,
			Side:        side,
			Quantity:    execution.Decimal(strings.TrimPrefix(risk.PositionAmt, "-")),
			Entry:       execution.Decimal(risk.EntryPrice),
			Mark:        execution.Decimal(risk.MarkPrice),
			Liquidation: execution.Decimal(risk.LiquidationPrice),
			Margin:      execution.Decimal(margin),
		})
		marginModes[risk.MarginType] = true
	}
	if len(marginModes) == 1 {
		for mode := range marginModes {
			out.MarginMode = mode
		}
	}

	for _, o := range open {
		oType := "limit"
		if o.Type == futures.OrderTypeMarket {
			oType = "market"
		}
		out.Orders = append(out.Orders, execution.Order{
			Type:          oType,
			ReduceOnly:    o.ReduceOnly,
			PostOnly:      o.TimeInForce == futures.TimeInForceTypeGTX,
			VenueClientID: o.ClientOrderID,
			OrderID:       fmt.Sprint(o.OrderID),
			Role:          "entry",
			Symbol:        o.Symbol,
			Side:          binanceManagedSide(o.PositionSide, o.Side),
			Price:         execution.Decimal(o.Price),
			Quantity:      execution.Decimal(o.OrigQuantity),
			Status:        string(o.Status),
			Filled:        execution.Decimal(o.ExecutedQuantity),
			Average:       execution.Decimal(o.AvgPrice),
		})
	}
	for _, a := range algos {
		role := "sl"
		switch a.OrderType {
		case futures.AlgoOrderTypeStopMarket, futures.AlgoOrderTypeStop:
		case futures.AlgoOrderTypeTakeProfitMarket, futures.AlgoOrderTypeTakeProfit:
			role = "tp"
		default:
			continue
		}
		out.Orders = append(out.Orders, execution.Order{
			Type:          "market",
			ReduceOnly:    a.ReduceOnly,
			VenueClientID: a.ClientAlgoId,
			OrderID:       fmt.Sprint(a.AlgoId),
			Role:          role,
			Symbol:        a.Symbol,
			Side:          binanceManagedSide(a.PositionSide, a.Side),
			Price:         execution.Decimal(a.TriggerPrice),
			Quantity:      execution.Decimal(a.Quantity),
			Status:        a.AlgoStatus,
		})
	}
	for i, p := range out.Positions {
		var stop execution.Decimal
		for _, o := range out.Orders {
			if o.Role != "sl" || o.Symbol != p.Symbol || o.Side != p.Side || !execution.Equal(string(o.Quantity), string(p.Quantity)) {
				continue
			}
			if stop != "" && !execution.Equal(string(stop), string(o.Price)) {
				stop = "" // ambiguous protection is not confirmed
				break
			}
			stop = o.Price
		}
		out.Positions[i].StopPrice = stop
	}
	return out, nil
}

// VerifyManagedSettings only reads: it never switches mode or sets leverage.
// A flat symbol has no risk row, so margin mode and leverage are unverifiable.
func (b *FuturesTrader) VerifyManagedSettings(ctx context.Context, symbol string, leverage int) error {
	if !b.managedEnvironment.Valid() {
		return errors.New("managed Binance environment is not set")
	}
	if symbol == "" || leverage < 1 || leverage > 20 {
		return errors.New("invalid managed Binance settings request")
	}
	mode, err := b.client.NewGetPositionModeService().Do(ctx)
	if err != nil {
		return binanceReadError("verify the Binance position mode", err)
	}
	if mode.DualSidePosition {
		return errors.New("Binance account is in hedge mode; unattended execution requires one-way mode")
	}
	risks, err := b.client.NewGetPositionRiskService().Symbol(symbol).Do(ctx)
	if err != nil {
		return binanceReadError("read Binance position risk", err)
	}
	if len(risks) == 0 {
		return fmt.Errorf("cannot verify Binance margin mode and leverage for %s", symbol)
	}
	for _, r := range risks {
		if r.MarginType != "isolated" {
			return fmt.Errorf("Binance %s is not in isolated margin mode", symbol)
		}
		lever, err := strconv.Atoi(r.Leverage)
		if err != nil || lever != leverage {
			return fmt.Errorf("Binance %s leverage does not match the approved value", symbol)
		}
	}
	return nil
}

var _ execution.ManagedTrader = (*FuturesTrader)(nil)
