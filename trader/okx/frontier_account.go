package okx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"nofx/trader/execution"
	"nofx/trader/types"
	"strconv"
	"strings"
	"time"
)

func (t *OKXTrader) frontierPositions() ([]map[string]interface{}, error) {
	ctx := context.Background()
	rows, e := t.executionPositions(ctx, "")
	if e != nil {
		return nil, e
	}
	out := []map[string]interface{}{}
	for _, p := range rows {
		v, e := execution.Number(p.Pos)
		if e != nil {
			return nil, e
		}
		if v.Sign() == 0 {
			continue
		}
		// Frontier only executes these markets; other collateral must never be reinterpreted as USDT.
		if !strings.HasSuffix(p.InstId, "-USDT-SWAP") {
			continue
		}
		inst, e := t.executionInstrument(ctx, p.InstId)
		if e != nil {
			return nil, e
		}
		side := p.PosSide
		if side == "net" {
			side = "long"
			if v.Sign() < 0 {
				side = "short"
			}
		}
		if side != "long" && side != "short" {
			return nil, errors.New("unknown OKX position direction")
		}
		contracts, e := execution.ExactDecimal(v.Abs(v))
		if e != nil {
			return nil, e
		}
		qty, e := execution.Multiply(string(contracts), inst.CtVal)
		if e != nil {
			return nil, e
		}
		row := map[string]interface{}{"symbol": p.InstId, "side": side, "mgnMode": p.MgnMode}
		for key, value := range map[string]string{"positionAmt": string(qty), "entryPrice": p.AvgPx, "markPrice": p.MarkPx, "unRealizedProfit": p.Upl, "leverage": p.Lever, "liquidationPrice": p.LiqPx} {
			if value == "" && (key == "liquidationPrice" || key == "leverage") {
				value = "0"
			}
			n, e := strconv.ParseFloat(value, 64)
			if e != nil {
				return nil, errors.New("invalid OKX position value")
			}
			row[key] = n
		}
		out = append(out, row)
	}
	return out, nil
}
func (t *OKXTrader) frontierOrders(symbol string) ([]types.OpenOrder, error) {
	ctx := context.Background()
	symbol = t.convertSymbol(symbol)
	inst, e := t.executionInstrument(ctx, symbol)
	if e != nil {
		return nil, e
	}
	rows, e := t.pendingExecution(ctx, symbol)
	if e != nil {
		return nil, e
	}
	out := []types.OpenOrder{}
	for _, r := range rows {
		if r.InstId != symbol {
			return nil, errors.New("OKX returned a different instrument")
		}
		size, e := execution.Multiply(r.Sz, inst.CtVal)
		if e != nil {
			return nil, e
		}
		qty, _ := strconv.ParseFloat(string(size), 64)
		price, _ := strconv.ParseFloat(r.Px, 64)
		id := r.OrdId
		kind := strings.ToUpper(r.OrdType)
		trigger := r.TriggerPx
		if r.AlgoId != "" {
			id = r.AlgoId
			if r.SlTriggerPx != "" {
				kind = "STOP_MARKET"
				trigger = r.SlTriggerPx
			}
			if r.TpTriggerPx != "" {
				kind = "TAKE_PROFIT_MARKET"
				trigger = r.TpTriggerPx
			}
		}
		stop, _ := strconv.ParseFloat(trigger, 64)
		out = append(out, types.OpenOrder{OrderID: id, Symbol: symbol, Side: strings.ToUpper(r.Side), PositionSide: strings.ToUpper(r.PosSide), Type: kind, Price: price, StopPrice: stop, Quantity: qty, Status: "NEW"})
	}
	return out, nil
}

func (t *OKXTrader) frontierBalance() (map[string]interface{}, error) {
	var rows []struct {
		Details []struct{ Ccy, Eq, AvailEq, AvailBal, Upl string }
	}
	if e := t.readExecution(context.Background(), okxAccountPath+"?ccy=USDT", &rows); e != nil {
		return nil, e
	}
	for _, r := range rows {
		for _, d := range r.Details {
			if d.Ccy != "USDT" {
				continue
			}
			available := d.AvailEq
			if available == "" {
				available = d.AvailBal
			}
			out := map[string]interface{}{}
			for k, v := range map[string]string{"total_equity": d.Eq, "availableBalance": available, "totalUnrealizedProfit": d.Upl} {
				if k == "totalUnrealizedProfit" && v == "" {
					v = "0"
				}
				n, e := strconv.ParseFloat(v, 64)
				if e != nil {
					return nil, errors.New("OKX USDT balance unavailable")
				}
				out[k] = n
			}
			out["totalWalletBalance"] = out["total_equity"].(float64) - out["totalUnrealizedProfit"].(float64)
			return out, nil
		}
	}
	return nil, errors.New("OKX USDT trading balance unavailable")
}

// ManagedCapabilities reports the amend-in-place guarantee only for the stop
// trigger, which /trade/amend-algos changes without cancelling the order.
func (t *OKXTrader) ManagedCapabilities() execution.Capabilities {
	return execution.Capabilities{SafeStopAmend: true, PrivateEvents: true}
}

// okxAmendAlgoPath changes a stop trigger in place. It is not a cancel-then-place
// sequence, so a refusal at either step leaves the original protection live.
const okxAmendAlgoPath = "/api/v5/trade/amend-algos"

// AmendStop tightens the stop trigger of one protection order and confirms it
// through the same read path used for reconciliation. An unconfirmed amend is
// reported as an error; the restored trigger is never assumed.
func (t *OKXTrader) AmendStop(ctx context.Context, o execution.Order, price execution.Decimal) (execution.Order, error) {
	if o.Role != "sl" || o.OrderID == "" {
		return o, errors.New("only a submitted stop-loss can be amended")
	}
	if !execution.Positive(price) {
		return o, errors.New("invalid stop price")
	}
	inst, e := t.executionInstrument(ctx, o.Symbol)
	if e != nil {
		return o, e
	}
	if !execution.Aligned(string(price), inst.TickSz) {
		return o, errors.New("stop price is not an exact OKX tick")
	}
	body := map[string]string{"instId": o.Symbol, "algoId": o.OrderID, "newSlTriggerPx": string(price), "newSlTriggerPxType": "mark"}
	raw, e := t.doRequestContext(ctx, "POST", okxAmendAlgoPath, body)
	if e != nil {
		return o, e
	}
	var rows []struct{ AlgoId, AlgoClOrdId, SCode, SMsg string }
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
		return o, errors.New("OKX stop amendment response is unconfirmed")
	}
	if rows[0].SCode != "0" || rows[0].AlgoId != o.OrderID {
		return o, fmt.Errorf("OKX stop amendment rejected (%s): %s", rows[0].SCode, rows[0].SMsg)
	}
	// Read-back proves the live order still exists and now carries the new
	// trigger; the caller never treats the acknowledgement as the amendment.
	checked := o
	checked.Price = price
	observed, e := t.LookupOrder(ctx, checked)
	if e != nil || observed.OrderID != o.OrderID || observed.Status != "NEW" || !execution.Equal(string(observed.Price), string(price)) {
		return o, errors.New("OKX stop amendment is unconfirmed; verify the protective order at the exchange")
	}
	return observed, nil
}

func (t *OKXTrader) Events(ctx context.Context, fn func(execution.AccountEvent) error) error {
	return t.ManagedEvents(ctx, fn)
}

// okxManagedSide names the position an order acts on. One-way orders carry
// posSide=net, so their direction is the opposite of the order side.
func okxManagedSide(posSide, side string) (string, error) {
	switch posSide {
	case "long", "short":
		return posSide, nil
	case "net", "":
	default:
		return "", errors.New("unknown OKX position side")
	}
	switch side {
	case "sell":
		return "long", nil
	case "buy":
		return "short", nil
	}
	return "", errors.New("unknown OKX order side")
}

// ManagedSnapshot reads the venue state used to verify unattended positions.
// Contract counts never leave the adapter; any unreadable field fails the read.
func (t *OKXTrader) ManagedSnapshot(ctx context.Context) (execution.ManagedAccount, error) {
	out := execution.ManagedAccount{
		Venue:       t.Venue(),
		Environment: t.environment,
		Positions:   []execution.ManagedPosition{},
		Orders:      []execution.Order{},
		ObservedAt:  time.Now().UTC(),
	}
	if !t.environment.Valid() {
		return out, errors.New("managed OKX environment is not set")
	}
	mode, err := t.executionMode(ctx)
	if err != nil {
		return out, err
	}
	out.PositionMode = "hedge"
	if mode == "net_mode" {
		out.PositionMode = "one_way"
	}
	var balances []struct {
		Details []struct{ Ccy, Eq, AvailEq, AvailBal, Imr string }
	}
	if err := t.readExecution(ctx, okxAccountPath+"?ccy=USDT", &balances); err != nil {
		return out, err
	}
	found := false
	for _, b := range balances {
		for _, d := range b.Details {
			if d.Ccy != "USDT" {
				continue
			}
			available := d.AvailEq
			if available == "" {
				available = d.AvailBal
			}
			if out.Equity, err = execution.BalanceDecimal(d.Eq); err != nil {
				return out, errors.New("OKX account equity is unavailable")
			}
			if out.Available, err = execution.BalanceDecimal(available); err != nil {
				return out, errors.New("OKX available margin is unavailable")
			}
			if out.MarginUsed, err = execution.BalanceDecimal(d.Imr); err != nil {
				return out, errors.New("OKX margin used is unavailable")
			}
			found = true
		}
	}
	if !found {
		return out, errors.New("OKX USDT trading balance unavailable")
	}

	instruments := map[string]exactInstrument{}
	marginModes := map[string]bool{}
	rows, err := t.executionPositions(ctx, "")
	if err != nil {
		return out, err
	}
	for _, p := range rows {
		v, err := execution.Number(p.Pos)
		if err != nil {
			return out, errors.New("invalid OKX position size")
		}
		if v.Sign() == 0 || !strings.HasSuffix(p.InstId, "-USDT-SWAP") {
			continue
		}
		inst, ok := instruments[p.InstId]
		if !ok {
			if inst, err = t.executionInstrument(ctx, p.InstId); err != nil {
				return out, err
			}
			instruments[p.InstId] = inst
		}
		contracts, err := execution.ExactDecimal(v.Abs(v))
		if err != nil {
			return out, err
		}
		if !execution.Aligned(string(contracts), inst.LotSz) {
			return out, errors.New("OKX position quantity is not an exact contract lot step")
		}
		qty, err := execution.Multiply(string(contracts), inst.CtVal)
		if err != nil {
			return out, err
		}
		side := p.PosSide
		if side == "net" || side == "" {
			side = "long"
			if v.Sign() < 0 {
				side = "short"
			}
		}
		if side != "long" && side != "short" {
			return out, errors.New("unknown OKX position direction")
		}
		if p.MgnMode != "cross" && p.MgnMode != "isolated" {
			return out, errors.New("unknown OKX margin mode")
		}
		margin := p.Imr
		if margin == "" {
			margin = "0"
		}
		out.Positions = append(out.Positions, execution.ManagedPosition{
			Symbol:      p.InstId,
			Side:        side,
			Quantity:    qty,
			Entry:       execution.Decimal(p.AvgPx),
			Mark:        execution.Decimal(p.MarkPx),
			Liquidation: execution.Decimal(p.LiqPx),
			Margin:      execution.Decimal(margin),
		})
		marginModes[p.MgnMode] = true
	}
	if len(marginModes) == 1 {
		for mode := range marginModes {
			out.MarginMode = mode
		}
	}

	pendings, err := t.pendingExecution(ctx, "")
	if err != nil {
		return out, err
	}
	for _, r := range pendings {
		if !strings.HasSuffix(r.InstId, "-USDT-SWAP") {
			continue
		}
		inst, ok := instruments[r.InstId]
		if !ok {
			if inst, err = t.executionInstrument(ctx, r.InstId); err != nil {
				return out, err
			}
			instruments[r.InstId] = inst
		}
		qty, err := execution.Multiply(r.Sz, inst.CtVal)
		if err != nil {
			return out, errors.New("invalid OKX pending order size")
		}
		side, err := okxManagedSide(r.PosSide, r.Side)
		if err != nil {
			return out, err
		}
		role, orderType, price := "entry", r.OrdType, r.Px
		if r.AlgoId != "" {
			orderType = "market"
			switch {
			case r.SlTriggerPx != "":
				role, price = "sl", r.SlTriggerPx
			case r.TpTriggerPx != "":
				role, price = "tp", r.TpTriggerPx
			default:
				// An entry trigger order carries triggerPx but no protective leg.
				return out, errors.New("OKX pending algo order is not a confirmed SL/TP protection")
			}
		}
		id, clientID := r.OrdId, r.ClOrdId
		if id == "" {
			id = r.AlgoId
		}
		if clientID == "" {
			clientID = r.AlgoClOrdId
		}
		out.Orders = append(out.Orders, execution.Order{
			Type:          orderType,
			VenueClientID: clientID,
			OrderID:       id,
			Role:          role,
			Symbol:        r.InstId,
			Side:          side,
			Price:         execution.Decimal(price),
			Quantity:      qty,
			Status:        r.State,
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

// VerifyManagedSettings only reads: it never switches position or margin mode
// and never sets leverage.
func (t *OKXTrader) VerifyManagedSettings(ctx context.Context, symbol string, leverage int) error {
	if !t.environment.Valid() {
		return errors.New("managed OKX environment is not set")
	}
	if symbol == "" || leverage < 1 || leverage > 125 {
		return errors.New("invalid managed OKX settings request")
	}
	mode, err := t.executionMode(ctx)
	if err != nil {
		return err
	}
	if mode != "net_mode" {
		return errors.New("OKX account is in long/short mode; unattended execution requires net mode")
	}
	positions, err := t.executionPositions(ctx, symbol)
	if err != nil {
		return err
	}
	for _, p := range positions {
		if p.InstId != symbol {
			continue
		}
		v, err := execution.Number(p.Pos)
		if err != nil {
			return errors.New("invalid OKX position size")
		}
		if v.Sign() != 0 && p.MgnMode != "isolated" {
			return errors.New("OKX position is not in isolated margin mode")
		}
	}
	var rows []struct{ InstId, MgnMode, Lever string }
	if err := t.readExecution(ctx, okxLeverageInfoPath+"?instType=SWAP&instId="+url.QueryEscape(symbol)+"&mgnMode=isolated", &rows); err != nil {
		return err
	}
	for _, r := range rows {
		if (r.InstId == "" || r.InstId == symbol) && r.MgnMode == "isolated" && execution.Equal(r.Lever, strconv.Itoa(leverage)) {
			return nil
		}
	}
	return errors.New("OKX leverage does not match the approved value")
}

var _ execution.ManagedTrader = (*OKXTrader)(nil)
