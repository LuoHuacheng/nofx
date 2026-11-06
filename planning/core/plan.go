// Package core owns deterministic planning and the approval boundary.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"nofx/trader/execution"
	"strconv"
	"strings"
	"time"
)

// Decimal is an exact base-10 number encoded as a JSON string. No binary floats
// participate in risk calculations or quantity rounding.
type Decimal = execution.Decimal

func rat(d Decimal) (*big.Rat, error) {
	if len(d) == 0 || len(d) > 40 {
		return nil, errors.New("invalid decimal length")
	}
	for i, c := range d {
		if (c < '0' || c > '9') && c != '.' && !(i == 0 && c == '-') {
			return nil, errors.New("invalid decimal")
		}
	}
	if parts := strings.Split(string(d), "."); len(parts) > 2 || (len(parts) == 2 && len(parts[1]) > 12) {
		return nil, errors.New("decimal precision exceeds 12 places")
	}
	r, ok := new(big.Rat).SetString(string(d))
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	return r, nil
}
func Positive(d Decimal) bool    { r, e := rat(d); return e == nil && r.Sign() > 0 }
func number(r *big.Rat) Decimal  { return Decimal(r.FloatString(12)) }
func add(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func sub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func mul(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }
func div(a, b *big.Rat) *big.Rat { return new(big.Rat).Quo(a, b) }
func abs(a *big.Rat) *big.Rat    { return new(big.Rat).Abs(a) }
func min(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}
func must(d Decimal) *big.Rat {
	r, e := rat(d)
	if e != nil {
		panic(e)
	}
	return r
}

// Snapshot is a single observed book, not a prediction. Rules are retrieved
// from venue instrument metadata rather than guessed from a symbol name.
type Snapshot struct {
	Venue         string      `json:"venue"`
	Symbol        string      `json:"symbol"`
	Kind          string      `json:"kind"`
	BaseAsset     string      `json:"base_asset,omitempty"`
	QuoteAsset    string      `json:"quote_asset,omitempty"`
	ContractValue Decimal     `json:"contract_value,omitempty"`
	Bid           Decimal     `json:"bid"`
	Ask           Decimal     `json:"ask"`
	BidSize       Decimal     `json:"bid_size"`
	AskSize       Decimal     `json:"ask_size"`
	Bids          []BookLevel `json:"bids,omitempty"`
	Asks          []BookLevel `json:"asks,omitempty"`
	Tick          Decimal     `json:"tick"`
	Step          Decimal     `json:"step"`
	MinQty        Decimal     `json:"min_qty"`
	MinNotional   Decimal     `json:"min_notional"`
	Observed      time.Time   `json:"observed"`
	SourceTime    time.Time   `json:"source_time,omitempty"`
}

type BookLevel struct {
	Price    Decimal `json:"price"`
	Quantity Decimal `json:"quantity"`
}

func validateDepth(levels []BookLevel, price, quantity Decimal, descending bool) error {
	if len(levels) > 100 {
		return errors.New("order book exceeds supported depth")
	}
	for n, level := range levels {
		if !Positive(level.Price) || !Positive(level.Quantity) {
			return errors.New("invalid order book level")
		}
		if n == 0 {
			if must(level.Price).Cmp(must(price)) != 0 || must(level.Quantity).Cmp(must(quantity)) != 0 {
				return errors.New("order book depth does not match top quote")
			}
		} else {
			cmp := must(level.Price).Cmp(must(levels[n-1].Price))
			if (descending && cmp >= 0) || (!descending && cmp <= 0) {
				return errors.New("order book levels must be unique and price ordered")
			}
		}
	}
	return nil
}

func (s Snapshot) Validate(now time.Time) error {
	if s.Venue != "binance" && s.Venue != "okx" && s.Venue != "bitget" && s.Venue != "hyperliquid" {
		return errors.New("unsupported venue")
	}
	if s.Symbol == "" || (s.Kind != "spot" && s.Kind != "perpetual") {
		return errors.New("invalid instrument")
	}
	for _, d := range []Decimal{s.Bid, s.Ask, s.BidSize, s.AskSize, s.Tick, s.Step, s.MinQty, s.MinNotional} {
		if !Positive(d) {
			return errors.New("invalid market numbers")
		}
	}
	if s.ContractValue != "" && (!Positive(s.ContractValue) || s.Kind != "perpetual") {
		return errors.New("invalid contract value")
	}
	if must(s.Bid).Cmp(must(s.Ask)) > 0 {
		return errors.New("crossed book")
	}
	if err := validateDepth(s.Bids, s.Bid, s.BidSize, true); err != nil {
		return err
	}
	if err := validateDepth(s.Asks, s.Ask, s.AskSize, false); err != nil {
		return err
	}
	if s.Observed.IsZero() || now.Sub(s.Observed) > 15*time.Second || s.Observed.After(now.Add(time.Second)) {
		return errors.New("stale market snapshot")
	}
	if !s.SourceTime.IsZero() && now.Sub(s.SourceTime) > 15*time.Second {
		return errors.New("Venue order book is over 15 seconds old; cannot plan or execute with stale data")
	}
	// A small forward clock difference is distinct from stale data. The local
	// machine measured ~7s behind exchange clocks; still bound the tolerance.
	if !s.SourceTime.IsZero() && s.SourceTime.After(now.Add(10*time.Second)) {
		return errors.New("Venue time is over 10 seconds ahead of the local clock. Synchronize the clock before planning or execution.")
	}
	return nil
}

type Constraints struct {
	EntryPolicy     string  `json:"entry_policy,omitempty"`
	PlanningOnly    bool    `json:"planning_only,omitempty"`
	SizingPolicy    string  `json:"sizing_policy,omitempty"`
	Leverage        int     `json:"leverage,omitempty"`
	AvailableMargin Decimal `json:"available_margin,omitempty"`
	Equity          Decimal `json:"equity"`
	MaxRisk         Decimal `json:"max_risk"`
	MaxNotional     Decimal `json:"max_notional"`
	MinRewardRisk   Decimal `json:"min_reward_risk"`
	FeeBPS          Decimal `json:"fee_bps"`
	SlippageBPS     Decimal `json:"slippage_bps"`
}

// An omitted policy preserves the calculation and execution checks of saved plans.
const SizingAccountMargin = "account_margin"

const EntryAI500TrendLong = "ai500_trend_long"

func (c Constraints) EffectiveLeverage() int {
	if c.Leverage == 0 {
		return 1
	}
	return c.Leverage
}

func (c Constraints) Validate() error {
	if c.EntryPolicy != "" && c.EntryPolicy != EntryAI500TrendLong {
		return errors.New("unsupported entry policy")
	}
	if c.SizingPolicy != "" && c.SizingPolicy != SizingAccountMargin {
		return errors.New("unsupported sizing policy")
	}
	for _, d := range []Decimal{c.Equity, c.MaxRisk, c.MaxNotional, c.FeeBPS, c.SlippageBPS} {
		if !Positive(d) {
			return errors.New("all constraints must be positive decimal strings")
		}
	}
	minimum, err := rat(c.MinRewardRisk)
	if err != nil || minimum.Sign() < 0 {
		return errors.New("minimum reward/risk must be a non-negative decimal; 0 disables the minimum")
	}
	if c.Leverage < 0 || c.Leverage > 20 {
		return errors.New("planning leverage must be between 1x and 20x")
	}
	if c.PlanningOnly {
		available, err := rat(c.AvailableMargin)
		if c.SizingPolicy != SizingAccountMargin || err != nil || available.Sign() > 0 {
			return errors.New("equity previews require verified non-positive available margin")
		}
	} else if c.AvailableMargin != "" && (!Positive(c.AvailableMargin) || must(c.AvailableMargin).Cmp(must(c.Equity)) > 0) {
		return errors.New("available margin must be positive and cannot exceed equity")
	}
	if must(c.MaxRisk).Cmp(must(c.Equity)) > 0 || must(c.MaxNotional).Cmp(mul(must(c.Equity), big.NewRat(int64(c.EffectiveLeverage()), 1))) > 0 {
		return errors.New("risk exceeds equity or notional exceeds leveraged equity")
	}
	if must(c.FeeBPS).Cmp(big.NewRat(1000, 1)) > 0 || must(c.SlippageBPS).Cmp(big.NewRat(1000, 1)) > 0 {
		return errors.New("cost assumption exceeds 1000 bps")
	}
	return nil
}

type Idea struct {
	Name      string     `json:"name"`
	Side      string     `json:"side"`
	Entry     Decimal    `json:"entry"`
	Stop      Decimal    `json:"stop"`
	Target    Decimal    `json:"target"`
	Rationale string     `json:"rationale"`
	Execution *Execution `json:"execution,omitempty"`
}
type Plan struct {
	ID            string      `json:"id"`
	Hash          string      `json:"hash"`
	Snapshot      Snapshot    `json:"snapshot"`
	Constraints   Constraints `json:"constraints"`
	Idea          Idea        `json:"idea"`
	Quantity      Decimal     `json:"quantity"`
	SizingLimit   string      `json:"sizing_limit,omitempty"`
	VenueQuantity Decimal     `json:"venue_quantity,omitempty"`
	Risk          Decimal     `json:"risk"`
	Reward        Decimal     `json:"reward"`
	RewardRisk    Decimal     `json:"reward_risk"`
	Created       time.Time   `json:"created"`
	Expires       time.Time   `json:"expires"`
	Orders        []OrderSpec `json:"orders,omitempty"`
	Analysis      *Analysis   `json:"analysis,omitempty"`
}

func aligned(v, step *big.Rat) bool { return div(v, step).IsInt() }
func floorStep(v, step *big.Rat) *big.Rat {
	q := div(v, step)
	n := new(big.Int).Quo(q.Num(), q.Denom())
	return mul(new(big.Rat).SetInt(n), step)
}

func sizeByLimits(c Constraints, risk, budget, liquidity *big.Rat) (*big.Rat, string) {
	quantity, limit := budget, "budget"
	if risk.Cmp(quantity) < 0 {
		quantity, limit = risk, "risk"
	}
	if c.SizingPolicy != SizingAccountMargin && liquidity.Cmp(quantity) < 0 {
		quantity, limit = liquidity, "liquidity"
	}
	return quantity, limit
}
func Compile(s Snapshot, c Constraints, i Idea, now time.Time) (Plan, error) {
	if c.EntryPolicy == EntryAI500TrendLong && i.Side != "long" {
		return Plan{}, errors.New("AI500 plans are long-only. Look for a trend-following long entry or wait; short entries are not allowed.")
	}
	if i.Execution != nil {
		return compileExecution(s, c, i, now)
	}
	var p Plan
	if err := s.Validate(now); err != nil {
		return p, err
	}
	if err := c.Validate(); err != nil {
		return p, err
	}
	if i.Side != "long" && i.Side != "short" {
		return p, errors.New("side must be long or short")
	}
	if s.Kind == "spot" && c.EffectiveLeverage() != 1 {
		return p, errors.New("spot leverage is unsupported")
	}
	if s.Kind == "spot" && i.Side == "short" {
		return p, errors.New("spot short requires inventory/margin; unsupported")
	}
	for _, d := range []Decimal{i.Entry, i.Stop, i.Target} {
		if !Positive(d) {
			return p, errors.New("invalid idea price")
		}
		if !aligned(must(d), must(s.Tick)) {
			return p, errors.New("price violates tick size")
		}
	}
	e, st, tp := must(i.Entry), must(i.Stop), must(i.Target)
	if (i.Side == "long" && (st.Cmp(e) >= 0 || tp.Cmp(e) <= 0)) || (i.Side == "short" && (st.Cmp(e) <= 0 || tp.Cmp(e) >= 0)) {
		return p, errors.New("invalid stop/target direction")
	}
	mid := div(add(must(s.Bid), must(s.Ask)), big.NewRat(2, 1))
	if div(abs(sub(e, mid)), mid).Cmp(big.NewRat(1, 10)) > 0 {
		return p, errors.New("entry more than 10% from book")
	}
	cost := div(add(must(c.FeeBPS), must(c.SlippageBPS)), big.NewRat(10000, 1))
	loss := add(abs(sub(e, st)), mul(add(e, st), cost))
	if c.EffectiveLeverage() > 1 && mul(loss, big.NewRat(int64(c.EffectiveLeverage()), 1)).Cmp(mul(e, big.NewRat(4, 5))) >= 0 {
		return p, errors.New("stop and costs consume over 80% of initial margin at this leverage; reduce leverage")
	}
	gain := sub(abs(sub(tp, e)), mul(add(e, tp), cost))
	if gain.Sign() <= 0 {
		return p, errors.New("target return does not cover estimated fees and slippage")
	}
	if must(c.MinRewardRisk).Sign() > 0 && div(gain, loss).Cmp(must(c.MinRewardRisk)) < 0 {
		return p, fmt.Errorf("Net reward/risk %s is below minimum %s after fees and slippage; per-unit risk %s, net reward %s", number(div(gain, loss)), c.MinRewardRisk, number(loss), number(gain))
	}
	// Keep the former top-level cap only for previously signed plans. New account
	// sizing uses margin; market execution checks depth without resizing the order.
	liquidity := must(s.AskSize)
	if i.Side == "short" {
		liquidity = must(s.BidSize)
	}
	size, limit := sizeByLimits(c, div(must(c.MaxRisk), loss), div(must(c.MaxNotional), mul(e, add(big.NewRat(1, 1), cost))), div(liquidity, big.NewRat(10, 1)))
	q := floorStep(size, must(s.Step))
	if q.Cmp(must(s.MinQty)) < 0 || mul(q, e).Cmp(must(s.MinNotional)) < 0 {
		if c.SizingPolicy == SizingAccountMargin {
			return p, fmt.Errorf("Available margin and risk budget allow quantity %s (notional %s), below venue minimum quantity %s or notional %s", number(q), number(mul(q, e)), s.MinQty, s.MinNotional)
		}
		return p, fmt.Errorf("Risk, notional and liquidity constraints allow quantity %s (notional %s), below venue minimum quantity %s or notional %s. Top-of-book side size is %s; participation is capped at 10%%. Changing the target cannot remove liquidity or minimum-order limits.", number(q), number(mul(q, e)), s.MinQty, s.MinNotional, number(liquidity))
	}
	p = Plan{Snapshot: s, Constraints: c, Idea: i, Quantity: number(q), Risk: number(mul(q, loss)), Reward: number(mul(q, gain)), RewardRisk: number(div(gain, loss)), Created: now, Expires: now.Add(60 * time.Second)}
	p.SizingLimit = limit
	for _, d := range []Decimal{p.Quantity, p.Risk, p.Reward, p.RewardRisk} {
		if !Positive(d) {
			return Plan{}, errors.New("derived amount exceeds supported decimal magnitude/precision")
		}
	}
	if s.ContractValue != "" {
		p.VenueQuantity = number(div(must(p.Quantity), must(s.ContractValue)))
	}
	p.Hash = p.Digest()
	p.ID = p.Hash[:16]
	return p, nil
}
func (p Plan) Digest() string {
	p.ID = ""
	p.Hash = ""
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (p Plan) Verify(now time.Time) error {
	if p.Hash != p.Digest() || p.ID != p.Hash[:16] {
		return errors.New("plan integrity failure")
	}
	if !now.Before(p.Expires) {
		return errors.New("plan expired; replan and approve again")
	}
	rebuilt, err := Compile(p.Snapshot, p.Constraints, p.Idea, p.Created)
	if err != nil {
		return err
	}
	if p.SizingLimit == "" {
		// Preserve the exact approved digest of plans saved before sizing labels.
		rebuilt.SizingLimit = ""
		rebuilt.Hash = rebuilt.Digest()
		rebuilt.ID = rebuilt.Hash[:16]
	}
	rebuilt, err = rebuilt.WithAnalysis(p.Analysis)
	if err != nil {
		return err
	}
	if rebuilt.Hash != p.Hash {
		return errors.New("plan calculation mismatch")
	}
	return nil
}

// Price rounds down to a valid price tick for deterministic demonstration ideas.
func Price(d Decimal, factor float64, tick Decimal) Decimal {
	if math.IsNaN(factor) || math.IsInf(factor, 0) {
		return "0"
	}
	f := must(Decimal(strconv.FormatFloat(factor, 'f', 8, 64)))
	return number(floorStep(mul(must(d), f), must(tick)))
}
func CheckBook(p Plan, s Snapshot, now time.Time) error {
	if err := s.Validate(now); err != nil {
		return err
	}
	a := p.Snapshot
	if s.Venue != a.Venue || s.Symbol != a.Symbol || s.Kind != a.Kind || s.Tick != a.Tick || s.Step != a.Step || s.MinQty != a.MinQty || s.MinNotional != a.MinNotional || s.ContractValue != a.ContractValue {
		return errors.New("instrument rules changed; replan")
	}
	quote, size := s.Ask, s.AskSize
	if p.Idea.Side == "short" {
		quote, size = s.Bid, s.BidSize
	}
	if div(abs(sub(must(quote), must(originalQuote(p)))), must(originalQuote(p))).Cmp(div(must(p.Constraints.SlippageBPS), big.NewRat(10000, 1))) > 0 {
		return fmt.Errorf("market moved beyond approved slippage; replan")
	}
	if p.Constraints.SizingPolicy == SizingAccountMargin {
		return checkMarketDepth(p, s)
	}
	if must(p.Quantity).Cmp(div(must(size), big.NewRat(10, 1))) > 0 {
		return errors.New("insufficient visible liquidity")
	}
	return nil
}

// Limit baskets may rest or fill partially. A market entry must have enough
// observed depth inside its approved price bound; never silently reduce its size.
func checkMarketDepth(p Plan, s Snapshot) error {
	if p.Idea.Execution == nil || p.Idea.Execution.Kind != "market" {
		return nil
	}
	if len(p.Orders) != 1 || !Positive(p.Orders[0].Price) || !Positive(p.Quantity) {
		return errors.New("invalid market order specification")
	}
	levels := s.Asks
	if len(levels) == 0 {
		levels = []BookLevel{{s.Ask, s.AskSize}}
	}
	if p.Idea.Side == "short" {
		levels = s.Bids
		if len(levels) == 0 {
			levels = []BookLevel{{s.Bid, s.BidSize}}
		}
	}
	available := new(big.Rat)
	bound := must(p.Orders[0].Price)
	for _, level := range levels {
		cmp := must(level.Price).Cmp(bound)
		if (p.Idea.Side == "long" && cmp > 0) || (p.Idea.Side == "short" && cmp < 0) {
			break
		}
		available.Add(available, must(level.Quantity))
		if available.Cmp(must(p.Quantity)) >= 0 {
			return nil
		}
	}
	return fmt.Errorf("Insufficient order-book depth within the approved market price bound: need %s, available %s. Size was not reduced; refresh the plan or choose a limit or split entry", p.Quantity, number(available))
}

func originalQuote(p Plan) Decimal {
	if p.Idea.Side == "short" {
		return p.Snapshot.Bid
	}
	return p.Snapshot.Ask
}
