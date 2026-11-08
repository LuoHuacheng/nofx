package automation

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"nofx/planning/core"
	"nofx/planning/market"
	"nofx/planning/model"
	"nofx/trader/execution"
	"nofx/trading/risk"
)

// signalTestNow sits exactly on a 15m boundary, so the last closed bar is
// signalTestNow-900s and any bar starting at signalTestNow is still open.
var signalTestNow = time.Unix(1_760_000_400, 0)

const testIdeaJSON = `"name":"x","side":"long","entry":"100","stop":"99","target":"102","rationale":"r"`

type stubPlanner struct {
	reply model.Message
	err   error
	calls int
	last  []model.Message
	tools []model.Tool
}

func (p *stubPlanner) Chat(_ context.Context, messages []model.Message, tools []model.Tool) (model.Message, error) {
	p.calls++
	p.last = messages
	p.tools = tools
	return p.reply, p.err
}

type delayedPlanner struct {
	stubPlanner
	clock *time.Time
}

func (p *delayedPlanner) Chat(ctx context.Context, messages []model.Message, tools []model.Tool) (model.Message, error) {
	reply, err := p.stubPlanner.Chat(ctx, messages, tools)
	*p.clock = p.clock.Add(45 * time.Second)
	return reply, err
}

func TestModelSourceStampsCompletedGeneration(t *testing.T) {
	clock := signalTestNow
	planner := &delayedPlanner{clock: &clock, stubPlanner: stubPlanner{reply: model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}}}
	feed := &stubMarket{book: testBook(clock), rows: testCandles(clock.Unix()-SignalBarSeconds, SignalBars)}
	source := NewModelSource(planner, feed, func() time.Time { return clock })
	signal, err := source.Next(context.Background(), testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := signal.Validate(clock); err != nil {
		t.Fatalf("newly generated signal is already expired: %v", err)
	}
	if signal.CreatedAt != clock || signal.ExpiresAt != clock.Add(30*time.Second) {
		t.Fatal("signal validity must start when generation finishes")
	}
}

type stubMarket struct {
	book   core.Snapshot
	rows   []market.Candle
	err    error
	calls  int
	query  market.CandleQuery
	symbol string
	// skew shifts the book timestamp, imitating a builder that stamps Observed
	// after the caller sampled its clock.
	skew time.Duration
}

func (m *stubMarket) Snapshot(_ context.Context, _, symbol string) (core.Snapshot, error) {
	m.symbol = symbol
	book := m.book
	book.Observed = book.Observed.Add(m.skew)
	return book, nil
}

func (m *stubMarket) Candles(_ context.Context, q market.CandleQuery) ([]market.Candle, error) {
	m.calls++
	m.query = q
	return m.rows, m.err
}

func testPolicy() risk.Policy {
	return risk.Policy{
		AccountKey:  "binance:main",
		Version:     "v1",
		Venue:       "binance",
		Environment: execution.EnvironmentSandbox,
		Symbols:     []string{"BTCUSDT"},
	}
}

func testBook(observed time.Time) core.Snapshot {
	return core.Snapshot{
		Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual",
		Bid: "100", Ask: "100.1", BidSize: "1", AskSize: "1",
		Tick: "0.1", Step: "0.001", MinQty: "0.001", MinNotional: "5",
		Observed: observed,
	}
}

// testCandles returns n identical but timestamped bars ending at last.
func testCandles(last int64, n int) []market.Candle {
	rows := make([]market.Candle, n)
	for i := range rows {
		rows[i] = market.Candle{
			Time: last - int64(n-1-i)*SignalBarSeconds,
			Open: "100", High: "101", Low: "99", Close: "100", Volume: "1",
		}
	}
	return rows
}

func testSource(reply model.Message, rows []market.Candle) (*ModelSource, *stubPlanner, *stubMarket) {
	planner := &stubPlanner{reply: reply}
	feed := &stubMarket{book: testBook(signalTestNow), rows: rows}
	return NewModelSource(planner, feed, func() time.Time { return signalTestNow }), planner, feed
}

func TestModelSourceStrictBoundary(t *testing.T) {
	cases := []struct {
		name     string
		reply    model.Message
		observed time.Time
		want     string
	}{
		{"unknown idea field", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `,"quantity":"1"}]}`}, time.Time{}, ""},
		{"unknown top-level field", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}],"leverage":3}`}, time.Time{}, ""},
		{"two ideas", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `},{` + testIdeaJSON + `}]}`}, time.Time{}, ""},
		{"tool call", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`, ToolCalls: []model.ToolCall{{ID: "c1", Type: "function"}}}, time.Time{}, ""},
		{"empty content", model.Message{}, time.Time{}, ""},
		{"trailing json", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}{"ideas":[]}`}, time.Time{}, ""},
		{"invalid json", model.Message{Content: `{"ideas":[`}, time.Time{}, ""},
		{"market entry", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `,"execution":{"kind":"market"}}]}`}, time.Time{}, "may only request a limit entry"},
		{"ladder entry", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `,"execution":{"kind":"limit","levels":[{"price":"100","weight":"1"}]}}]}`}, time.Time{}, "may only request a limit entry"},
		{"stale snapshot", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}, signalTestNow.Add(-10 * time.Second), "market snapshot is stale"},
		{"future snapshot", model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}, signalTestNow.Add(10 * time.Minute), "market snapshot is stale"},
	}
	var injectedQuantityErr error
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars)
			source, _, feed := testSource(c.reply, rows)
			if !c.observed.IsZero() {
				feed.book = testBook(c.observed)
			}
			sig, err := source.Next(context.Background(), testPolicy())
			if err == nil {
				t.Fatal("model input outside the whitelist was accepted")
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to mention %q", err, c.want)
			}
			if sig != (Signal{}) {
				t.Fatal("a rejected reply produced a signal")
			}
			if c.name == "unknown idea field" {
				injectedQuantityErr = err
			}
		})
	}
	if injectedQuantityErr == nil {
		t.Fatal("model changed execution authority")
	}
}

// Real snapshot builders stamp Observed at build time, i.e. after the caller
// sampled its clock. That must be accepted; only real staleness or gross skew
// is rejected.
func TestModelSourceSnapshotClockOrder(t *testing.T) {
	rows := testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars)
	source, _, feed := testSource(model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}, rows)
	feed.skew = 200 * time.Millisecond
	if _, err := source.Next(context.Background(), testPolicy()); err != nil {
		t.Fatalf("a snapshot stamped just after the clock was rejected: %v", err)
	}
}

func TestModelSourceNoTrade(t *testing.T) {
	rows := testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars)
	source, _, _ := testSource(model.Message{Content: `{"ideas":[]}`}, rows)
	sig, noIdeasErr := source.Next(context.Background(), testPolicy())
	if !errors.Is(noIdeasErr, ErrNoSignal) {
		t.Fatal("empty ideas produced a trade")
	}
	if sig != (Signal{}) {
		t.Fatal("empty ideas produced a trade")
	}
}

// A policy edit can shrink the whitelist between calls; the cursor must not
// index past the new list.
func TestModelSourceSymbolListShrink(t *testing.T) {
	rows := testCandles(signalTestNow.Unix()-SignalBarSeconds, SignalBars)
	source, planner, feed := testSource(model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}, rows)
	wide := testPolicy()
	wide.Symbols = []string{"AAAUSDT", "BBBUSDT", "CCCUSDT"}
	if _, err := source.Next(context.Background(), wide); err != nil {
		t.Fatal(err)
	}
	narrow := testPolicy()
	narrow.Symbols = []string{"DDDUSDT"}
	sig, err := source.Next(context.Background(), narrow)
	if err != nil {
		t.Fatal(err)
	}
	if feed.symbol != "DDDUSDT" {
		t.Fatalf("shrunk whitelist signalled %q", feed.symbol)
	}
	if sig.ID == "" {
		t.Fatal("shrunk whitelist produced no signal id")
	}
	for _, message := range planner.last {
		if !strings.Contains(message.Content, "Symbol: DDDUSDT") {
			t.Fatalf("model saw a symbol outside the new whitelist: %s", message.Content)
		}
	}
}

func TestModelSourceStaleCandles(t *testing.T) {
	// The last closed bar ended 3*SignalBarSeconds ago: too old to trade on.
	rows := testCandles(signalTestNow.Unix()-4*SignalBarSeconds, SignalBars)
	source, planner, _ := testSource(model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}, rows)
	if _, err := source.Next(context.Background(), testPolicy()); err == nil || !strings.Contains(err.Error(), "candles are stale") {
		t.Fatalf("stale candle error = %v", err)
	}
	if planner.calls != 0 {
		t.Fatal("the model was called on a stale candle window")
	}
}

func TestModelSourceStableID(t *testing.T) {
	rows := testCandles(signalTestNow.Unix()-SignalBarSeconds, 121)
	planner := &stubPlanner{reply: model.Message{Content: `{"ideas":[{` + testIdeaJSON + `}]}`}}
	feed := &stubMarket{book: testBook(signalTestNow), rows: rows}
	clock := signalTestNow
	source := NewModelSource(planner, feed, func() time.Time { return clock })

	first, err := source.Next(context.Background(), testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	clock = signalTestNow.Add(2 * time.Second)
	second, err := source.Next(context.Background(), testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.ID != second.ID {
		t.Fatalf("same bar and idea produced ids %q and %q", first.ID, second.ID)
	}
	if first.CreatedAt != signalTestNow || first.ExpiresAt != signalTestNow.Add(SignalExpiry) {
		t.Fatalf("server did not stamp the signal: %v/%v", first.CreatedAt, first.ExpiresAt)
	}
	if second.CreatedAt != clock || second.ExpiresAt != clock.Add(SignalExpiry) {
		t.Fatalf("server did not stamp the second signal: %v/%v", second.CreatedAt, second.ExpiresAt)
	}
	if planner.tools == nil || len(planner.tools) != 0 {
		t.Fatal("the model was offered trading tools")
	}
	// The id must bind every input, not just the idea text.
	if signalID("binance:main", "v1", "BTCUSDT", 900, "i") == signalID("binance:other", "v1", "BTCUSDT", 900, "i") ||
		signalID("binance:main", "v1", "BTCUSDT", 900, "i") == signalID("binance:main", "v2", "BTCUSDT", 900, "i") ||
		signalID("binance:main", "v1", "BTCUSDT", 900, "i") == signalID("binance:main", "v1", "ETHUSDT", 900, "i") ||
		signalID("binance:main", "v1", "BTCUSDT", 900, "i") == signalID("binance:main", "v1", "BTCUSDT", 1800, "i") ||
		signalID("binance:main", "v1", "BTCUSDT", 900, "i") == signalID("binance:main", "v1", "BTCUSDT", 900, "j") {
		t.Fatal("signal id does not bind account, policy, symbol, bar and idea")
	}
	typ := reflect.TypeOf(Signal{})
	want := []string{"ID", "Symbol", "Idea", "CreatedAt", "ExpiresAt"}
	if typ.NumField() != len(want) {
		t.Fatalf("Signal carries %d fields", typ.NumField())
	}
	for i, name := range want {
		if typ.Field(i).Name != name {
			t.Fatalf("Signal field %d is %s, not %s", i, typ.Field(i).Name, name)
		}
	}
}

var candleLine = regexp.MustCompile(`^\d{10},\S+,\S+,\S+,\S+,\S+$`)

func TestModelSourceClosedBarsOnly(t *testing.T) {
	rows := testCandles(signalTestNow.Unix(), 121) // the last bar starts at now: still open
	source, planner, feed := testSource(model.Message{Content: `{"ideas":[{"name":"x","side":"long","entry":"100","stop":"99","target":"102","rationale":"r","execution":{"kind":"limit"}}]}`}, rows)
	if _, err := source.Next(context.Background(), testPolicy()); err != nil {
		t.Fatal(err)
	}
	if feed.query.Interval != SignalBarInterval || feed.query.Limit != SignalBars+1 {
		t.Fatalf("candle query = %s/%d", feed.query.Interval, feed.query.Limit)
	}
	var prompt []string
	for _, message := range planner.last {
		prompt = append(prompt, strings.Split(message.Content, "\n")...)
	}
	closed := 0
	for _, line := range prompt {
		if candleLine.MatchString(line) {
			closed++
		}
		if strings.Contains(line, strconv.FormatInt(signalTestNow.Unix(), 10)) {
			t.Fatal("an open candle entered the model input")
		}
	}
	if closed != SignalBars {
		t.Fatalf("prompt carried %d closed candles, want %d", closed, SignalBars)
	}

	short := testCandles(signalTestNow.Unix()-SignalBarSeconds, 100)
	source, planner, _ = testSource(model.Message{Content: `{"ideas":[]}`}, short)
	if _, err := source.Next(context.Background(), testPolicy()); err == nil || !strings.Contains(err.Error(), "insufficient closed 15m candles") {
		t.Fatalf("short candle history error = %v", err)
	}
	if planner.calls != 0 {
		t.Fatal("the model was called without a full closed-bar window")
	}
}
