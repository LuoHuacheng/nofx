// Package automation turns a model reply into a trade signal that carries no
// execution authority: sizing, leverage, accounts and constraints stay server-side.
package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/planning/core"
	"nofx/planning/live"
	"nofx/planning/market"
	"nofx/planning/model"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

const (
	SignalBarInterval    = "15m"
	SignalBarSeconds     = int64(900)
	SignalBars           = 120
	SignalExpiry         = 30 * time.Second
	SignalSnapshotMaxAge = 5 * time.Second
	SignalMaxReplyBytes  = 64 * 1024
)

// ErrNoSignal means the model deliberately proposed no trade. It is never an
// error to recover from by inventing one.
var ErrNoSignal = errors.New("no trade signal")

type Signal struct {
	ID        string
	Symbol    string
	Idea      core.Idea
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (s Signal) Validate(now time.Time) error {
	if s.CreatedAt.IsZero() || s.ExpiresAt.IsZero() || s.CreatedAt.After(now.Add(time.Second)) || now.Sub(s.CreatedAt) > SignalExpiry || !now.Before(s.ExpiresAt) || !s.ExpiresAt.After(s.CreatedAt) || s.ExpiresAt.After(s.CreatedAt.Add(SignalExpiry)) {
		return errors.New("automation: signal is expired or has invalid timestamps")
	}
	return nil
}

type IdeaSource interface {
	Next(context.Context, risk.Policy) (Signal, error)
}

type SourceMarket interface {
	Snapshot(context.Context, string, string) (core.Snapshot, error)
	Candles(context.Context, market.CandleQuery) ([]market.Candle, error)
}

// ModelSource asks a model for at most one limit-entry idea on one whitelisted
// symbol per call, using closed bars only.
type ModelSource struct {
	mu      sync.Mutex
	planner model.ChatPlanner
	feed    SourceMarket
	now     func() time.Time
	cursor  int
}

func NewModelSource(planner model.ChatPlanner, feed SourceMarket, now func() time.Time) *ModelSource {
	if now == nil {
		now = time.Now
	}
	return &ModelSource{planner: planner, feed: feed, now: now}
}

func (s *ModelSource) Next(ctx context.Context, policy risk.Policy) (Signal, error) {
	// Accounts of one user share the model client and symbol cursor.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	if len(policy.Symbols) == 0 {
		return Signal{}, errors.New("automation: policy requires a non-empty symbol whitelist")
	}
	if !policy.Environment.Valid() {
		return Signal{}, errors.New("automation: policy environment must be sandbox or live")
	}
	if policy.Version == "" {
		return Signal{}, errors.New("automation: policy version is required to bind a signal")
	}
	if s.cursor >= len(policy.Symbols) {
		s.cursor = 0
	}
	symbol := policy.Symbols[s.cursor]
	s.cursor = (s.cursor + 1) % len(policy.Symbols)

	book, err := s.feed.Snapshot(ctx, policy.Venue, symbol)
	if err != nil {
		return Signal{}, fmt.Errorf("automation: market snapshot unavailable: %w", err)
	}
	// Sample the clock after the snapshot: builders stamp Observed at build
	// time, so a pre-call clock would reject every real snapshot as future-dated.
	now := s.now()
	if age := now.Sub(book.Observed); age > SignalSnapshotMaxAge || book.Observed.After(now.Add(time.Second)) {
		return Signal{}, errors.New("automation: market snapshot is stale")
	}
	rows, err := s.feed.Candles(ctx, market.CandleQuery{
		Venue: policy.Venue, Symbol: symbol, Interval: SignalBarInterval, Limit: SignalBars + 1,
	})
	if err != nil {
		return Signal{}, fmt.Errorf("automation: candles unavailable: %w", err)
	}
	bars, err := closedBars(rows, now)
	if err != nil {
		return Signal{}, err
	}
	// Fetches can cross a boundary; never pay for a bar outside the checkpoint.
	if expected, ok := ctx.Value(signalBarCloseKey{}).(time.Time); ok && bars[len(bars)-1].Time+SignalBarSeconds != expected.Unix() {
		return Signal{}, errors.New("automation: scheduled candle changed before generation")
	}

	reply, err := s.planner.Chat(ctx, signalPrompt(policy, symbol, book, bars), []model.Tool{})
	if err != nil {
		return Signal{}, fmt.Errorf("automation: model chat failed: %w", err)
	}
	if len(reply.ToolCalls) != 0 {
		return Signal{}, errors.New("automation: model attempted to call a tool")
	}
	content := strings.TrimSpace(reply.Content)
	if content == "" {
		return Signal{}, errors.New("automation: model reply is empty")
	}
	if len(content) > SignalMaxReplyBytes {
		return Signal{}, errors.New("automation: model reply exceeds the size limit")
	}
	ideas, err := decodeIdeas(content)
	if err != nil {
		return Signal{}, err
	}
	if len(ideas) == 0 {
		return Signal{}, ErrNoSignal
	}
	idea := ideas[0]
	if err := checkIdea(&idea); err != nil {
		return Signal{}, err
	}

	raw, err := json.Marshal(idea)
	if err != nil {
		return Signal{}, errors.New("automation: idea is not encodable")
	}
	generatedAt := s.now()
	return Signal{
		ID:        signalID(policy.AccountKey, policy.Version, symbol, bars[len(bars)-1].Time, string(raw)),
		Symbol:    symbol,
		Idea:      idea,
		CreatedAt: generatedAt,
		ExpiresAt: generatedAt.Add(SignalExpiry),
	}, nil
}

// closedBars keeps only bars whose interval has ended. A short history is an
// error: fabricating bars would invent market context the model never saw.
func closedBars(rows []market.Candle, now time.Time) ([]market.Candle, error) {
	normalized, err := market.NormalizeCandles(rows, now)
	if err != nil {
		return nil, fmt.Errorf("automation: invalid candles: %w", err)
	}
	closed := make([]market.Candle, 0, len(normalized))
	for _, bar := range normalized {
		if bar.Time+SignalBarSeconds > now.Unix() {
			continue
		}
		closed = append(closed, bar)
	}
	if len(closed) < SignalBars {
		return nil, errors.New("automation: insufficient closed 15m candles")
	}
	closed = closed[len(closed)-SignalBars:]
	lastClose := closed[len(closed)-1].Time + SignalBarSeconds
	if now.Unix()-lastClose > 2*SignalBarSeconds {
		return nil, errors.New("automation: candles are stale")
	}
	if lastClose != now.Unix()/SignalBarSeconds*SignalBarSeconds {
		return nil, errors.New("automation: latest closed 15m candle is not available")
	}
	return closed, nil
}

func decodeIdeas(content string) ([]core.Idea, error) {
	var reply struct {
		Ideas []core.Idea `json:"ideas"`
	}
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("automation: invalid model reply: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("automation: model reply has trailing JSON")
	}
	if reply.Ideas == nil {
		return nil, errors.New("automation: model reply must contain an ideas array")
	}
	if len(reply.Ideas) > 1 {
		return nil, errors.New("automation: model replied with more than one idea")
	}
	return reply.Ideas, nil
}

func checkIdea(idea *core.Idea) error {
	if idea.Side != "long" && idea.Side != "short" {
		return errors.New("automation: model side must be long or short")
	}
	for _, price := range []struct {
		name  string
		value core.Decimal
	}{{"entry", idea.Entry}, {"stop", idea.Stop}, {"target", idea.Target}} {
		r, err := execution.Number(string(price.value))
		if err != nil || r.Sign() <= 0 {
			return fmt.Errorf("automation: model %s must be a positive decimal", price.name)
		}
	}
	if len(idea.Name) > 400 || len(idea.Rationale) > 400 {
		return errors.New("automation: model idea text is too long")
	}
	if idea.Execution == nil {
		idea.Execution = &core.Execution{Kind: "limit"}
	}
	if x := idea.Execution; x.Kind != "limit" || len(x.Levels) != 0 || x.Slices != 0 || x.IntervalSeconds != 0 {
		return errors.New("automation: model may only request a limit entry")
	}
	return nil
}

// signalID binds a signal to its account, policy version, symbol and closed
// bar, so the same idea on the same bar is idempotent across replays.
func signalID(accountKey, policyVersion, symbol string, barTime int64, idea string) string {
	sum := sha256.Sum256([]byte(accountKey + "\n" + policyVersion + "\n" + symbol + "\n" + strconv.FormatInt(barTime, 10) + "\n" + idea))
	return hex.EncodeToString(sum[:])
}

func signalPrompt(policy risk.Policy, symbol string, book core.Snapshot, bars []market.Candle) []model.Message {
	var b strings.Builder
	b.WriteString("You are a restricted signal generator. You cannot choose size, leverage, account or risk limits.\n")
	fmt.Fprintf(&b, "Venue: %s\nSymbol: %s\n", policy.Venue, symbol)
	b.WriteString("This turn covers exactly this symbol; no other symbol may be signalled.\n")
	fmt.Fprintf(&b, "Current book: bid=%s ask=%s tick_size=%s. Prices must be multiples of tick_size.\n", book.Bid, book.Ask, book.Tick)
	fmt.Fprintf(&b, "Server cost assumptions: fee_bps=%s per leg, slippage_bps=%s per leg, funding_reserve_bps=%d, min_reward_risk=%s.\n", live.ManagedFeeBPS, live.ManagedSlippageBPS, policy.FundingReserveBPS, policy.MinRewardRisk)
	b.WriteString("Net costs: cost=(fee_bps+slippage_bps)/10000; funding=entry*funding_reserve_bps/10000; loss=abs(entry-stop)+(entry+stop)*cost+funding; reward=abs(target-entry)-(entry+target)*cost-funding. Require reward>0 and reward/loss>=min_reward_risk after all costs, not gross price distance.\n")
	fmt.Fprintf(&b, "Limit entries expire after %d seconds; maximum holding time is %d hours.\n", int(EntryFillWindow/time.Second), int(MaxHold/time.Hour))
	b.WriteString("Only propose chart-supported entry, stop and target levels compatible with the current book. Do not invent or move targets farther away just to pass a constraint. If no such setup is supported, return {\"ideas\":[]}; no trade is a healthy outcome.\n")
	b.WriteString("Closed 15m candles (time,open,high,low,close,volume), oldest first:\n")
	for _, bar := range bars {
		fmt.Fprintf(&b, "%d,%s,%s,%s,%s,%s\n", bar.Time, bar.Open, bar.High, bar.Low, bar.Close, bar.Volume)
	}
	b.WriteString(`Reply with exactly one JSON object holding an "ideas" array of at most one idea.
An idea is {"name","side":"long|short","entry","stop","target","rationale","execution":{"kind":"limit"}}.
Prices are decimal strings. Never include quantity, leverage, account or constraints: the server sizes every order.
Return {"ideas":[]} when there is no trade.`)
	return []model.Message{{Role: "system", Content: b.String()}}
}
