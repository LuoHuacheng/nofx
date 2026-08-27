package backtest

import (
	"encoding/json"
	"fmt"
	"time"

	"nofx/mcp"

	// Register all provider factories (deepseek/qwen/claw402/...) so the
	// registry works in tests and in the server binary alike.
	_ "nofx/mcp/payment"
	_ "nofx/mcp/provider"
)

// ModelConfigSnapshot is the JSON snapshot of the AI model config used
// by a backtest run (stored in BacktestRun.ModelConfigJSON, §P3.2). The
// API handler builds it from the user's AIModel record at start time so
// the run is reproducible.
type ModelConfigSnapshot struct {
	ModelID         string `json:"model_id,omitempty"` // store record id; API key is resolved from it at run time
	Provider        string `json:"provider"`
	Model           string `json:"model"`              // custom model name (custom provider)
	APIKey          string `json:"api_key,omitempty"`  // transient: never persisted with the run
	CustomAPIURL    string `json:"custom_api_url,omitempty"`
	CustomModelName string `json:"custom_model_name,omitempty"`
}

// MarshalModelSnapshot serializes a snapshot for storage.
func MarshalModelSnapshot(s ModelConfigSnapshot) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalModelSnapshot parses a stored snapshot.
func UnmarshalModelSnapshot(s string) (ModelConfigSnapshot, error) {
	var out ModelConfigSnapshot
	if s == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return out, fmt.Errorf("invalid model config snapshot: %w", err)
	}
	return out, nil
}

// NewAIClientFromSnapshot builds an mcp.AIClient from a snapshot using
// the same provider-registry logic as the live trader (auto_trader.go).
// Falls back to mcp.New() (deepseek default) for unknown providers.
func NewAIClientFromSnapshot(s ModelConfigSnapshot) mcp.AIClient {
	provider := s.Provider
	var client mcp.AIClient
	switch provider {
	case "", "custom":
		client = mcp.New()
	default:
		client = mcp.NewAIClientByProvider(provider)
		if client == nil {
			client = mcp.New()
		}
	}
	// Payment providers (claw402) ignore customURL.
	if provider == mcp.ProviderClaw402 {
		client.SetAPIKey(s.APIKey, "", s.Model)
	} else {
		client.SetAPIKey(s.APIKey, s.CustomAPIURL, s.CustomModelName)
	}
	return client
}

// FakeAIClient is a test double implementing mcp.AIClient with a fixed
// response. It counts calls so tests can assert deduplication.
type FakeAIClient struct {
	Response string
	Calls    int
}

// SetAPIKey implements mcp.AIClient.
func (f *FakeAIClient) SetAPIKey(string, string, string) {}

// SetTimeout implements mcp.AIClient.
func (f *FakeAIClient) SetTimeout(time.Duration) {}

// CallWithMessages implements mcp.AIClient.
func (f *FakeAIClient) CallWithMessages(_, _ string) (string, error) {
	f.Calls++
	return f.Response, nil
}

// CallWithRequest implements mcp.AIClient.
func (f *FakeAIClient) CallWithRequest(_ *mcp.Request) (string, error) {
	f.Calls++
	return f.Response, nil
}

// CallWithRequestStream implements mcp.AIClient.
func (f *FakeAIClient) CallWithRequestStream(_ *mcp.Request, onChunk func(string)) (string, error) {
	f.Calls++
	onChunk(f.Response)
	return f.Response, nil
}

// CallWithRequestFull implements mcp.AIClient.
func (f *FakeAIClient) CallWithRequestFull(_ *mcp.Request) (*mcp.LLMResponse, error) {
	f.Calls++
	return &mcp.LLMResponse{Content: f.Response}, nil
}
