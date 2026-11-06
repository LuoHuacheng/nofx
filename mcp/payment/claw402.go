package payment

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"

	"nofx/mcp"
	"nofx/mcp/provider"
)

// ErrInsufficientFunds is returned when the claw402 wallet does not hold
// enough USDC to cover the live quoted ceiling. Callers can type-assert
// to surface balance/needed/address to the UI.
type ErrInsufficientFunds struct {
	Address string
	Balance float64
	Needed  float64
	Model   string
}

func (e *ErrInsufficientFunds) Error() string {
	return fmt.Sprintf(
		"claw402 insufficient Base USDC: wallet=%s balance=$%.6f quoted_ceiling=$%.6f model=%s; fund the fee wallet with Base USDC (not ETH); no payment submitted",
		shortAddr(e.Address), e.Balance, e.Needed, e.Model,
	)
}

func (e *ErrInsufficientFunds) Retryable() bool { return false }

// shortAddr renders 0x1234…abcd for log/error strings that may leak into
// telemetry bundles. The full address stays on the struct for programmatic use.
func shortAddr(addr string) string {
	if len(addr) < 10 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-4:]
}

const (
	DefaultClaw402URL   = "https://claw402.ai"
	DefaultClaw402Model = "gpt-5.6"
)

// claw402ModelEndpoints maps user-friendly model names to claw402 API paths.
// Must stay in sync with the claw402 catalog (GET /api/v1/catalog).
var claw402ModelEndpoints = map[string]string{
	// OpenAI
	"gpt-6":         "/api/v1/ai/openai/chat/6",
	"gpt6":          "/api/v1/ai/openai/chat/6",
	"gpt-6-astra":   "/api/v1/ai/openai/chat/6",
	"gpt-6.1-sol":   "/api/v1/ai/openai/chat/6.1-sol",
	"gpt-6-sol":     "/api/v1/ai/openai/chat/6-sol",
	"gpt-6-luna":    "/api/v1/ai/openai/chat/6-luna",
	"gpt-5.6":       "/api/v1/ai/openai/chat/5.6",
	"gpt-5.6-sol":   "/api/v1/ai/openai/chat/5.6",
	"gpt-5.6-terra": "/api/v1/ai/openai/chat/5.6-terra",
	"gpt-5.6-luna":  "/api/v1/ai/openai/chat/5.6-luna",
	// Anthropic
	"claude-fable":    "/api/v1/ai/anthropic/messages/fable",
	"claude-fable-5":  "/api/v1/ai/anthropic/messages/fable",
	"claude-opus":     "/api/v1/ai/anthropic/messages/opus",
	"claude-opus-4-8": "/api/v1/ai/anthropic/messages/opus",
	// DeepSeek
	"deepseek":          "/api/v1/ai/deepseek/v4-flash",
	"deepseek-flash":    "/api/v1/ai/deepseek/v4-flash",
	"deepseek-reasoner": "/api/v1/ai/deepseek/v4-pro",
	"deepseek-v4-flash": "/api/v1/ai/deepseek/v4-flash",
	"deepseek-v4-pro":   "/api/v1/ai/deepseek/v4-pro",
	// Z.AI (Zhipu)
	"glm-5": "/api/v1/ai/zhipu/chat",
}

func init() {
	mcp.RegisterProvider(mcp.ProviderClaw402, func(opts ...mcp.ClientOption) mcp.AIClient {
		return NewClaw402ClientWithOptions(opts...)
	})
}

// Claw402Client implements AIClient using claw402.ai's x402 v2 USDC payment gateway.
// When the selected model routes to an Anthropic endpoint, it automatically uses
// the Anthropic wire format for requests and responses (via an internal ClaudeClient).
type Claw402Client struct {
	*mcp.Client
	privateKey  *ecdsa.PrivateKey
	claudeProxy *provider.ClaudeClient // non-nil when endpoint is /anthropic/
}

func (c *Claw402Client) BaseClient() *mcp.Client { return c.Client }

// LastCallCostUSD reports only independently verified settlement amounts.
// Token estimates and gateway headers alone are not money movement evidence.
func (c *Claw402Client) LastCallCostUSD() (float64, bool) {
	if c.Client.LastCallSettlementVerified {
		return c.Client.LastCallSettledUSD, true
	}
	return 0, false
}

// NewClaw402Client creates a claw402 client (backward compatible).
func NewClaw402Client() mcp.AIClient {
	return NewClaw402ClientWithOptions()
}

// NewClaw402ClientWithOptions creates a claw402 client with options.
func NewClaw402ClientWithOptions(opts ...mcp.ClientOption) mcp.AIClient {
	baseOpts := []mcp.ClientOption{
		mcp.WithProvider(mcp.ProviderClaw402),
		mcp.WithModel(DefaultClaw402Model),
		mcp.WithBaseURL(DefaultClaw402URL),
		mcp.WithTimeout(X402Timeout),
		mcp.WithMaxRetries(1), // disable outer retry — inner x402 loop handles retries; outer retry causes duplicate payments
	}
	allOpts := append(baseOpts, opts...)
	baseClient := mcp.NewClient(allOpts...).(*mcp.Client)
	baseClient.UseFullURL = true
	baseClient.BaseURL = DefaultClaw402URL + claw402ModelEndpoints[DefaultClaw402Model]

	c := &Claw402Client{Client: baseClient}
	baseClient.Hooks = c
	return c
}

// SetAPIKey stores the EVM private key and selects the model endpoint.
func (c *Claw402Client) SetAPIKey(apiKey string, _ string, customModel string) {
	// Clear first so a malformed rotation can never keep spending from the
	// previously configured wallet.
	c.privateKey = nil
	c.APIKey = ""
	hexKey := strings.TrimSpace(apiKey)
	if len(hexKey) >= 2 && strings.EqualFold(hexKey[:2], "0x") {
		hexKey = hexKey[2:]
	}
	privKey, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		c.Log.Warnf("⚠️  [MCP] Claw402: invalid private key: %v", err)
	} else {
		c.privateKey = privKey
		c.APIKey = hexKey
		addr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()
		c.Log.Infof("🔧 [MCP] Claw402 wallet: %s", addr)
	}
	if customModel != "" {
		c.Model = customModel
	}
	endpoint := c.resolveEndpoint()
	c.BaseURL = DefaultClaw402URL + endpoint

	// Anthropic endpoints need different wire format (Messages API)
	if strings.Contains(endpoint, "/anthropic/") {
		c.claudeProxy = &provider.ClaudeClient{Client: c.Client}
		c.Log.Infof("🔧 [MCP] Claw402 model: %s → %s (Anthropic format)", c.Model, endpoint)
	} else {
		c.claudeProxy = nil
		c.Log.Infof("🔧 [MCP] Claw402 model: %s → %s", c.Model, endpoint)
	}
}

// resolveEndpoint returns the API path for the configured model.
func (c *Claw402Client) resolveEndpoint() string {
	if ep, ok := claw402ModelEndpoints[c.Model]; ok {
		return ep
	}
	// Allow raw path override (e.g. "/api/v1/ai/openai/chat/5.4")
	if strings.HasPrefix(c.Model, "/api/") {
		return c.Model
	}
	return ""
}

func (c *Claw402Client) SetAuthHeader(h http.Header) { X402SetAuthHeader(h) }

// Discovery already retries safely. Repeating an entire inference after a
// signed request or a broken stream would create another spending permission.
func (c *Claw402Client) IsRetryableError(error) bool { return false }

func (c *Claw402Client) Call(systemPrompt, userPrompt string) (string, error) {
	if c.resolveEndpoint() == "" {
		return "", fmt.Errorf("unsupported Claw402 model %q; select a model listed in the gateway catalog; no payment submitted", c.Model)
	}
	unlock := lockFeeWallet(c.walletAddress())
	defer unlock()
	return X402CallStream(c.Client, c.signPayment, "Claw402", systemPrompt, userPrompt, nil)
}

func (c *Claw402Client) CallWithRequestFull(req *mcp.Request) (*mcp.LLMResponse, error) {
	if c.resolveEndpoint() == "" {
		return nil, fmt.Errorf("unsupported Claw402 model %q; select a model listed in the gateway catalog; no payment submitted", c.Model)
	}
	unlock := lockFeeWallet(c.walletAddress())
	defer unlock()
	return X402CallFull(c.Client, c.signPayment, "Claw402", req)
}

// walletAddress derives the EVM address from the configured private key.
// Returns "" when no key has been set (client unconfigured).
func (c *Claw402Client) walletAddress() string {
	if c.privateKey == nil {
		return ""
	}
	return crypto.PubkeyToAddress(c.privateKey.PublicKey).Hex()
}

// signPayment requires usage settlement for model calls. It never silently
// downgrades to a full-ceiling exact charge or sends approval transactions.
// Insufficient allowance uses a bounded, gas-sponsored off-chain permit.
func (c *Claw402Client) signPayment(paymentHeaderB64 string) (string, error) {
	if c.privateKey == nil {
		return "", fmt.Errorf("Claw402 fee wallet is not configured")
	}
	raw, err := X402DecodeHeader(paymentHeaderB64)
	if err != nil {
		return "", err
	}
	var required X402v2PaymentRequired
	if err = json.Unmarshal(raw, &required); err != nil {
		return "", err
	}
	if required.X402Version != 2 {
		return "", fmt.Errorf("unsupported x402 payment version")
	}
	for _, option := range required.Accepts {
		if option.Scheme != "upto" || option.Network != BaseNetwork {
			continue
		}
		return signSponsoredPayment(c.privateKey, c.walletAddress(), option, required)
	}
	return "", fmt.Errorf("Claw402 model requires native Permit2 upto; refusing fixed-price fallback")
}

// ── Format overrides for Anthropic endpoints ─────────────────────────────────

// stripMaxTokens removes per-call max_tokens caps from a body destined for
// claw402. The gateway already enforces a per-route default/floor/cap
// (see providers/*.yaml token_default_max_out / token_min_max_out /
// token_max_out_cap). Sending a small max_tokens here on a thinking model
// (Kimi K2.5, DeepSeek R1/V4) caused reasoning tokens to consume the entire
// budget and left `delta.content` empty, surfacing as "no content received".
// The live gateway ceiling is checked against the wallet balance before signing;
// upto charges actual usage, which can increase when more output is allowed.
func stripMaxTokens(body map[string]any) map[string]any {
	if body == nil {
		return body
	}
	delete(body, "max_tokens")
	delete(body, "max_completion_tokens")
	return body
}

// applyClawModelControls prevents DeepSeek V4 from spending the gateway's
// entire output allowance on reasoning_content and returning no final answer.
// The gateway accepts the standard thinking control on these V4 routes.
func applyClawModelControls(body map[string]any, model string) map[string]any {
	if body == nil {
		return body
	}
	switch model {
	case "deepseek", "deepseek-flash", "deepseek-reasoner", "deepseek-v4-flash", "deepseek-v4-pro":
		body["thinking"] = map[string]any{"type": "disabled"}
	}
	return body
}

func (c *Claw402Client) BuildMCPRequestBody(systemPrompt, userPrompt string) map[string]any {
	if c.claudeProxy != nil {
		return c.claudeProxy.BuildMCPRequestBody(systemPrompt, userPrompt)
	}
	body := stripMaxTokens(c.Client.BuildMCPRequestBody(systemPrompt, userPrompt))
	return applyClawModelControls(body, c.Model)
}

func (c *Claw402Client) BuildRequestBodyFromRequest(req *mcp.Request) map[string]any {
	if c.claudeProxy != nil {
		return c.claudeProxy.BuildRequestBodyFromRequest(req)
	}
	body := stripMaxTokens(c.Client.BuildRequestBodyFromRequest(req))
	return applyClawModelControls(body, c.Model)
}

func (c *Claw402Client) ParseMCPResponse(body []byte) (string, error) {
	if c.claudeProxy != nil {
		return c.claudeProxy.ParseMCPResponse(body)
	}
	return c.Client.ParseMCPResponse(body)
}

func (c *Claw402Client) ParseMCPResponseFull(body []byte) (*mcp.LLMResponse, error) {
	if c.claudeProxy != nil {
		return c.claudeProxy.ParseMCPResponseFull(body)
	}
	return c.Client.ParseMCPResponseFull(body)
}

// BuildUrl returns the full claw402 endpoint URL.
func (c *Claw402Client) BuildUrl() string {
	return c.BaseURL
}

func (c *Claw402Client) BuildRequest(url string, jsonData []byte) (*http.Request, error) {
	return X402BuildRequest(url, jsonData)
}
