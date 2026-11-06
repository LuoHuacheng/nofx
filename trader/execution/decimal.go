package execution

import (
	"errors"
	"math/big"
	"strings"
)

// ExactDecimal rejects values that would be rounded by the execution contract.
func ExactDecimal(r *big.Rat) (Decimal, error) {
	s := r.FloatString(12)
	check, ok := new(big.Rat).SetString(s)
	if !ok || check.Cmp(r) != 0 {
		return "", errors.New("unsupported execution precision")
	}
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		s = "0"
	}
	return Decimal(s), nil
}
func Number(s string) (*big.Rat, error) {
	if s == "" || len(s) > 40 {
		return nil, errors.New("missing or invalid exchange number")
	}
	for i, c := range s {
		if (c < '0' || c > '9') && c != '.' && !(c == '-' && i == 0) {
			return nil, errors.New("invalid exchange number")
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("invalid exchange number")
	}
	return r, nil
}

// BalanceDecimal conservatively floors account funds to the planning precision.
// Exchanges can report more than 12 decimal places for equity and free margin.
// Never use this conversion for order prices, quantities or position exposure.
func BalanceDecimal(s string) (Decimal, error) {
	r, err := Number(s)
	if err != nil {
		return "", err
	}
	scale := big.NewInt(1_000_000_000_000)
	n := new(big.Int).Mul(r.Num(), scale)
	units := new(big.Int).Div(n, r.Denom())
	return ExactDecimal(new(big.Rat).SetFrac(units, scale))
}
func Multiply(a, b string) (Decimal, error) {
	x, e := Number(a)
	if e != nil {
		return "", e
	}
	y, e := Number(b)
	if e != nil {
		return "", e
	}
	return ExactDecimal(new(big.Rat).Mul(x, y))
}
func Divide(a, b string) (Decimal, error) {
	x, e := Number(a)
	if e != nil {
		return "", e
	}
	y, e := Number(b)
	if e != nil || y.Sign() <= 0 {
		return "", errors.New("invalid contract value")
	}
	return ExactDecimal(new(big.Rat).Quo(x, y))
}
func Equal(a, b string) bool {
	x, e := Number(a)
	y, f := Number(b)
	return e == nil && f == nil && x.Cmp(y) == 0
}
func Aligned(value, step string) bool {
	x, e := Number(value)
	y, f := Number(step)
	return e == nil && f == nil && x.Sign() > 0 && y.Sign() > 0 && new(big.Rat).Quo(x, y).IsInt()
}

// ValidOrderPrice keeps strict price precision for orders sent to the exchange.
// A reduce-only market close carries the observed average entry only as review
// metadata. That weighted average is not a limit price and may have more digits.
func ValidOrderPrice(o Order) bool {
	if o.Role == "entry" && o.Type == "market" && o.ReduceOnly {
		price, err := Number(string(o.Price))
		return err == nil && price.Sign() > 0
	}
	return Positive(o.Price)
}

func ValidateIntent(o Order) error {
	id, e := CanonicalClientID(o.ClientID)
	if e != nil || id != o.ClientID || (o.VenueClientID != "" && o.VenueClientID != id) {
		return errors.New("invalid durable client order ID")
	}
	if o.Side != "long" && o.Side != "short" {
		return errors.New("invalid execution side")
	}
	if o.Role != "entry" && o.Role != "sl" && o.Role != "tp" {
		return errors.New("invalid order role")
	}
	if o.Type != "" && o.Type != "limit" && o.Type != "market" {
		return errors.New("unsupported order type")
	}
	if o.ReduceOnly && (o.Role != "entry" || o.Type != "market") {
		return errors.New("unsupported reduce-only intent")
	}
	if !Positive(o.Quantity) || !ValidOrderPrice(o) || o.PostOnly && (o.Role != "entry" || o.Type == "market") {
		return errors.New("invalid order quantity, price or post-only mode")
	}
	return nil
}
