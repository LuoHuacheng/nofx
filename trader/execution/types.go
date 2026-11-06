// Package execution defines NOFX's shared, exact order execution contract.
// Callers must persist intent before submitting and reconcile ambiguous results.
package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"
)

type Decimal string

// Positive accepts bounded, plain base-10 values; never NaN, fractions or exponents.
func Positive(d Decimal) bool {
	if len(d) == 0 || len(d) > 40 {
		return false
	}
	for _, c := range d {
		if (c < '0' || c > '9') && c != '.' {
			return false
		}
	}
	parts := strings.Split(string(d), ".")
	if len(parts) > 2 || (len(parts) == 2 && len(parts[1]) > 12) {
		return false
	}
	r, ok := new(big.Rat).SetString(string(d))
	return ok && r.Sign() > 0
}

func NewClientID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

type Order struct {
	Type          string  `json:"type,omitempty"` // Empty preserves legacy limit entries.
	ReduceOnly    bool    `json:"reduce_only,omitempty"`
	PostOnly      bool    `json:"post_only,omitempty"`
	VenueClientID string  `json:"venue_client_id,omitempty"`
	ParentID      string  `json:"parent_id,omitempty"`
	ClientID      string  `json:"client_id"`
	OrderID       string  `json:"order_id"`
	Role          string  `json:"role"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Price         Decimal `json:"price"`
	Quantity      Decimal `json:"quantity"`
	Status        string  `json:"status"`
	Filled        Decimal `json:"filled"`
	Average       Decimal `json:"average,omitempty"`
	Error         string  `json:"error,omitempty"`
}
type Account struct {
	Equity, Available, FeeBPS Decimal
	Position                  Decimal
	OpenOrders                int
}

// Trader is implemented by NOFX exchange traders, independently of AI/planning.
// SubmitOrder sends exactly once; LookupOrder never assumes missing means filled.
type Trader interface {
	Venue() string
	AccountSnapshot(context.Context, string) (Account, error)
	PrepareExecution(context.Context, string) error
	SubmitOrder(context.Context, Order) (Order, error)
	LookupOrder(context.Context, Order) (Order, error)
	CancelExecutionOrder(context.Context, Order) error
}

// CanonicalClientID maps existing grid labels deterministically to a native
// 128-bit ID. Already canonical durable IDs pass through without changing.
func CanonicalClientID(id string) (string, error) {
	if id == "" {
		return NewClientID()
	}
	if raw, err := hex.DecodeString(id); err == nil && len(raw) == 16 {
		return hex.EncodeToString(raw), nil
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16]), nil
}
