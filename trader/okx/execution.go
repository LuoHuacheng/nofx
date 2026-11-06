package okx

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

var _ execution.Trader = (*OKXTrader)(nil)

func (t *OKXTrader) Venue() string                           { return "okx" }
func (t *OKXTrader) RouteClientID(id string) (string, error) { return execution.CanonicalClientID(id) }
func (t *OKXTrader) readExecution(ctx context.Context, path string, out any) error {
	b, e := t.doRequestContext(ctx, "GET", path, nil)
	if e != nil {
		return e
	}
	if string(b) == "null" {
		return errors.New("missing OKX data")
	}
	return json.Unmarshal(b, out)
}

type exactInstrument struct{ InstId, InstFamily, GroupId, InstType, State, CtType, SettleCcy, CtValCcy, CtVal, CtMult, LotSz, MinSz, TickSz, MaxMktSz, MaxLmtSz string }

func (t *OKXTrader) executionInstrument(ctx context.Context, symbol string) (exactInstrument, error) {
	var rows []exactInstrument
	e := t.readExecution(ctx, okxInstrumentsPath+"?instType=SWAP&instId="+url.QueryEscape(symbol), &rows)
	if e != nil {
		return exactInstrument{}, e
	}
	if len(rows) != 1 {
		return exactInstrument{}, errors.New("OKX instrument not found")
	}
	r := rows[0]
	if r.InstId != symbol || !strings.HasSuffix(symbol, "-USDT-SWAP") || r.InstType != "SWAP" || r.State != "live" || r.CtType != "linear" || r.SettleCcy != "USDT" || r.CtValCcy != strings.TrimSuffix(symbol, "-USDT-SWAP") || (r.CtMult != "" && !execution.Equal(r.CtMult, "1")) || !execution.Positive(execution.Decimal(r.CtVal)) {
		return r, errors.New("Frontier requires active OKX USDT linear perpetuals")
	}
	return r, nil
}
func (t *OKXTrader) executionMode(ctx context.Context) (string, error) {
	var rows []struct{ PosMode, AcctLv string }
	if e := t.readExecution(ctx, okxAccountConfigPath, &rows); e != nil {
		return "", e
	}
	if len(rows) != 1 || (rows[0].PosMode != "net_mode" && rows[0].PosMode != "long_short_mode") {
		return "", errors.New("unknown OKX position mode")
	}
	if rows[0].AcctLv != "2" && rows[0].AcctLv != "3" {
		return "", errors.New("Frontier supports OKX futures and multi-currency margin accounts; portfolio margin is not supported")
	}
	return rows[0].PosMode, nil
}

type executionPosition struct{ InstId, Pos, PosSide, MgnMode, AvgPx, MarkPx, Upl, Lever, LiqPx string }

func (t *OKXTrader) executionPositions(ctx context.Context, symbol string) ([]executionPosition, error) {
	var rows []executionPosition
	e := t.readExecution(ctx, okxPositionPath+"?instType=SWAP&instId="+url.QueryEscape(symbol), &rows)
	return rows, e
}

type executionReceipt struct {
	OrdId, ClOrdId, AlgoId, AlgoClOrdId, InstId, Side, PosSide, OrdType, Px, Sz, AccFillSz, AvgPx, State, SlTriggerPx, TpTriggerPx, TriggerPx, SlTriggerPxType, TpTriggerPxType, SlOrdPx, TpOrdPx string
	OrdIdList                                                                                                                                                                                     []string
	ReduceOnly                                                                                                                                                                                    json.RawMessage
}

func (t *OKXTrader) pendingExecution(ctx context.Context, symbol string) ([]executionReceipt, error) {
	var result []executionReceipt
	if e := t.readExecution(ctx, okxPendingOrdersPath+"?instType=SWAP&instId="+url.QueryEscape(symbol), &result); e != nil {
		return nil, e
	}
	for _, kind := range []string{"conditional", "oco", "trigger", "move_order_stop"} {
		var rows []executionReceipt
		if e := t.readExecution(ctx, okxAlgoPendingPath+"?instType=SWAP&instId="+url.QueryEscape(symbol)+"&ordType="+kind, &rows); e != nil {
			return nil, e
		}
		result = append(result, rows...)
	}
	return result, nil
}
func (t *OKXTrader) AccountSnapshot(ctx context.Context, symbol string) (execution.Account, error) {
	var out execution.Account
	inst, e := t.executionInstrument(ctx, symbol)
	if e != nil {
		return out, e
	}
	if _, e = t.executionMode(ctx); e != nil {
		return out, e
	}
	var balances []struct {
		Details []struct{ Ccy, Eq, AvailEq, AvailBal string }
	}
	if e = t.readExecution(ctx, okxAccountPath+"?ccy=USDT", &balances); e != nil {
		return out, e
	}
	found := false
	for _, b := range balances {
		for _, d := range b.Details {
			if d.Ccy == "USDT" {
				out.Equity = execution.Decimal(d.Eq)
				available := d.AvailEq
				if available == "" {
					available = d.AvailBal
				}
				out.Available = execution.Decimal(available)
				found = true
			}
		}
	}
	if !found {
		return out, errors.New("OKX USDT trading balance unavailable")
	}
	if out.Equity, e = execution.BalanceDecimal(string(out.Equity)); e != nil {
		return out, fmt.Errorf("invalid OKX account equity: %w", e)
	}
	if out.Available, e = execution.BalanceDecimal(string(out.Available)); e != nil {
		return out, fmt.Errorf("invalid OKX available margin: %w", e)
	}
	var fees []struct {
		Taker, TakerU string
		FeeGroup      []struct{ GroupId, Taker string }
	}
	family := inst.InstFamily
	if family == "" {
		family = strings.TrimSuffix(symbol, "-SWAP")
	}
	if e = t.readExecution(ctx, "/api/v5/account/trade-fee?instType=SWAP&instFamily="+url.QueryEscape(family), &fees); e != nil {
		return out, e
	}
	if len(fees) != 1 {
		return out, errors.New("OKX fee rate unavailable")
	}
	fee := fees[0].TakerU
	if fee == "" {
		fee = fees[0].Taker
	}
	if len(fees[0].FeeGroup) > 0 {
		fee = ""
		for _, group := range fees[0].FeeGroup {
			if group.GroupId == inst.GroupId || (inst.GroupId == "" && len(fees[0].FeeGroup) == 1) {
				fee = group.Taker
				break
			}
		}
		if fee == "" {
			return out, errors.New("OKX instrument fee group unavailable")
		}
	}
	f, e := execution.Number(fee)
	if e != nil {
		return out, e
	}
	f.Abs(f)
	out.FeeBPS, e = execution.ExactDecimal(f.Mul(f, big.NewRat(10000, 1)))
	if e != nil {
		return out, e
	}
	rows, e := t.executionPositions(ctx, symbol)
	if e != nil {
		return out, e
	}
	gross := new(big.Rat)
	for _, p := range rows {
		if p.InstId != symbol {
			continue
		}
		n, e := execution.Number(p.Pos)
		if e != nil {
			return out, e
		}
		gross.Add(gross, n.Abs(n))
	}
	contracts, e := execution.ExactDecimal(gross)
	if e != nil {
		return out, e
	}
	out.Position, e = execution.Multiply(string(contracts), inst.CtVal)
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
func (t *OKXTrader) PrepareExecution(ctx context.Context, symbol string) error {
	return t.PrepareExecutionLeverage(ctx, symbol, 1)
}
func (t *OKXTrader) PrepareExecutionLeverage(ctx context.Context, symbol string, leverage int) error {
	if leverage < 1 || leverage > 125 {
		return errors.New("invalid OKX leverage")
	}
	a, e := t.AccountSnapshot(ctx, symbol)
	if e != nil {
		return e
	}
	if !execution.Equal(string(a.Position), "0") || a.OpenOrders != 0 {
		return errors.New("OKX market already has a position or pending orders")
	}
	// Cross leverage is configured per instrument, in both position modes.
	body := map[string]string{"instId": symbol, "lever": strconv.Itoa(leverage), "mgnMode": "cross"}
	raw, e := t.doRequestContext(ctx, "POST", okxLeveragePath, body)
	if e != nil {
		return e
	}
	var rows []struct{ Lever string }
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return errors.New("OKX leverage confirmation missing")
	}
	for _, r := range rows {
		if !execution.Equal(r.Lever, strconv.Itoa(leverage)) {
			return errors.New("OKX did not confirm approved leverage")
		}
	}
	return nil
}
func okxSide(o execution.Order) (side, held string) {
	side = "buy"
	if o.Side == "short" {
		side = "sell"
	}
	held = o.Side
	if o.Role != "entry" {
		if side == "buy" {
			side = "sell"
		} else {
			side = "buy"
		}
	}
	if o.ReduceOnly {
		held = "short"
		if o.Side == "short" {
			held = "long"
		}
	}
	return
}
func (t *OKXTrader) SubmitOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	if e := execution.ValidateIntent(o); e != nil {
		return o, e
	}
	inst, e := t.executionInstrument(ctx, o.Symbol)
	if e != nil {
		return o, e
	}
	sz, e := execution.Divide(string(o.Quantity), inst.CtVal)
	if e != nil {
		return o, e
	}
	if !execution.Aligned(string(sz), inst.LotSz) {
		return o, errors.New("quantity is not an exact OKX contract increment")
	}
	n, _ := execution.Number(string(sz))
	min, e := execution.Number(inst.MinSz)
	if e != nil || n.Cmp(min) < 0 {
		return o, errors.New("quantity below OKX contract minimum")
	}
	max := inst.MaxLmtSz
	if o.Type == "market" || o.Role != "entry" {
		max = inst.MaxMktSz
	}
	if max != "" {
		m, e := execution.Number(max)
		if e != nil || n.Cmp(m) > 0 {
			return o, errors.New("quantity exceeds OKX order limit")
		}
	}
	if (o.Type != "market" || o.Role != "entry") && !execution.Aligned(string(o.Price), inst.TickSz) {
		return o, errors.New("price is not an exact OKX tick")
	}
	mode, e := t.executionMode(ctx)
	if e != nil {
		return o, e
	}
	side, held := okxSide(o)
	margin := "cross"
	if o.ReduceOnly || o.Role != "entry" {
		positions, e := t.executionPositions(ctx, o.Symbol)
		if e != nil {
			return o, e
		}
		available := new(big.Rat)
		found := false
		for _, p := range positions {
			if p.InstId != o.Symbol {
				continue
			}
			v, e := execution.Number(p.Pos)
			if e != nil {
				return o, e
			}
			s := p.PosSide
			if s == "net" {
				s = "long"
				if v.Sign() < 0 {
					s = "short"
				}
			}
			if s != held || v.Sign() == 0 {
				continue
			}
			if p.MgnMode != "cross" && p.MgnMode != "isolated" {
				return o, errors.New("unknown OKX margin mode")
			}
			if found && margin != p.MgnMode {
				return o, errors.New("multiple OKX margin modes require exchange review")
			}
			margin = p.MgnMode
			found = true
			available.Add(available, v.Abs(v))
		}
		if !found || n.Cmp(available) > 0 {
			return o, errors.New("OKX position changed; refresh before closing or protecting")
		}
	}
	body := map[string]any{"instId": o.Symbol, "tdMode": margin, "side": side, "sz": string(sz), "tag": okxTag}
	if mode == "long_short_mode" {
		body["posSide"] = held
	} else {
		body["posSide"] = "net"
		if o.ReduceOnly || o.Role != "entry" {
			body["reduceOnly"] = true
		}
	}
	path := okxOrderPath
	if o.Role == "entry" {
		body["clOrdId"] = o.ClientID
		body["ordType"] = "limit"
		body["px"] = string(o.Price)
		if o.Type == "market" {
			body["ordType"] = "market"
			delete(body, "px")
		} else if o.PostOnly {
			body["ordType"] = "post_only"
		}
	} else {
		path = okxAlgoOrderPath
		body["algoClOrdId"] = o.ClientID
		body["ordType"] = "conditional"
		prefix := o.Role
		body[prefix+"TriggerPx"] = string(o.Price)
		body[prefix+"OrdPx"] = "-1"
		body[prefix+"TriggerPxType"] = "mark"
	}
	data, e := t.doRequestContext(ctx, "POST", path, body)
	if e != nil {
		return o, e
	}
	var rows []struct{ OrdId, AlgoId, ClOrdId, AlgoClOrdId, SCode, SMsg string }
	if json.Unmarshal(data, &rows) != nil || len(rows) != 1 {
		return o, errors.New("unconfirmed OKX order response")
	}
	r := rows[0]
	if r.SCode != "0" {
		return o, fmt.Errorf("OKX order rejected (%s): %s", r.SCode, r.SMsg)
	}
	id, cid := r.OrdId, r.ClOrdId
	if o.Role != "entry" {
		id, cid = r.AlgoId, r.AlgoClOrdId
	}
	if id == "" || (cid != "" && cid != o.ClientID) {
		return o, errors.New("OKX order identity not confirmed")
	}
	o.OrderID = id
	o.Status = "NEW"
	o.Filled = "0"
	return o, nil
}
func (t *OKXTrader) LookupOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	previousFilled := o.Filled
	if e := execution.ValidateIntent(o); e != nil {
		return o, e
	}
	var rows []executionReceipt
	path := okxOrderPath + "?instId=" + url.QueryEscape(o.Symbol) + "&clOrdId=" + url.QueryEscape(o.ClientID)
	if o.Role != "entry" {
		path = okxAlgoOrderPath + "?algoClOrdId=" + url.QueryEscape(o.ClientID)
	}
	if e := t.readExecution(ctx, path, &rows); e != nil {
		return o, e
	}
	if len(rows) != 1 {
		return o, errors.New("OKX order not found; submission remains unconfirmed")
	}
	r := rows[0]
	id, cid := r.OrdId, r.ClOrdId
	if o.Role != "entry" {
		id, cid = r.AlgoId, r.AlgoClOrdId
	}
	if id == "" || cid != o.ClientID || r.InstId != o.Symbol || (o.OrderID != "" && id != o.OrderID) {
		return o, errors.New("OKX receipt identity mismatch")
	}
	inst, e := t.executionInstrument(ctx, o.Symbol)
	if e != nil {
		return o, e
	}
	quantity, e := execution.Multiply(r.Sz, inst.CtVal)
	if e != nil || !execution.Equal(string(quantity), string(o.Quantity)) {
		return o, errors.New("OKX receipt quantity mismatch")
	}
	side, held := okxSide(o)
	if r.Side != side || (r.PosSide != "net" && r.PosSide != held) {
		return o, errors.New("OKX receipt direction mismatch")
	}
	o.OrderID = id
	o.Filled = "0"
	if o.Role == "entry" {
		expected := "limit"
		if o.Type == "market" {
			expected = "market"
		} else if o.PostOnly {
			expected = "post_only"
		}
		if r.OrdType != expected || expected != "market" && !execution.Equal(r.Px, string(o.Price)) {
			return o, errors.New("OKX receipt order terms mismatch")
		}
		o.Status = map[string]string{"live": "NEW", "partially_filled": "PARTIALLY_FILLED", "filled": "FILLED", "canceled": "CANCELED", "mmp_canceled": "CANCELED"}[r.State]
		o.Filled, e = execution.Multiply(r.AccFillSz, inst.CtVal)
		if e != nil {
			return o, e
		}
		if execution.Positive(o.Filled) {
			if !execution.Positive(execution.Decimal(r.AvgPx)) {
				return o, errors.New("OKX fill price missing")
			}
			o.Average = execution.Decimal(r.AvgPx)
		}
	} else {
		price, trigger, executionPrice := r.SlTriggerPx, r.SlTriggerPxType, r.SlOrdPx
		if o.Role == "tp" {
			price, trigger, executionPrice = r.TpTriggerPx, r.TpTriggerPxType, r.TpOrdPx
		}
		if r.OrdType != "conditional" || !execution.Equal(price, string(o.Price)) || trigger != "mark" || executionPrice != "-1" {
			return o, errors.New("OKX protection terms mismatch")
		}
		o.Status = map[string]string{"live": "NEW", "effective": "TRIGGERED", "order_failed": "REJECTED", "canceled": "CANCELED", "pause": "UNKNOWN"}[r.State]
	}
	if (o.ReduceOnly || o.Role != "entry") && r.PosSide == "net" && string(r.ReduceOnly) != "true" && string(r.ReduceOnly) != "\"true\"" {
		return o, errors.New("OKX close or protection was not reduce-only")
	}
	if o.Status == "" {
		return o, errors.New("unrecognized OKX order state")
	}
	if o.Role != "entry" && o.Status == "TRIGGERED" {
		return t.lookupTriggeredProtection(ctx, o, r, inst, previousFilled)
	}
	return o, nil
}

// Triggering an algo creates regular orders. Only their verified fills prove
// an exit; the algo's effective state alone does not confirm a closed position.
func (t *OKXTrader) lookupTriggeredProtection(ctx context.Context, o execution.Order, parent executionReceipt, inst exactInstrument, previousFilled execution.Decimal) (execution.Order, error) {
	ids := parent.OrdIdList
	if len(ids) == 0 && parent.OrdId != "" {
		ids = []string{parent.OrdId}
	}
	if len(ids) == 0 {
		return o, nil // The exchange may publish linked order IDs later.
	}
	if len(ids) > 32 {
		return o, errors.New("OKX protection has too many linked orders to verify")
	}
	seen := map[string]bool{}
	filled, total, cost := new(big.Rat), new(big.Rat), new(big.Rat)
	terminal := true
	side, held := okxSide(o)
	for _, id := range ids {
		if id == "" || seen[id] {
			return o, errors.New("invalid OKX protection order linkage")
		}
		seen[id] = true
		var rows []executionReceipt
		if err := t.readExecution(ctx, okxOrderPath+"?instId="+url.QueryEscape(o.Symbol)+"&ordId="+url.QueryEscape(id), &rows); err != nil {
			return o, err
		}
		if len(rows) != 1 {
			return o, errors.New("OKX triggered exit order not yet confirmed")
		}
		r := rows[0]
		if r.OrdId != id || r.InstId != o.Symbol || r.Side != side || (r.PosSide != "net" && r.PosSide != held) || r.OrdType != "market" {
			return o, errors.New("OKX triggered exit identity or direction mismatch")
		}
		if r.PosSide == "net" && string(r.ReduceOnly) != "true" && string(r.ReduceOnly) != "\"true\"" {
			return o, errors.New("OKX triggered exit was not reduce-only")
		}
		quantity, err := execution.Multiply(r.Sz, inst.CtVal)
		if err != nil || !execution.Positive(quantity) {
			return o, errors.New("invalid OKX triggered exit quantity")
		}
		q, _ := execution.Number(string(quantity))
		f, err := execution.Multiply(r.AccFillSz, inst.CtVal)
		if err != nil {
			return o, errors.New("invalid OKX triggered exit fill")
		}
		fn, _ := execution.Number(string(f))
		if fn.Sign() < 0 || fn.Cmp(q) > 0 {
			return o, errors.New("invalid OKX triggered exit fill")
		}
		switch r.State {
		case "filled":
			if fn.Cmp(q) != 0 {
				return o, errors.New("incomplete OKX triggered exit fill")
			}
		case "live":
			if fn.Sign() != 0 {
				return o, errors.New("unexpected OKX live exit fill")
			}
			terminal = false
		case "partially_filled":
			if fn.Sign() <= 0 || fn.Cmp(q) >= 0 {
				return o, errors.New("invalid OKX partial exit fill")
			}
			terminal = false
		case "canceled", "mmp_canceled":
		default:
			return o, errors.New("unknown OKX triggered exit state")
		}
		if fn.Sign() > 0 {
			price, err := execution.Number(r.AvgPx)
			if err != nil || price.Sign() <= 0 {
				return o, errors.New("OKX triggered exit fill price missing")
			}
			cost.Add(cost, new(big.Rat).Mul(fn, price))
		}
		filled.Add(filled, fn)
		total.Add(total, q)
	}
	approved, _ := execution.Number(string(o.Quantity))
	if previousFilled == "" {
		previousFilled = "0"
	}
	previous, err := execution.Number(string(previousFilled))
	if err != nil || total.Cmp(approved) > 0 || filled.Cmp(approved) > 0 || filled.Cmp(previous) < 0 {
		return o, errors.New("OKX triggered exit quantity mismatch")
	}
	o.Filled, err = execution.ExactDecimal(filled)
	if err != nil {
		return o, err
	}
	if filled.Sign() > 0 {
		o.Average = execution.Decimal(new(big.Rat).Quo(cost, filled).FloatString(12))
	}
	if filled.Cmp(approved) == 0 {
		o.Status = "FILLED"
	} else if terminal && total.Cmp(approved) == 0 {
		o.Status = "CANCELED"
	}
	return o, nil
}
func (t *OKXTrader) CancelExecutionOrder(ctx context.Context, o execution.Order) error {
	if e := execution.ValidateIntent(o); e != nil {
		return e
	}
	if o.OrderID == "" {
		return errors.New("reconcile OKX order before cancellation")
	}
	path := okxCancelOrderPath
	var body any = map[string]string{"instId": o.Symbol, "ordId": o.OrderID, "clOrdId": o.ClientID}
	if o.Role != "entry" {
		path = okxCancelAlgoPath
		body = []map[string]string{{"instId": o.Symbol, "algoId": o.OrderID}}
	}
	raw, e := t.doRequestContext(ctx, "POST", path, body)
	if e != nil {
		return e
	}
	var rows []struct{ SCode, OrdId, AlgoId string }
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || rows[0].SCode != "0" || (rows[0].OrdId != o.OrderID && rows[0].AlgoId != o.OrderID) {
		return errors.New("OKX cancellation unconfirmed; reconcile order")
	}
	return nil
}
