package okx

import (
	"context"
	"errors"
	"nofx/trader/execution"
	"nofx/trader/types"
	"strconv"
	"strings"
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
