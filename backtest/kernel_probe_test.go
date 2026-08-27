package backtest

import (
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/mcp"
	"nofx/provider/vergex"
	"nofx/store"
)

// fakeAIResponse is a minimal valid decisions JSON for the kernel parser.
const fakeAIResponse = `<reasoning>probe reasoning</reasoning>
<decision>
[
  {"symbol":"BTCUSDT","action":"hold","confidence":50,"reasoning":"probe"}
]
</decision>`

// ProbeAIClient implements mcp.AIClient with a fixed response.
type ProbeAIClient struct {
	Response string
	Calls    int
}

func (f *ProbeAIClient) SetAPIKey(string, string, string) {}
func (f *ProbeAIClient) SetTimeout(time.Duration)         {}
func (f *ProbeAIClient) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	f.Calls++
	return f.Response, nil
}
func (f *ProbeAIClient) CallWithRequest(req *mcp.Request) (string, error) {
	f.Calls++
	return f.Response, nil
}
func (f *ProbeAIClient) CallWithRequestStream(req *mcp.Request, onChunk func(string)) (string, error) {
	f.Calls++
	onChunk(f.Response)
	return f.Response, nil
}
func (f *ProbeAIClient) CallWithRequestFull(req *mcp.Request) (*mcp.LLMResponse, error) {
	f.Calls++
	return &mcp.LLMResponse{Content: f.Response}, nil
}

// TestKernelProbe verifies the P3.3 adaptation surface before the
// runner exists: NewStrategyEngine(config, "") and
// GetFullDecisionWithStrategy(ctx, client, engine, "") must not panic
// with the backtest-built context, and missing context fields
// (VergexDataMap etc.) must stay nil-safe.
func TestKernelProbe(t *testing.T) {
	def := store.GetDefaultStrategyConfig("en")
	engine := kernel.NewStrategyEngine(&def, "")
	if engine == nil {
		t.Fatal("NewStrategyEngine returned nil")
	}
	cfg := engine.GetConfig()
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}

	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	// A single bar so the token estimate stays tiny.
	bars := []market.Kline{
		{OpenTime: now.Add(-5 * time.Minute).UnixMilli(), Open: 100, High: 101, Low: 99, Close: 100, Volume: 100},
	}
	data, err := market.BuildDataFromKlines("BTCUSDT", "5m", bars, nil)
	if err != nil {
		t.Fatalf("BuildDataFromKlines: %v", err)
	}
	if data == nil || data.CurrentPrice != 100 || data.TimeframeData["5m"] == nil {
		t.Fatalf("BuildDataFromKlines: %+v", data)
	}

	ctx := &kernel.Context{
		CurrentTime:     now.Format("2006-01-02 15:04:05 UTC"),
		CallCount:       1,
		BTCETHLeverage:  3,
		AltcoinLeverage: 2,
		Account: kernel.AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
			PositionCount:    0,
		},
		Positions:      []kernel.PositionInfo{},
		CandidateCoins: []kernel.CandidateCoin{{Symbol: "BTCUSDT"}},
		MarketDataMap:  map[string]*market.Data{"BTCUSDT": data},
		Timeframes:     []string{"5m"},
		// nil VergexDataMap / OITopDataMap: engine must not panic; with
		// SourceType static the vergex enrichment path is skipped, and
		// OITopDataMap is lazily initialized inside the engine.
	}

	client := &ProbeAIClient{Response: fakeAIResponse}
	fd, err := kernel.GetFullDecisionWithStrategy(ctx, client, engine, "")
	if err != nil {
		t.Fatalf("GetFullDecisionWithStrategy: %v", err)
	}
	if fd == nil {
		t.Fatal("full decision is nil")
	}
	if client.Calls != 1 {
		t.Fatalf("AI client calls = %d, want 1", client.Calls)
	}
	if len(fd.Decisions) != 1 || fd.Decisions[0].Symbol != "BTCUSDT" {
		t.Fatalf("decisions = %+v", fd.Decisions)
	}
	if fd.SystemPrompt == "" || fd.UserPrompt == "" {
		t.Fatalf("prompts empty: sys=%q user=%q", fd.SystemPrompt, fd.UserPrompt)
	}
}

// TestKernelProbeVergexSource verifies the vergex signal path skips the
// network when VergexDataMap is pre-set to a non-nil empty map.
func TestKernelProbeVergexSource(t *testing.T) {
	def := store.GetDefaultStrategyConfig("en")
	engine := kernel.NewStrategyEngine(&def, "")
	cfg := engine.GetConfig()
	cfg.CoinSource.SourceType = "vergex_signal"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}

	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	bars := []market.Kline{
		{OpenTime: now.Add(-5 * time.Minute).UnixMilli(), Open: 100, High: 101, Low: 99, Close: 100, Volume: 100},
	}
	data, err := market.BuildDataFromKlines("BTCUSDT", "5m", bars, nil)
	if err != nil {
		t.Fatalf("BuildDataFromKlines: %v", err)
	}
	ctx := &kernel.Context{
		CurrentTime:     now.Format("2006-01-02 15:04:05 UTC"),
		CallCount:       1,
		BTCETHLeverage:  3,
		AltcoinLeverage: 2,
		Account:         kernel.AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
		Positions:       []kernel.PositionInfo{},
		CandidateCoins:  []kernel.CandidateCoin{{Symbol: "BTCUSDT"}},
		MarketDataMap:   map[string]*market.Data{"BTCUSDT": data},
		// Setting VergexDataMap to a non-nil empty map short-circuits
		// enrichVergexDataWithStrategy (no network).
		VergexDataMap: make(map[string]*vergex.MarketAnalysis),
		Timeframes:    []string{"5m"},
	}

	client := &ProbeAIClient{Response: fakeAIResponse}
	fd, err := kernel.GetFullDecisionWithStrategy(ctx, client, engine, "")
	if err != nil {
		t.Fatalf("GetFullDecisionWithStrategy: %v", err)
	}
	if fd == nil || len(fd.Decisions) != 1 {
		t.Fatalf("fd = %+v", fd)
	}
}
