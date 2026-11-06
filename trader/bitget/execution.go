package bitget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"nofx/trader/execution"
	"strconv"
	"strings"
)

var _ execution.Trader = (*BitgetTrader)(nil)

func (t *BitgetTrader) Venue() string { return "bitget" }
func (t *BitgetTrader) RouteClientID(id string) (string, error) {
	return execution.CanonicalClientID(id)
}
func (t *BitgetTrader) readExecution(ctx context.Context, path string, out any) error {
	b, e := t.doRequestContext(ctx, "GET", path, nil)
	if e != nil {
		return e
	}
	if string(b) == "null" {
		return errors.New("missing Bitget account data")
	}
	return json.Unmarshal(b, out)
}
func mixQuery(symbol string) string {
	return "?productType=USDT-FUTURES&symbol=" + url.QueryEscape(symbol)
}

type executionAccount struct{ MarginCoin, AccountEquity, Available, CrossedMaxAvailable, IsolatedMaxAvailable, MarginMode, PosMode, AssetMode string }

func (t *BitgetTrader) executionAccount(ctx context.Context, symbol string) (executionAccount, error) {
	var a executionAccount
	e := t.readExecution(ctx, "/api/v2/mix/account/account"+mixQuery(symbol)+"&marginCoin=USDT", &a)
	if e != nil {
		return a, e
	}
	if a.MarginCoin != "USDT" || (a.PosMode != "one_way_mode" && a.PosMode != "hedge_mode") || (a.MarginMode != "crossed" && a.MarginMode != "isolated") {
		return a, errors.New("unknown Bitget futures account mode")
	}
	if a.AssetMode != "" && a.AssetMode != "single" {
		return a, errors.New("Frontier requires Bitget single-asset USDT futures margin")
	}
	return a, nil
}

type exactContract struct{ Symbol, BaseCoin, QuoteCoin, SymbolStatus, SymbolType, MinTradeNum, MinTradeUSDT, SizeMultiplier, PricePlace, PriceEndStep, MaxMarketOrderQty, MaxOrderQty, TakerFeeRate string }

func (t *BitgetTrader) executionContract(ctx context.Context, symbol string) (exactContract, error) {
	var rows []exactContract
	e := t.readExecution(ctx, bitgetContractsPath+mixQuery(symbol), &rows)
	if e != nil {
		return exactContract{}, e
	}
	if len(rows) != 1 {
		return exactContract{}, errors.New("Bitget contract not found")
	}
	r := rows[0]
	if r.Symbol != symbol || r.QuoteCoin != "USDT" || r.SymbolStatus != "normal" || r.SymbolType != "perpetual" {
		return r, errors.New("Frontier requires active Bitget USDT perpetual futures")
	}
	return r, nil
}

type executionPosition struct{ Symbol, HoldSide, Total, Available, MarginMode string }

func (t *BitgetTrader) executionPositions(ctx context.Context) ([]executionPosition, error) {
	var rows []executionPosition
	e := t.readExecution(ctx, bitgetPositionPath+"?productType=USDT-FUTURES&marginCoin=USDT", &rows)
	return rows, e
}

type executionReceipt struct{ OrderId, ExecuteOrderId, ClientOid, Symbol, Size, BaseVolume, PriceAvg, Price, State, Status, Side, PosSide, PosMode, TradeSide, ReduceOnly, OrderType, Force, PlanType, PlanStatus, TriggerPrice, TriggerType, ExecutePrice string }

func (t *BitgetTrader) pendingExecution(ctx context.Context, symbol string) ([]executionReceipt, error) {
	var result []executionReceipt
	for _, kind := range []string{"", "normal_plan", "track_plan", "profit_loss"} {
		path := bitgetPendingPath + mixQuery(symbol) + "&limit=100"
		if kind != "" {
			path = "/api/v2/mix/order/orders-plan-pending" + mixQuery(symbol) + "&planType=" + kind + "&limit=100"
		}
		var response struct{ EntrustedList []executionReceipt }
		if e := t.readExecution(ctx, path, &response); e != nil {
			return nil, e
		}
		result = append(result, response.EntrustedList...)
	}
	return result, nil
}
func (t *BitgetTrader) AccountSnapshot(ctx context.Context, symbol string) (execution.Account, error) {
	var out execution.Account
	if _, e := t.executionContract(ctx, symbol); e != nil {
		return out, e
	}
	a, e := t.executionAccount(ctx, symbol)
	if e != nil {
		return out, e
	}
	out.Equity = execution.Decimal(a.AccountEquity)
	available := a.CrossedMaxAvailable
	if a.MarginMode == "isolated" {
		available = a.IsolatedMaxAvailable
	}
	if available == "" {
		available = a.Available
	}
	out.Available = execution.Decimal(available)
	if out.Equity, e = execution.BalanceDecimal(string(out.Equity)); e != nil {
		return out, fmt.Errorf("invalid Bitget account equity: %w", e)
	}
	if out.Available, e = execution.BalanceDecimal(string(out.Available)); e != nil {
		return out, fmt.Errorf("invalid Bitget available margin: %w", e)
	}
	var fee struct{ TakerFeeRate string }
	if e = t.readExecution(ctx, "/api/v2/common/trade-rate?businessType=mix&symbol="+url.QueryEscape(symbol), &fee); e != nil {
		return out, e
	}
	f, e := execution.Number(fee.TakerFeeRate)
	if e != nil {
		return out, e
	}
	out.FeeBPS, e = execution.ExactDecimal(f.Mul(f.Abs(f), big.NewRat(10000, 1)))
	if e != nil {
		return out, e
	}
	positions, e := t.executionPositions(ctx)
	if e != nil {
		return out, e
	}
	gross := new(big.Rat)
	for _, p := range positions {
		if p.Symbol != symbol {
			continue
		}
		v, e := execution.Number(p.Total)
		if e != nil {
			return out, e
		}
		gross.Add(gross, v.Abs(v))
	}
	out.Position, e = execution.ExactDecimal(gross)
	if e != nil {
		return out, e
	}
	orders, e := t.pendingExecution(ctx, symbol)
	if e != nil {
		return out, e
	}
	out.OpenOrders = len(orders)
	return out, nil
}
func (t *BitgetTrader) PrepareExecution(ctx context.Context, symbol string) error {
	return t.PrepareExecutionLeverage(ctx, symbol, 1)
}
func (t *BitgetTrader) PrepareExecutionLeverage(ctx context.Context, symbol string, leverage int) error {
	if leverage < 1 || leverage > 125 {
		return errors.New("invalid Bitget leverage")
	}
	a, e := t.AccountSnapshot(ctx, symbol)
	if e != nil {
		return e
	}
	if !execution.Equal(string(a.Position), "0") || a.OpenOrders != 0 {
		return errors.New("Bitget market already has a position or pending orders")
	}
	config, e := t.executionAccount(ctx, symbol)
	if e != nil {
		return e
	}
	sides := []string{""}
	if config.MarginMode == "isolated" && config.PosMode == "hedge_mode" {
		sides = []string{"long", "short"}
	}
	for _, side := range sides {
		body := map[string]string{"symbol": symbol, "productType": "USDT-FUTURES", "marginCoin": "USDT", "leverage": strconv.Itoa(leverage)}
		if side != "" {
			body["holdSide"] = side
		}
		raw, e := t.doRequestContext(ctx, "POST", bitgetLeveragePath, body)
		if e != nil {
			return e
		}
		var r struct{ CrossMarginLeverage, LongLeverage, ShortLeverage string }
		if json.Unmarshal(raw, &r) != nil {
			return errors.New("Bitget leverage confirmation missing")
		}
		value := r.CrossMarginLeverage
		if config.MarginMode == "isolated" {
			value = r.LongLeverage
			if side == "short" {
				value = r.ShortLeverage
			}
		}
		if !execution.Equal(value, strconv.Itoa(leverage)) {
			return errors.New("Bitget did not confirm approved leverage")
		}
	}
	return nil
}
func bitgetDirection(o execution.Order, mode string) (side, held, trade string) {
	held = o.Side
	side = "buy"
	if o.Side == "short" {
		side = "sell"
	}
	trade = "open"
	if o.ReduceOnly {
		held = "short"
		if o.Side == "short" {
			held = "long"
		}
	}
	if o.Role != "entry" || o.ReduceOnly {
		trade = "close"
		side = "sell"
		if held == "short" {
			side = "buy"
		}
		if mode == "hedge_mode" {
			side = "buy"
			if held == "short" {
				side = "sell"
			}
		}
	}
	return
}
func (t *BitgetTrader) SubmitOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	if e := execution.ValidateIntent(o); e != nil {
		return o, e
	}
	contract, e := t.executionContract(ctx, o.Symbol)
	if e != nil {
		return o, e
	}
	if !execution.Aligned(string(o.Quantity), contract.SizeMultiplier) {
		return o, errors.New("quantity is not an exact Bitget increment")
	}
	q, _ := execution.Number(string(o.Quantity))
	min, e := execution.Number(contract.MinTradeNum)
	if e != nil || q.Cmp(min) < 0 {
		return o, errors.New("quantity below Bitget minimum")
	}
	max := contract.MaxOrderQty
	if o.Type == "market" || o.Role != "entry" {
		max = contract.MaxMarketOrderQty
	}
	if max != "" {
		m, e := execution.Number(max)
		if e != nil || q.Cmp(m) > 0 {
			return o, errors.New("quantity exceeds Bitget order limit")
		}
	}
	places, e := strconv.Atoi(contract.PricePlace)
	if e != nil || places < 0 || places > 12 {
		return o, errors.New("invalid Bitget price precision")
	}
	tick, e := execution.Multiply(contract.PriceEndStep, "0."+strings.Repeat("0", maxInt(0, places-1))+"1")
	if places == 0 {
		tick = execution.Decimal(contract.PriceEndStep)
	}
	if e != nil {
		return o, e
	}
	if (o.Type != "market" || o.Role != "entry") && !execution.Aligned(string(o.Price), string(tick)) {
		return o, errors.New("price is not an exact Bitget tick")
	}
	a, e := t.executionAccount(ctx, o.Symbol)
	if e != nil {
		return o, e
	}
	side, held, trade := bitgetDirection(o, a.PosMode)
	margin := a.MarginMode
	if trade == "close" {
		positions, e := t.executionPositions(ctx)
		if e != nil {
			return o, e
		}
		amount := new(big.Rat)
		found := false
		for _, p := range positions {
			if p.Symbol != o.Symbol || p.HoldSide != held {
				continue
			}
			v, e := execution.Number(p.Total)
			if e != nil {
				return o, e
			}
			if v.Sign() <= 0 {
				continue
			}
			if p.MarginMode != "crossed" && p.MarginMode != "isolated" {
				return o, errors.New("unknown Bitget position margin mode")
			}
			if found && margin != p.MarginMode {
				return o, errors.New("multiple Bitget position margin modes")
			}
			margin = p.MarginMode
			found = true
			amount.Add(amount, v)
		}
		if !found || q.Cmp(amount) > 0 {
			return o, errors.New("Bitget position changed; refresh before closing or protecting")
		}
	}
	body := map[string]any{"symbol": o.Symbol, "productType": "USDT-FUTURES", "marginCoin": "USDT", "size": string(o.Quantity), "clientOid": o.ClientID}
	path := bitgetOrderPath
	if o.Role == "entry" {
		body["marginMode"] = margin
		body["side"] = side
		body["orderType"] = "limit"
		body["price"] = string(o.Price)
		body["force"] = "gtc"
		if a.PosMode == "hedge_mode" {
			body["tradeSide"] = trade
		} else if o.ReduceOnly {
			body["reduceOnly"] = "YES"
		}
		if o.Type == "market" {
			body["orderType"] = "market"
			delete(body, "price")
			delete(body, "force")
		} else if o.PostOnly {
			body["force"] = "post_only"
		}
	} else {
		path = "/api/v2/mix/order/place-tpsl-order"
		body["planType"] = "loss_plan"
		if o.Role == "tp" {
			body["planType"] = "profit_plan"
		}
		body["triggerPrice"] = string(o.Price)
		body["triggerType"] = "mark_price"
		body["executePrice"] = "0"
		body["holdSide"] = held
		if a.PosMode == "one_way_mode" {
			body["holdSide"] = "buy"
			if held == "short" {
				body["holdSide"] = "sell"
			}
		}
	}
	raw, e := t.doRequestContext(ctx, "POST", path, body)
	if e != nil {
		return o, e
	}
	var r struct{ OrderId, ClientOid string }
	if json.Unmarshal(raw, &r) != nil || r.OrderId == "" || r.ClientOid != o.ClientID {
		return o, errors.New("Bitget order identity not confirmed")
	}
	o.OrderID = r.OrderId
	o.Status = "NEW"
	o.Filled = "0"
	return o, nil
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (t *BitgetTrader) LookupOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	if e := execution.ValidateIntent(o); e != nil {
		return o, e
	}
	var r executionReceipt
	query := mixQuery(o.Symbol) + "&clientOid=" + url.QueryEscape(o.ClientID)
	if o.OrderID != "" {
		query = mixQuery(o.Symbol) + "&orderId=" + url.QueryEscape(o.OrderID)
	}
	if o.Role == "entry" {
		if e := t.readExecution(ctx, "/api/v2/mix/order/detail"+query, &r); e != nil {
			return o, e
		}
	} else {
		found := false
		for _, path := range []string{"/api/v2/mix/order/orders-plan-pending", "/api/v2/mix/order/orders-plan-history"} {
			var data struct{ EntrustedList []executionReceipt }
			if e := t.readExecution(ctx, path+query+"&planType=profit_loss", &data); e != nil {
				return o, e
			}
			for _, row := range data.EntrustedList {
				if bitgetReceiptIdentity(o, row) {
					if found {
						return o, errors.New("duplicate Bitget client ID")
					}
					r = row
					found = true
				}
			}
			if found {
				break
			}
		}
		if !found {
			return o, errors.New("Bitget protection not found; submission remains unconfirmed")
		}
	}
	if !bitgetReceiptIdentity(o, r) || !strings.EqualFold(r.Symbol, o.Symbol) || !execution.Equal(r.Size, string(o.Quantity)) {
		return o, errors.New("Bitget receipt identity or quantity mismatch")
	}
	if r.PosMode != "one_way_mode" && r.PosMode != "hedge_mode" {
		return o, errors.New("Bitget receipt position mode missing")
	}
	side, held, trade := bitgetDirection(o, r.PosMode)
	if (o.Role == "entry" && (r.Side != side || (r.PosMode == "hedge_mode" && r.PosSide != held))) || (o.Role != "entry" && !bitgetProtectionDirection(r, held)) {
		return o, errors.New("Bitget receipt direction mismatch")
	}
	o.OrderID = r.OrderId
	o.Filled = "0"
	if o.Role == "entry" {
		typ := "limit"
		if o.Type == "market" {
			typ = "market"
		}
		if r.OrderType != typ || typ != "market" && !execution.Equal(r.Price, string(o.Price)) || o.PostOnly && r.Force != "post_only" {
			return o, errors.New("Bitget receipt order terms mismatch")
		}
		if r.PosMode == "hedge_mode" && r.TradeSide != trade {
			return o, errors.New("Bitget open/close direction mismatch")
		}
		if o.ReduceOnly && r.PosMode == "one_way_mode" && !strings.EqualFold(r.ReduceOnly, "yes") {
			return o, errors.New("Bitget close was not reduce-only")
		}
		o.Status = map[string]string{"live": "NEW", "new": "NEW", "partially_filled": "PARTIALLY_FILLED", "filled": "FILLED", "cancelled": "CANCELED", "canceled": "CANCELED"}[r.State]
		n, e := execution.Number(r.BaseVolume)
		if e != nil || n.Sign() < 0 {
			return o, errors.New("invalid Bitget filled quantity")
		}
		o.Filled = execution.Decimal(r.BaseVolume)
		if execution.Positive(o.Filled) {
			if !execution.Positive(execution.Decimal(r.PriceAvg)) {
				return o, errors.New("Bitget fill price missing")
			}
			o.Average = execution.Decimal(r.PriceAvg)
		}
	} else {
		kind := "loss_plan"
		if o.Role == "tp" {
			kind = "profit_plan"
		}
		if r.PlanType != kind || !execution.Equal(r.TriggerPrice, string(o.Price)) || r.TriggerType != "mark_price" || (r.ExecutePrice != "" && !execution.Equal(r.ExecutePrice, "0")) {
			return o, errors.New("Bitget protection terms mismatch")
		}
		o.Status = map[string]string{"not_trigger": "NEW", "executing": "TRIGGERED", "executed": "TRIGGERED", "fail_execute": "REJECTED", "cancelled": "CANCELED"}[r.PlanStatus]
		if r.PlanStatus == "executed" && r.ExecuteOrderId != "" {
			return t.lookupProtectionExecution(ctx, o, r)
		}
	}
	if o.Status == "" {
		return o, errors.New("unrecognized Bitget order state")
	}
	return o, nil
}

// A known exchange ID remains authoritative when historical records omit the
// original client ID. Conflicting IDs must never identify a different order.
func bitgetReceiptIdentity(o execution.Order, r executionReceipt) bool {
	if r.OrderId == "" || (r.ClientOid != "" && r.ClientOid != o.ClientID) {
		return false
	}
	if o.OrderID != "" {
		return r.OrderId == o.OrderID
	}
	return r.ClientOid == o.ClientID
}

func bitgetProtectionDirection(r executionReceipt, held string) bool {
	if r.PosMode == "hedge_mode" {
		return r.PosSide == held
	}
	if r.PosMode != "one_way_mode" {
		return false
	}
	// One-way TP/SL responses report a net position, so the closing order side
	// identifies which position is protected. Net alone is not direction evidence.
	switch r.PosSide {
	case "long", "short":
		return r.PosSide == held
	case "net":
		return (held == "long" && r.Side == "sell") || (held == "short" && r.Side == "buy")
	default:
		return false
	}
}

// A triggered plan is not proof of a fill. Read its linked execution order and
// keep the original plan ID so durable protection identity remains unchanged.
func (t *BitgetTrader) lookupProtectionExecution(ctx context.Context, o execution.Order, plan executionReceipt) (execution.Order, error) {
	var fill executionReceipt
	if err := t.readExecution(ctx, "/api/v2/mix/order/detail"+mixQuery(o.Symbol)+"&orderId="+url.QueryEscape(plan.ExecuteOrderId), &fill); err != nil {
		return o, err
	}
	if fill.OrderId != plan.ExecuteOrderId || !strings.EqualFold(fill.Symbol, o.Symbol) || !execution.Equal(fill.Size, string(o.Quantity)) || fill.PosMode != plan.PosMode || fill.OrderType != "market" || !bitgetProtectionDirection(fill, o.Side) {
		return o, errors.New("Bitget protection execution identity or terms mismatch")
	}
	side, _, trade := bitgetDirection(o, fill.PosMode)
	if fill.Side != side || (fill.PosMode == "hedge_mode" && fill.TradeSide != trade) || (fill.PosMode == "one_way_mode" && !strings.EqualFold(fill.ReduceOnly, "yes")) {
		return o, errors.New("Bitget protection execution was not a closing order")
	}
	state := fill.State
	if state == "" {
		state = fill.Status
	}
	status := map[string]string{"live": "TRIGGERED", "new": "TRIGGERED", "partially_filled": "PARTIALLY_FILLED", "filled": "FILLED", "cancelled": "CANCELED", "canceled": "CANCELED"}[state]
	quantity, err := execution.Number(fill.BaseVolume)
	total, _ := execution.Number(string(o.Quantity))
	if err != nil || quantity.Sign() < 0 || quantity.Cmp(total) > 0 || status == "" || (status == "FILLED" && quantity.Cmp(total) != 0) || (status == "PARTIALLY_FILLED" && (quantity.Sign() == 0 || quantity.Cmp(total) == 0)) || (status == "TRIGGERED" && quantity.Sign() != 0) {
		return o, errors.New("Bitget protection execution fill is unconfirmed")
	}
	o.Filled = execution.Decimal(fill.BaseVolume)
	o.Status = status
	if quantity.Sign() > 0 {
		if !execution.Positive(execution.Decimal(fill.PriceAvg)) {
			return o, errors.New("Bitget protection execution fill price missing")
		}
		o.Average = execution.Decimal(fill.PriceAvg)
	}
	return o, nil
}
func (t *BitgetTrader) CancelExecutionOrder(ctx context.Context, o execution.Order) error {
	if e := execution.ValidateIntent(o); e != nil {
		return e
	}
	if o.OrderID == "" {
		return errors.New("reconcile Bitget order before cancellation")
	}
	body := map[string]any{"symbol": o.Symbol, "productType": "USDT-FUTURES", "marginCoin": "USDT"}
	path := bitgetCancelOrderPath
	if o.Role == "entry" {
		body["orderId"] = o.OrderID
		body["clientOid"] = o.ClientID
	} else {
		path = "/api/v2/mix/order/cancel-plan-order"
		body["orderIdList"] = []map[string]string{{"orderId": o.OrderID, "clientOid": o.ClientID}}
	}
	raw, e := t.doRequestContext(ctx, "POST", path, body)
	if e != nil {
		return e
	}
	var r struct {
		OrderId, ClientOid       string
		SuccessList, FailureList []struct{ OrderId, ClientOid string }
	}
	if json.Unmarshal(raw, &r) != nil {
		return errors.New("invalid Bitget cancellation response")
	}
	if o.Role == "entry" {
		if r.OrderId == o.OrderID && r.ClientOid == o.ClientID {
			return nil
		}
	} else if len(r.FailureList) == 0 && len(r.SuccessList) == 1 && r.SuccessList[0].OrderId == o.OrderID {
		return nil
	}
	return errors.New("Bitget cancellation unconfirmed; reconcile order")
}
