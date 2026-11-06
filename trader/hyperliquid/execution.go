package hyperliquid

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	hl "github.com/sonirico/go-hyperliquid"
	"math/big"
	"nofx/trader/execution"
	"regexp"
	"strconv"
	"strings"
)

func (b *HyperliquidTrader) Venue() string { return "hyperliquid" }

func (b *HyperliquidTrader) executionExchange(symbol string) (*hl.Exchange, string, error) {
	coin := convertSymbolToHyperliquid(symbol)
	if strings.Contains(coin, ":") && !strings.HasPrefix(coin, "xyz:") {
		return nil, coin, errors.New("exact execution supports core perps and xyz HIP-3 only")
	}
	ex, e := b.leverageExchange(coin)
	return ex, coin, e
}
func (b *HyperliquidTrader) AccountSnapshot(ctx context.Context, symbol string) (execution.Account, error) {
	out := execution.Account{Position: "0"}
	ex, coin, e := b.executionExchange(symbol)
	if e != nil {
		return out, e
	}
	request := map[string]any{"type": "clearinghouseState", "user": b.walletAddr}
	if strings.HasPrefix(coin, "xyz:") {
		request["dex"] = "xyz"
	}
	raw, e := b.directAccountInfo(ctx, request)
	if e != nil {
		return out, e
	}
	var state struct {
		MarginSummary struct {
			AccountValue string `json:"accountValue"`
		} `json:"marginSummary"`
		Withdrawable   string `json:"withdrawable"`
		AssetPositions []struct {
			Position struct {
				Coin string `json:"coin"`
				Size string `json:"szi"`
			} `json:"position"`
		} `json:"assetPositions"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return out, errors.New("invalid Hyperliquid account state")
	}
	out.Equity = execution.Decimal(state.MarginSummary.AccountValue)
	out.Available = execution.Decimal(state.Withdrawable)
	if b.isUnifiedAccount {
		// Same collateral policy as NOFX GetBalance: unified Spot USDC is
		// already account equity. Do not add core/xyz collateral views or PnL.
		spotRaw, err := b.directAccountInfo(ctx, map[string]any{"type": "spotClearinghouseState", "user": b.walletAddr})
		if err != nil {
			return out, errors.New("could not read unified account USDC collateral")
		}
		out.Equity, out.Available, err = unifiedExecutionCollateral(spotRaw)
		if err != nil {
			return out, err
		}
	}
	qty := new(big.Rat)
	for _, p := range state.AssetPositions {
		if p.Position.Coin == coin {
			n, ok := new(big.Rat).SetString(p.Position.Size)
			if !ok {
				return out, errors.New("invalid Hyperliquid position size")
			}
			qty.Add(qty, n.Abs(n))
		}
	}
	out.Position = execution.Decimal(qty.FloatString(12))
	orders, e := ex.Info().OpenOrders(ctx, b.walletAddr, func() string {
		if strings.HasPrefix(coin, "xyz:") {
			return "xyz"
		}
		return ""
	}())
	if e != nil {
		return out, errors.New("Hyperliquid open orders unavailable")
	}
	for _, o := range orders {
		if o.Coin == coin {
			out.OpenOrders++
		}
	}
	fees, e := ex.Info().UserFees(ctx, b.walletAddr)
	if e != nil {
		return out, errors.New("Hyperliquid fee rate unavailable")
	}
	a, ok := new(big.Rat).SetString(fees.UserCrossRate)
	if !ok {
		return out, errors.New("invalid Hyperliquid fee rate")
	}
	m, ok := new(big.Rat).SetString(fees.UserAddRate)
	if !ok {
		return out, errors.New("invalid Hyperliquid maker fee")
	}
	if m.Cmp(a) > 0 {
		a = m
	}
	a.Mul(a, big.NewRat(10000, 1))
	a.Add(a, big.NewRat(5, 1))
	out.FeeBPS = execution.Decimal(a.FloatString(12))
	return out, nil
}

var executionCollateralDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,12})?$`)

func unifiedExecutionCollateral(raw []byte) (execution.Decimal, execution.Decimal, error) {
	var state struct {
		Balances *[]struct {
			Coin  string  `json:"coin"`
			Total string  `json:"total"`
			Hold  *string `json:"hold"`
		} `json:"balances"`
	}
	if json.Unmarshal(raw, &state) != nil || state.Balances == nil {
		return "", "", errors.New("unified USDC collateral response is incomplete")
	}
	var total, held *big.Rat
	for _, balance := range *state.Balances {
		if balance.Coin != "USDC" {
			continue
		}
		if total != nil || balance.Hold == nil {
			return "", "", errors.New("unified USDC collateral reservations are ambiguous")
		}
		parse := func(value string) (*big.Rat, bool) {
			if len(value) > 40 || !executionCollateralDecimal.MatchString(value) {
				return nil, false
			}
			return new(big.Rat).SetString(value)
		}
		var ok bool
		total, ok = parse(balance.Total)
		if !ok {
			return "", "", errors.New("invalid unified USDC total")
		}
		held, ok = parse(*balance.Hold)
		if !ok {
			return "", "", errors.New("invalid unified USDC hold")
		}
	}
	if total == nil {
		return "0", "0", nil
	}
	available := new(big.Rat).Sub(total, held)
	if available.Sign() < 0 {
		available.SetInt64(0)
	}
	return execution.Decimal(total.FloatString(12)), execution.Decimal(available.FloatString(12)), nil
}
func (b *HyperliquidTrader) PrepareExecution(ctx context.Context, symbol string) error {
	return b.PrepareExecutionLeverage(ctx, symbol, 1)
}
func (b *HyperliquidTrader) PrepareExecutionLeverage(ctx context.Context, symbol string, leverage int) error {
	if leverage < 1 || leverage > 20 {
		return errors.New("unsupported planning leverage")
	}
	if e := b.CheckTradingAuthorization(); e != nil {
		return e
	}
	ex, coin, e := b.executionExchange(symbol)
	if e != nil {
		return e
	}
	_, e = ex.UpdateLeverage(ctx, leverage, coin, true)
	return e
}
func (b *HyperliquidTrader) SubmitOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	if o.Type != "" && (o.Type != "market" || o.Role != "entry" || o.PostOnly || o.ReduceOnly) {
		return o, errors.New("unsupported execution order type")
	}
	if e := b.CheckTradingAuthorization(); e != nil {
		return o, e
	}
	ex, coin, e := b.executionExchange(o.Symbol)
	if e != nil {
		return o, e
	}
	rawID, idErr := hex.DecodeString(o.ClientID)
	if idErr != nil || len(rawID) != 16 || !execution.Positive(o.Price) || !execution.Positive(o.Quantity) || (o.Side != "long" && o.Side != "short") {
		return o, errors.New("invalid locked order")
	}
	qty, e := strconv.ParseFloat(string(o.Quantity), 64)
	if e != nil {
		return o, e
	}
	price, e := strconv.ParseFloat(string(o.Price), 64)
	if e != nil {
		return o, e
	}
	rounded, e := b.roundOrderPrice(coin, price)
	if e != nil || rounded != price || b.roundToSzDecimals(coin, qty) != qty {
		return o, errors.New("exchange rounding would change the approved price or quantity")
	}
	client := "0x" + o.ClientID
	buy := o.Side == "long"
	typ := hl.OrderType{Limit: &hl.LimitOrderType{Tif: hl.TifGtc}}
	if o.Type == "market" {
		typ.Limit.Tif = hl.TifIoc
	}
	reduce := o.ReduceOnly
	if o.PostOnly {
		typ.Limit.Tif = hl.TifAlo
	}
	if o.Role != "entry" {
		buy = !buy
		reduce = true
		if o.Role != "sl" && o.Role != "tp" {
			return o, errors.New("invalid protective role")
		}
		typ = hl.OrderType{Trigger: &hl.TriggerOrderType{TriggerPx: price, IsMarket: true, Tpsl: hl.Tpsl(o.Role)}}
	}
	receipt, e := b.submitNativeOrder(ctx, ex, hl.CreateOrderRequest{Coin: coin, IsBuy: buy, Price: price, Size: qty, ReduceOnly: reduce, ClientOrderID: &client, OrderType: typ})
	if e != nil {
		return o, errors.New("Hyperliquid submission is unconfirmed. Reconcile by cloid; do not resubmit.")
	}
	if receipt.Error != nil {
		return o, fmt.Errorf("Hyperliquid rejected locked %s order", o.Role)
	}
	if receipt.Resting != nil && receipt.Resting.Oid > 0 {
		if o.Type == "market" {
			return o, errors.New("unexpected resting market receipt; reconcile by cloid")
		}
		o.OrderID = fmt.Sprint(receipt.Resting.Oid)
		o.Status = "NEW"
		o.Filled = "0"
		return o, nil
	}
	if receipt.Filled != nil && receipt.Filled.Oid > 0 {
		filled, ok := new(big.Rat).SetString(receipt.Filled.TotalSz)
		requested, _ := new(big.Rat).SetString(string(o.Quantity))
		if !ok || filled.Sign() <= 0 || filled.Cmp(requested) > 0 {
			return o, errors.New("invalid Hyperliquid fill quantity")
		}
		o.OrderID = fmt.Sprint(receipt.Filled.Oid)
		o.Status = "FILLED"
		if filled.Cmp(requested) < 0 {
			o.Status = "PARTIALLY_FILLED"
			if o.Type == "market" {
				// IOC cancels the unfilled remainder; protect only confirmed fills.
				o.Status = "CANCELED"
			}
		}
		o.Filled = execution.Decimal(receipt.Filled.TotalSz)
		o.Average = execution.Decimal(receipt.Filled.AvgPx)
		return o, nil
	}
	return o, errors.New("Hyperliquid acknowledgement has no native order ID")
}
func (b *HyperliquidTrader) LookupOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	ex, coin, e := b.executionExchange(o.Symbol)
	if e != nil {
		return o, e
	}
	r, e := ex.Info().QueryOrderByCloid(ctx, b.walletAddr, "0x"+o.ClientID)
	if e != nil || r == nil || r.Status != hl.OrderQueryStatusSuccess {
		return o, errors.New("Hyperliquid order is unconfirmed; unknownOid does not mean filled")
	}
	native := r.Order.Order
	if native.Oid <= 0 || native.Coin != coin || native.Cloid == nil || *native.Cloid != "0x"+o.ClientID {
		return o, errors.New("Hyperliquid order identity mismatch")
	}
	o.OrderID = fmt.Sprint(native.Oid)
	return executionOrderState(o, native, r.Order.Status)
}

func executionOrderState(o execution.Order, native hl.QueriedOrder, status hl.OrderStatusValue) (execution.Order, error) {
	total, ok := new(big.Rat).SetString(native.OrigSz)
	expected, expectedOK := new(big.Rat).SetString(string(o.Quantity))
	if !ok || !expectedOK || total.Sign() <= 0 || total.Cmp(expected) != 0 {
		return o, errors.New("invalid original size")
	}
	left, ok := new(big.Rat).SetString(native.Sz)
	if !ok {
		return o, errors.New("invalid remaining size")
	}
	filled := new(big.Rat).Sub(total, left)
	if left.Sign() < 0 || filled.Sign() < 0 {
		return o, errors.New("inconsistent Hyperliquid fill size")
	}
	previousFilled := o.Filled
	o.Filled = execution.Decimal(filled.FloatString(12))
	switch string(status) {
	case "open":
		o.Status = "NEW"
		if filled.Sign() > 0 {
			o.Status = "PARTIALLY_FILLED"
		}
	case "filled":
		o.Status = "FILLED"
		o.Filled = execution.Decimal(total.FloatString(12))
	case "canceled", "marginCanceled", "vaultWithdrawalCanceled", "openInterestCapCanceled", "selfTradeCanceled", "reduceOnlyCanceled", "siblingFilledCanceled", "delistedCanceled":
		o.Status = "CANCELED"
	case "triggered":
		o.Status = "TRIGGERED"
		o.Filled = previousFilled // Trigger acknowledgement is not a confirmed fill.
	default:
		if strings.HasSuffix(string(status), "Rejected") {
			o.Status = "REJECTED"
		} else {
			return o, errors.New("unknown Hyperliquid order state")
		}
	}
	return o, nil
}
func (b *HyperliquidTrader) CancelExecutionOrder(ctx context.Context, o execution.Order) error {
	ex, coin, e := b.executionExchange(o.Symbol)
	if e != nil {
		return e
	}
	r, e := ex.CancelByCloid(ctx, coin, "0x"+o.ClientID)
	if e != nil || r == nil || !r.Ok {
		return errors.New("Hyperliquid cancellation not confirmed")
	}
	for _, s := range r.Data.Statuses {
		if value, ok := s.String(); !ok || value != "success" {
			return errors.New("Hyperliquid cancellation rejected")
		}
	}
	if len(r.Data.Statuses) != 1 {
		return errors.New("missing cancellation receipt")
	}
	return nil
}

var _ execution.Trader = (*HyperliquidTrader)(nil)
