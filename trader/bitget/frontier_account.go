package bitget

import (
	"context"
	"errors"
	"nofx/trader/types"
	"strconv"
	"strings"
)

func (t *BitgetTrader) frontierPositions() ([]map[string]interface{}, error) {
	var rows []struct{ Symbol, HoldSide, Total, OpenPriceAvg, MarkPrice, UnrealizedPL, Leverage, LiquidationPrice, MarginMode string }
	if e := t.readExecution(context.Background(), bitgetPositionPath+"?productType=USDT-FUTURES&marginCoin=USDT", &rows); e != nil {
		return nil, e
	}
	out := []map[string]interface{}{}
	for _, p := range rows {
		total, e := strconv.ParseFloat(p.Total, 64)
		if e != nil || total < 0 {
			return nil, errors.New("invalid Bitget position size")
		}
		if total == 0 {
			continue
		}
		if p.HoldSide != "long" && p.HoldSide != "short" {
			return nil, errors.New("unknown Bitget position direction")
		}
		r := map[string]interface{}{"symbol": p.Symbol, "side": p.HoldSide, "positionAmt": total, "marginMode": p.MarginMode}
		for key, value := range map[string]string{"entryPrice": p.OpenPriceAvg, "markPrice": p.MarkPrice, "unRealizedProfit": p.UnrealizedPL, "leverage": p.Leverage, "liquidationPrice": p.LiquidationPrice} {
			if key == "liquidationPrice" && value == "" {
				value = "0"
			}
			v, e := strconv.ParseFloat(value, 64)
			if e != nil {
				return nil, errors.New("invalid Bitget position value")
			}
			r[key] = v
		}
		out = append(out, r)
	}
	return out, nil
}
func (t *BitgetTrader) frontierOrders(symbol string) ([]types.OpenOrder, error) {
	rows, e := t.pendingExecution(context.Background(), symbol)
	if e != nil {
		return nil, e
	}
	out := []types.OpenOrder{}
	for _, r := range rows {
		if !strings.EqualFold(r.Symbol, symbol) {
			return nil, errors.New("Bitget returned a different instrument")
		}
		qty, e := strconv.ParseFloat(r.Size, 64)
		if e != nil {
			return nil, errors.New("invalid Bitget order quantity")
		}
		price, _ := strconv.ParseFloat(r.Price, 64)
		stop, _ := strconv.ParseFloat(r.TriggerPrice, 64)
		kind := strings.ToUpper(r.OrderType)
		if r.PlanType != "" {
			kind = "TRIGGER"
			if r.PlanType == "loss_plan" || r.PlanType == "pos_loss" {
				kind = "STOP_MARKET"
			}
			if r.PlanType == "profit_plan" || r.PlanType == "pos_profit" {
				kind = "TAKE_PROFIT_MARKET"
			}
		}
		out = append(out, types.OpenOrder{OrderID: r.OrderId, Symbol: symbol, Side: strings.ToUpper(r.Side), PositionSide: strings.ToUpper(r.PosSide), Type: kind, Price: price, StopPrice: stop, Quantity: qty, Status: "NEW"})
	}
	return out, nil
}

func (t *BitgetTrader) frontierBalance() (map[string]interface{}, error) {
	var rows []struct{ MarginCoin, AccountEquity, Available, CrossedMaxAvailable, UnrealizedPL string }
	if e := t.readExecution(context.Background(), bitgetAccountPath+"?productType=USDT-FUTURES", &rows); e != nil {
		return nil, e
	}
	for _, d := range rows {
		if d.MarginCoin != "USDT" {
			continue
		}
		out := map[string]interface{}{}
		for k, v := range map[string]string{"total_equity": d.AccountEquity, "availableBalance": d.Available, "totalUnrealizedProfit": d.UnrealizedPL} {
			if k == "totalUnrealizedProfit" && v == "" {
				v = "0"
			}
			n, e := strconv.ParseFloat(v, 64)
			if e != nil {
				return nil, errors.New("Bitget USDT balance unavailable")
			}
			out[k] = n
		}
		out["totalWalletBalance"] = out["total_equity"].(float64) - out["totalUnrealizedProfit"].(float64)
		return out, nil
	}
	return nil, errors.New("Bitget USDT futures balance unavailable")
}
