package payment

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"

	"nofx/internal/permit2"
	"nofx/mcp"
)

const (
	// X402MaxPaymentRetries limits unsigned discovery attempts only.
	// Signed requests are never automatically replayed or re-signed.
	X402MaxPaymentRetries = 5

	// X402RetryBaseWait is the base wait between payment retry attempts.
	X402RetryBaseWait = 3 * time.Second

	// X402Timeout is the HTTP timeout for x402 payment providers.
	// AI inference (especially DeepSeek) can take several minutes; the default
	// 120s causes premature timeouts that trigger duplicate payments.
	X402Timeout = 5 * time.Minute
)

func x402ContextFromRequest(req *mcp.Request) context.Context {
	if req != nil && req.Ctx != nil {
		return req.Ctx
	}
	return context.Background()
}

func x402Sleep(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func doInitialX402Request(
	ctx context.Context,
	httpClient *http.Client,
	buildReqFn func() (*http.Request, error),
	providerTag string,
	logger mcp.Logger,
) (*http.Response, error) {
	var lastBody []byte
	var lastErr error
	var lastStatus int

	for attempt := 1; attempt <= X402MaxPaymentRetries; attempt++ {
		req, err := buildReqFn()
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req = req.WithContext(ctx)
		if req.Header.Get("Payment-Signature") != "" || req.Header.Get("X-Payment") != "" {
			return nil, fmt.Errorf("initial x402 discovery must not contain a payment authorization")
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < X402MaxPaymentRetries {
				wait := X402RetryBaseWait * time.Duration(attempt)
				logger.Warnf("⚠️  [%s] Initial request failed: %v, retrying in %v (%d/%d)...",
					providerTag, err, wait, attempt+1, X402MaxPaymentRetries)
				if err := x402Sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("failed to send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPaymentRequired {
			return resp, nil
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read response: %w", readErr)
		}
		lastBody = body
		lastStatus = resp.StatusCode

		if isRetryableInitialX402Status(resp.StatusCode) && attempt < X402MaxPaymentRetries {
			wait := X402RetryBaseWait * time.Duration(attempt)
			logger.Warnf("⚠️  [%s] Initial server error (status %d), retrying in %v (%d/%d)...",
				providerTag, resp.StatusCode, wait, attempt+1, X402MaxPaymentRetries)
			if err := x402Sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}

		return nil, fmt.Errorf("%s API error (status %d): %s", providerTag, resp.StatusCode, string(body))
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to send request after %d retries: %w", X402MaxPaymentRetries, lastErr)
	}
	return nil, fmt.Errorf("%s API error after %d retries (status %d): %s", providerTag, X402MaxPaymentRetries, lastStatus, string(lastBody))
}

func isRetryableInitialX402Status(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// ── Shared x402 types ────────────────────────────────────────────────────────

// X402v2PaymentRequired is the structure of the Payment-Required header (x402 v2).
type X402v2PaymentRequired struct {
	X402Version int                        `json:"x402Version"`
	Accepts     []X402AcceptOption         `json:"accepts"`
	Resource    *X402Resource              `json:"resource"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

// X402AcceptOption is a payment option from the x402 v2 header.
type X402AcceptOption struct {
	Scheme            string            `json:"scheme"`
	Network           string            `json:"network"`
	Amount            string            `json:"amount"`
	Asset             string            `json:"asset"`
	PayTo             string            `json:"payTo"`
	MaxTimeoutSeconds int               `json:"maxTimeoutSeconds"`
	Extra             map[string]string `json:"extra"`
}

// X402Resource describes the resource being paid for.
type X402Resource struct {
	URL         string `json:"url"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// X402SignFunc is a callback that signs an x402 payment header and returns the
// base64-encoded payment signature.
type X402SignFunc func(paymentHeaderB64 string) (string, error)

// ── Shared x402 helpers ──────────────────────────────────────────────────────

// X402DecodeHeader decodes a base64-encoded x402 Payment-Required header,
// trying RawStdEncoding first then StdEncoding as fallback.
func X402DecodeHeader(b64 string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("failed to base64-decode payment header: %w", err)
		}
	}
	return decoded, nil
}

// MakeClaw402SignFunc creates an X402SignFunc from a private key for claw402 payments.
func MakeClaw402SignFunc(privateKey *ecdsa.PrivateKey) X402SignFunc {
	return func(paymentHeaderB64 string) (string, error) {
		return SignBasePaymentHeader(privateKey, paymentHeaderB64, "Claw402")
	}
}

// SignBasePaymentHeader decodes a base64 x402 header and signs exact EIP-3009
// or native Permit2 upto, never an EIP-3009 authorization relabeled as upto.
func SignBasePaymentHeader(privateKey *ecdsa.PrivateKey, paymentHeaderB64 string, providerName string) (string, error) {
	if privateKey == nil {
		return "", fmt.Errorf("no private key set for %s wallet", providerName)
	}

	decoded, err := X402DecodeHeader(paymentHeaderB64)
	if err != nil {
		return "", err
	}

	var req X402v2PaymentRequired
	if err := json.Unmarshal(decoded, &req); err != nil {
		return "", fmt.Errorf("failed to parse x402 v2 payment header: %w", err)
	}
	if len(req.Accepts) == 0 {
		return "", fmt.Errorf("no payment options in x402 response")
	}
	if req.X402Version != 2 {
		return "", fmt.Errorf("unsupported x402 payment version")
	}

	senderAddr := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
	for _, option := range req.Accepts {
		if option.Network == BaseNetwork && strings.EqualFold(option.Asset, BaseUSDCContract) && (option.Scheme == "exact" || option.Scheme == "upto") {
			return signSponsoredPayment(privateKey, senderAddr, option, req)
		}
	}
	return "", fmt.Errorf("no supported Base USDC payment option; no payment submitted")
}

// X402SettledUSDHeader carries the actually-settled cost (USD) of an upto
// call, set by the claw402 gateway on the paid response.
const X402SettledUSDHeader = "X-Claw402-Settled-Usd"

func storeRespHeader(sink []*http.Header, h http.Header) {
	for _, p := range sink {
		if p != nil {
			*p = h
		}
	}
}

// captureSettledUSD records only costs checked by the local receipt verifier.
// Missing verification remains unknown, never a catalog-price estimate.
func captureSettledUSD(c *mcp.Client, h http.Header) {
	c.LastCallSettledUSD = 0
	c.LastCallSettlementVerified = false
	if h == nil || h.Get("X-Nofx-Receipt-Verified") != "true" {
		return
	}
	if v := h.Get(X402SettledUSDHeader); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && !math.IsNaN(f) && !math.IsInf(f, 0) {
			c.LastCallSettledUSD = f
			c.LastCallSettlementVerified = true
		}
	}
}

// PaymentOutcomeUnknownError must never trigger an automatic new authorization.
// A transport error or any paid HTTP error (including 402) is not proof of expiry.
type PaymentOutcomeUnknownError struct{ Cause error }

func (e *PaymentOutcomeUnknownError) Error() string {
	// This message reaches API responses and persisted decision logs. Keep raw
	// transport bytes, URLs and remote bodies out of user-visible errors; callers
	// can still inspect the original typed cause via errors.Is/As and Unwrap.
	reason := "payment response or settlement receipt could not be verified"
	if e.Cause != nil && strings.Contains(strings.ToLower(e.Cause.Error()), "malformed http response") {
		reason = "payment response could not be read due to an HTTP protocol error"
	}
	return "paid request outcome unknown; refusing automatic replay: " + reason + "; check the original payment before retrying [diagnostic:" + e.ReasonCode() + "]"
}

// ReasonCode exposes only a fixed diagnostic category, never remote text,
// response bodies, wallet credentials or transport URLs.
func (e *PaymentOutcomeUnknownError) ReasonCode() string {
	if e.Cause == nil {
		return "verification_failed"
	}
	text := strings.ToLower(e.Cause.Error())
	for _, hint := range []string{"schema_required", "schema_additional_properties", "function_schema", "stream_required", "tools_unsupported", "context_length"} {
		if strings.Contains(text, "hint:"+hint) {
			return "gateway_" + hint
		}
	}
	for _, r := range []struct{ match, code string }{
		{"validation:invalid_function_parameters", "gateway_invalid_function_parameters"},
		{"validation:unsupported_parameter", "gateway_unsupported_parameter"},
		{"validation:invalid_request_error", "gateway_invalid_request"},
		{"paid request returned status 400", "gateway_http_400"},
		{"paid request returned status 429", "gateway_http_429"},
		{"paid request returned status 502", "gateway_http_502"},
		{"paid request returned status 503", "gateway_http_503"},
		{"usage stream", "settlement_stream_invalid"},
		{"malformed http response", "http_protocol_error"},
		{"missing payment receipt", "receipt_missing"},
		{"missing or invalid settled amount", "settled_amount_missing"},
		{"conflicting", "receipt_conflict"},
		{"rpc chain mismatch or unavailable", "receipt_rpc_unavailable"},
		{"receipt unavailable", "settlement_pending"},
		{"not confirmed successfully", "settlement_pending"},
		{"does not match authorization", "authorization_mismatch"},
		{"on-chain amount mismatch", "settlement_mismatch"},
		{"deadline exceeded", "request_timeout"},
	} {
		if strings.Contains(text, r.match) {
			return r.code
		}
	}
	return "verification_failed"
}

func (e *PaymentOutcomeUnknownError) Unwrap() error   { return e.Cause }
func (e *PaymentOutcomeUnknownError) Retryable() bool { return false }

// A gateway's explicit pre-settlement rejection is different from an ambiguous
// paid/network failure. Neither authorizes automatic re-signing or replay.
type PaymentVerificationRejectedError struct{ Status int }

func (e *PaymentVerificationRejectedError) Error() string {
	return fmt.Sprintf("payment verification rejected (status %d); gateway reports no settlement for this attempt; check the current quote, fee-wallet balance and client version before a new request (earlier submissions may still have charged)", e.Status)
}
func (e *PaymentVerificationRejectedError) Retryable() bool { return false }

// DoX402Request shares the one-authorization flow with streaming requests.
func DoX402Request(ctx context.Context, httpClient *http.Client,
	buildReqFn func() (*http.Request, error), signFn X402SignFunc,
	providerTag string, logger mcp.Logger, headerSink ...*http.Header,
) ([]byte, error) {
	resp, err := DoX402RequestStream(ctx, httpClient, buildReqFn, signFn, providerTag, logger)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &PaymentOutcomeUnknownError{Cause: err}
	}
	storeRespHeader(headerSink, resp.Header)
	return body, nil
}

// DoX402RequestStream signs at most once. Only unsigned discovery can retry.
// The caller owns the successful response body.
func DoX402RequestStream(ctx context.Context, httpClient *http.Client,
	buildReqFn func() (*http.Request, error), signFn X402SignFunc,
	providerTag string, logger mcp.Logger,
) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := doInitialX402Request(ctx, httpClient, buildReqFn, providerTag, logger)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		resp.Header.Del("X-Nofx-Receipt-Verified")
		return resp, nil
	}
	paymentHeader := resp.Header.Get("Payment-Required")
	if paymentHeader == "" {
		paymentHeader = resp.Header.Get("X-Payment-Required")
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if paymentHeader == "" {
		return nil, fmt.Errorf("received 402 but no Payment-Required header")
	}
	paymentSig, err := signFn(paymentHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to sign x402 payment: %w", err)
	}
	req, err := buildReqFn()
	if err != nil {
		return nil, fmt.Errorf("failed to build paid request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("X-Payment", paymentSig)
	req.Header.Set("Payment-Signature", paymentSig)
	// A reused Go transport can silently replay GET on EOF even without an
	// application retry. Send paid requests over a fresh, non-reused connection.
	paidClient := singleUsePaidClient(httpClient)
	paidResp, err := paidClient.Do(req)
	if err != nil {
		return nil, &PaymentOutcomeUnknownError{Cause: err}
	}
	if paidResp.StatusCode != http.StatusOK {
		// Retain only allowlisted protocol metadata. Never include a remote error
		// message or arbitrary response body: these may echo private inputs.
		failureBody, _ := io.ReadAll(io.LimitReader(paidResp.Body, 16384))
		paidResp.Body.Close()
		if paidResp.StatusCode == http.StatusPaymentRequired && paidResp.Header.Get("X-Claw402-Payment-Stage") == "verification_rejected" && paidResp.Header.Get("Payment-Response") == "" && paidResp.Header.Get("X-Payment-Response") == "" {
			return nil, &PaymentVerificationRejectedError{Status: paidResp.StatusCode}
		}
		return nil, &PaymentOutcomeUnknownError{Cause: fmt.Errorf("%s paid request returned status %d%s; reconcile the original payment before retrying", providerTag, paidResp.StatusCode, paidFailureMetadata(failureBody))}
	}
	// Remote headers cannot manufacture this internal verification flag.
	paidResp.Header.Del("X-Nofx-Receipt-Verified")
	if err := guardUsageResponse(paidResp, paymentSig); err != nil {
		paidResp.Body.Close()
		return nil, &PaymentOutcomeUnknownError{Cause: err}
	}
	return paidResp, nil
}

func paidFailureMetadata(body []byte) string {
	var response struct {
		Error   json.RawMessage `json:"error"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	var nested struct {
		Code    string `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(response.Error, &nested) == nil {
		if nested.Code != "" {
			response.Code = nested.Code
		} else if nested.Type != "" {
			response.Code = nested.Type
		}
		if nested.Message != "" {
			response.Message = nested.Message
		}
	}
	message := strings.ToLower(response.Message)
	if strings.Contains(message, "schema") {
		if strings.Contains(message, "required") {
			return " hint:schema_required"
		}
		if strings.Contains(message, "additionalproperties") {
			return " hint:schema_additional_properties"
		}
		return " hint:function_schema"
	}
	if strings.Contains(message, "stream") && strings.Contains(message, "true") {
		return " hint:stream_required"
	}
	if strings.Contains(message, "tools") && strings.Contains(message, "not supported") {
		return " hint:tools_unsupported"
	}
	if strings.Contains(message, "context length") {
		return " hint:context_length"
	}
	for _, code := range []string{"invalid_function_parameters", "unsupported_parameter", "invalid_request_error"} {
		if response.Code == code {
			return " validation:" + code
		}
	}
	return ""
}

// x402StreamIdleTimeout is the idle timeout for SSE streaming through x402.
// If no SSE line arrives for this duration, the stream is considered stalled.
const x402StreamIdleTimeout = 90 * time.Second

// X402CallStream handles the x402 payment flow with streaming for the simple Call path.
// It adds "stream": true to the request body and uses ParseSSEStream to read chunks.
//
// Robustness: uses TeeReader so the raw body is captured while parsing SSE.
// If SSE parsing yields no text (e.g. server returned plain JSON despite stream:true),
// falls back to ParseMCPResponse on the buffered body.
func X402CallStream(c *mcp.Client, signFn X402SignFunc, tag string, systemPrompt, userPrompt string, onChunk func(string)) (string, error) {
	c.Log.Infof("📡 [%s] Request AI Server (stream): %s", tag, c.BaseURL)

	requestBody := c.Hooks.BuildMCPRequestBody(systemPrompt, userPrompt)
	requestBody["stream"] = true
	jsonData, err := c.Hooks.MarshalRequestBody(requestBody)
	if err != nil {
		return "", err
	}

	// Idle-timeout context: cancel() closes the underlying TCP connection,
	// which immediately unblocks any pending resp.Body.Read().
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.LastCallSettledUSD = 0
	c.LastCallUsage = nil
	c.LastCallSettlementVerified = false
	resp, err := DoX402RequestStream(ctx, c.HTTPClient, func() (*http.Request, error) {
		return c.Hooks.BuildRequest(c.Hooks.BuildUrl(), jsonData)
	}, signFn, tag, c.Log)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	captureSettledUSD(c, resp.Header)

	ct := resp.Header.Get("Content-Type")
	c.Log.Infof("📡 [%s] Response Content-Type: %s", tag, ct)

	// Start idle-timeout watchdog AFTER the 402 dance is done.
	resetCh := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(x402StreamIdleTimeout)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.Log.Warnf("⚠️  [%s] SSE idle timeout (%v), cancelling stream", tag, x402StreamIdleTimeout)
				cancel() // closes the TCP connection → body.Read() returns error
				return
			case <-resetCh:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(x402StreamIdleTimeout)
			}
		}
	}()

	onLine := func() {
		select {
		case resetCh <- struct{}{}:
		default:
		}
	}

	// TeeReader: body is streamed through SSE parser AND captured in bodyBuf.
	// If SSE yields nothing (server returned JSON), we can still parse bodyBuf.
	var bodyBuf bytes.Buffer
	tee := io.TeeReader(resp.Body, &bodyBuf)

	text, usage, sseErr := mcp.ParseSSEStream(tee, onChunk, onLine)
	captureSettledUSD(c, resp.Header)
	mcp.ReportStreamUsage(usage, c.Provider, c.Model)
	c.LastCallUsage = usage

	if text != "" {
		if sseErr != nil {
			return "", fmt.Errorf("[%s] stream failed after partial response: %w", tag, sseErr)
		}
		c.Log.Infof("📡 [%s] SSE stream complete, got %d chars", tag, len(text))
		return text, nil
	}

	// SSE yielded nothing — try JSON fallback on the buffered body. A plain JSON
	// response naturally has no SSE completion marker, so test the documented
	// fallback before propagating that framing error.
	if bodyBuf.Len() > 0 {
		c.Log.Infof("📡 [%s] SSE empty, trying JSON fallback on %d bytes", tag, bodyBuf.Len())
		jsonText, jsonErr := c.Hooks.ParseMCPResponse(bodyBuf.Bytes())
		if jsonErr == nil && jsonText != "" {
			return jsonText, nil
		}
		diagnostic := mcp.DescribeSSEBody(bodyBuf.Bytes())
		c.Log.Warnf("⚠️  [%s] JSON fallback also failed: %v; content_type=%q; SSE shape: %s",
			tag, jsonErr, resp.Header.Get("Content-Type"), diagnostic)
	}
	if sseErr != nil {
		return "", fmt.Errorf("[%s] stream failed: %w", tag, sseErr)
	}

	return "", fmt.Errorf("[%s] no content received (status %d, content-type %q, body %d bytes; %s)",
		tag, resp.StatusCode, resp.Header.Get("Content-Type"), bodyBuf.Len(), mcp.DescribeSSEBody(bodyBuf.Bytes()))
}

// X402BuildRequest creates a POST request with Content-Type but no auth header.
func X402BuildRequest(url string, jsonData []byte) (*http.Request, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("fail to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-ID", "nofx")
	return req, nil
}

// X402SetAuthHeader is a no-op — x402 providers authenticate via payment signing.
func X402SetAuthHeader(_ http.Header) {}

// X402Call handles the x402 payment flow for the simple CallWithMessages path.
func X402Call(c *mcp.Client, signFn X402SignFunc, tag string, systemPrompt, userPrompt string) (string, error) {
	c.Log.Infof("📡 [%s] Request AI Server: %s", tag, c.BaseURL)

	requestBody := c.Hooks.BuildMCPRequestBody(systemPrompt, userPrompt)
	jsonData, err := c.Hooks.MarshalRequestBody(requestBody)
	if err != nil {
		return "", err
	}

	c.LastCallSettledUSD = 0
	var respHeader http.Header
	c.LastCallSettlementVerified = false
	body, err := DoX402Request(context.Background(), c.HTTPClient, func() (*http.Request, error) {
		return c.Hooks.BuildRequest(c.Hooks.BuildUrl(), jsonData)
	}, signFn, tag, c.Log, &respHeader)
	if err != nil {
		return "", err
	}
	captureSettledUSD(c, respHeader)
	return c.Hooks.ParseMCPResponse(body)
}

// X402CallFull handles the x402 payment flow for the advanced Request path.
func X402CallFull(c *mcp.Client, signFn X402SignFunc, tag string, req *mcp.Request) (*mcp.LLMResponse, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("AI API key not set, please call SetAPIKey first")
	}
	if req.Model == "" {
		req.Model = c.Model
	}

	c.Log.Infof("📡 [%s] Request AI (full): %s", tag, c.BaseURL)

	requestBody := c.Hooks.BuildRequestBodyFromRequest(req)
	jsonData, err := c.Hooks.MarshalRequestBody(requestBody)
	if err != nil {
		return nil, err
	}

	c.LastCallSettledUSD = 0
	var respHeader http.Header
	c.LastCallSettlementVerified = false
	body, err := DoX402Request(x402ContextFromRequest(req), c.HTTPClient, func() (*http.Request, error) {
		return c.Hooks.BuildRequest(c.Hooks.BuildUrl(), jsonData)
	}, signFn, tag, c.Log, &respHeader)
	if err != nil {
		return nil, err
	}
	captureSettledUSD(c, respHeader)
	return c.Hooks.ParseMCPResponseFull(body)
}

// ── Shared EIP-712 constants & helpers (Base chain, USDC) ────────────────────

const (
	BaseUSDCContract       = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	BaseChainID      int64 = 8453
	BaseNetwork            = "eip155:8453"
	// One USDC is a deliberately generous hard ceiling for one inference call.
	MaxX402PaymentMicroUSDC = int64(1_000_000)
	// DeepSeek routes currently advertise 660 seconds; cap above that while
	// still rejecting unbounded authorizations.
	MaxX402PaymentTimeoutSeconds = 900
)

// EIP-712 type hashes for USDC TransferWithAuthorization (ERC-3009)
var (
	eip712DomainTypeHash     = keccak256String("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	transferWithAuthTypeHash = keccak256String("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)")
)

func keccak256String(s string) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(s))
	return h.Sum(nil)
}

func keccak256Bytes(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, b := range data {
		h.Write(b)
	}
	return h.Sum(nil)
}

// SignX402Payment is the shared EIP-712 signing logic for x402 v2 on Base USDC.
// PaymentQuoteLimitError is returned before signing or sending any payment.
type PaymentQuoteLimitError struct{ QuoteMicroUSDC int64 }

func (e *PaymentQuoteLimitError) Error() string {
	return fmt.Sprintf("Model request quote %.6f USDC exceeds the per-call limit of %.2f USDC. No payment was submitted; shorten the request or choose another model.", float64(e.QuoteMicroUSDC)/1e6, float64(MaxX402PaymentMicroUSDC)/1e6)
}

func SignX402Payment(privateKey *ecdsa.PrivateKey, senderAddr string, opt X402AcceptOption, resource *X402Resource) (string, error) {
	if privateKey == nil || !strings.EqualFold(senderAddr, crypto.PubkeyToAddress(privateKey.PublicKey).Hex()) {
		return "", fmt.Errorf("invalid x402 signing wallet")
	}
	if opt.Network != BaseNetwork {
		return "", fmt.Errorf("refusing x402 payment on unexpected network %q", opt.Network)
	}
	if !strings.EqualFold(opt.Asset, BaseUSDCContract) {
		return "", fmt.Errorf("refusing x402 payment for unexpected asset %q", opt.Asset)
	}
	if opt.Scheme != "exact" && opt.Scheme != "upto" {
		return "", fmt.Errorf("refusing unsupported x402 scheme %q", opt.Scheme)
	}
	if !permit2.ValidAddress(opt.PayTo) {
		return "", fmt.Errorf("refusing invalid x402 recipient %q", opt.PayTo)
	}
	if opt.MaxTimeoutSeconds <= 0 || opt.MaxTimeoutSeconds > MaxX402PaymentTimeoutSeconds {
		return "", fmt.Errorf("refusing x402 timeout %d", opt.MaxTimeoutSeconds)
	}
	amountBig, err := parseBigInt(opt.Amount)
	if err != nil || amountBig.Sign() <= 0 || !amountBig.IsInt64() {
		return "", fmt.Errorf("refusing x402 amount %q above the per-call safety policy", opt.Amount)
	}
	if amountBig.Cmp(big.NewInt(MaxX402PaymentMicroUSDC)) > 0 {
		return "", &PaymentQuoteLimitError{QuoteMicroUSDC: amountBig.Int64()}
	}
	if opt.Extra != nil {
		if name := opt.Extra["name"]; name != "" && name != "USD Coin" {
			return "", fmt.Errorf("refusing unexpected EIP-712 domain name %q", name)
		}
		if version := opt.Extra["version"]; version != "" && version != "2" {
			return "", fmt.Errorf("refusing unexpected EIP-712 domain version %q", version)
		}
	}
	recipient := opt.PayTo
	amount := opt.Amount
	network := opt.Network
	asset := opt.Asset
	extra := opt.Extra
	maxTimeout := opt.MaxTimeoutSeconds
	if maxTimeout == 0 {
		maxTimeout = 300
	}
	// EIP-3009 is exact-only. Upto must use the distinct Permit2 domain,
	// proxy and facilitator-bound witness; never relabel an exact signature.
	if opt.Scheme == "upto" {
		facilitator := opt.Extra["facilitatorAddress"]
		if !permit2.ValidAddress(facilitator) {
			return "", fmt.Errorf("upto requires a Permit2-capable facilitator; refusing legacy EIP-3009 authorization")
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return "", err
		}
		p, err := permit2.Sign(privateKey, BaseChainID, permit2.Authorization{
			From: senderAddr, Permitted: permit2.Permissions{Token: asset, Amount: amount},
			Spender: permit2.UptoProxy, Nonce: "0x" + hex.EncodeToString(nonce),
			Deadline: strconv.FormatInt(time.Now().Unix()+int64(maxTimeout), 10),
			Witness:  permit2.Witness{To: recipient, Facilitator: facilitator, ValidAfter: "0"},
		})
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(map[string]interface{}{"x402Version": 2, "accepted": opt, "resource": resource, "payload": p})
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(b), nil
	}
	scheme := opt.Scheme
	if scheme == "" {
		scheme = "exact"
	}

	resourceURL := ""
	resourceDesc := ""
	resourceMime := "application/json"
	if resource != nil {
		resourceURL = resource.URL
		resourceDesc = resource.Description
		resourceMime = resource.MimeType
	}

	now := time.Now().Unix()
	validAfter := int64(0)
	validBefore := now + int64(maxTimeout)

	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	nonce := "0x" + hex.EncodeToString(nonceBytes)

	domainName := "USD Coin"
	domainVersion := "2"
	if extra != nil {
		if v, ok := extra["name"]; ok && v != "" {
			domainName = v
		}
		if v, ok := extra["version"]; ok && v != "" {
			domainVersion = v
		}
	}

	domainSeparator, err := buildDomainSeparatorDynamic(domainName, domainVersion, network, asset)
	if err != nil {
		return "", fmt.Errorf("failed to build domain separator: %w", err)
	}

	structHash, err := buildTransferWithAuthHashDynamic(senderAddr, recipient, amountBig, validAfter, validBefore, nonce)
	if err != nil {
		return "", fmt.Errorf("failed to build struct hash: %w", err)
	}

	digest := make([]byte, 0, 66)
	digest = append(digest, 0x19, 0x01)
	digest = append(digest, domainSeparator...)
	digest = append(digest, structHash...)
	hash := keccak256Bytes(digest)

	sig, err := crypto.Sign(hash, privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign: %w", err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}

	sigHex := "0x" + hex.EncodeToString(sig)

	paymentData := map[string]interface{}{
		"x402Version": 2,
		"resource": map[string]string{
			"url":         resourceURL,
			"description": resourceDesc,
			"mimeType":    resourceMime,
		},
		"accepted": map[string]interface{}{
			"scheme":            scheme,
			"network":           network,
			"amount":            amount,
			"asset":             asset,
			"payTo":             recipient,
			"maxTimeoutSeconds": maxTimeout,
			"extra":             extra,
		},
		"payload": map[string]interface{}{
			"signature": sigHex,
			"authorization": map[string]string{
				"from":        senderAddr,
				"to":          recipient,
				"value":       amount,
				"validAfter":  fmt.Sprintf("%d", validAfter),
				"validBefore": fmt.Sprintf("%d", validBefore),
				"nonce":       nonce,
			},
		},
		"extensions": map[string]interface{}{},
	}

	resultJSON, err := json.Marshal(paymentData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payment result: %w", err)
	}

	return base64.StdEncoding.EncodeToString(resultJSON), nil
}

// buildDomainSeparatorDynamic builds the EIP-712 domain separator using runtime values.
func buildDomainSeparatorDynamic(name, version, network, asset string) ([]byte, error) {
	chainID := new(big.Int).SetInt64(BaseChainID)
	if strings.HasPrefix(network, "eip155:") {
		parts := strings.SplitN(network, ":", 2)
		if len(parts) == 2 {
			if n, ok := new(big.Int).SetString(parts[1], 10); ok {
				chainID = n
			}
		}
	}

	contractAddr, err := hex.DecodeString(strings.TrimPrefix(asset, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid contract address: %w", err)
	}

	nameHash := keccak256String(name)
	versionHash := keccak256String(version)

	encoded := make([]byte, 0, 5*32)
	encoded = append(encoded, leftPad32(eip712DomainTypeHash)...)
	encoded = append(encoded, leftPad32(nameHash)...)
	encoded = append(encoded, leftPad32(versionHash)...)
	encoded = append(encoded, leftPad32(chainID.Bytes())...)
	addrPadded := make([]byte, 32)
	copy(addrPadded[32-len(contractAddr):], contractAddr)
	encoded = append(encoded, addrPadded...)

	return keccak256Bytes(encoded), nil
}

// buildTransferWithAuthHashDynamic builds the struct hash for TransferWithAuthorization.
func buildTransferWithAuthHashDynamic(from, to string, value *big.Int, validAfter, validBefore int64, nonce string) ([]byte, error) {
	fromBytes, err := hexToAddress(from)
	if err != nil {
		return nil, fmt.Errorf("invalid from address: %w", err)
	}
	toBytes, err := hexToAddress(to)
	if err != nil {
		return nil, fmt.Errorf("invalid to address: %w", err)
	}
	nonceBytes, err := hexToBytes32(nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce: %w", err)
	}

	validAfterBig := new(big.Int).SetInt64(validAfter)
	validBeforeBig := new(big.Int).SetInt64(validBefore)

	encoded := make([]byte, 0, 7*32)
	encoded = append(encoded, leftPad32(transferWithAuthTypeHash)...)
	encoded = append(encoded, leftPad32(fromBytes)...)
	encoded = append(encoded, leftPad32(toBytes)...)
	encoded = append(encoded, leftPad32(value.Bytes())...)
	encoded = append(encoded, leftPad32(validAfterBig.Bytes())...)
	encoded = append(encoded, leftPad32(validBeforeBig.Bytes())...)
	encoded = append(encoded, leftPad32(nonceBytes)...)

	return keccak256Bytes(encoded), nil
}

func hexToAddress(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(b) != 20 {
		return nil, fmt.Errorf("address must be 20 bytes, got %d", len(b))
	}
	return b, nil
}

func hexToBytes32(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(b) > 32 {
		return nil, fmt.Errorf("nonce too long: %d bytes", len(b))
	}
	return b, nil
}

func parseBigInt(s string) (*big.Int, error) {
	n := new(big.Int)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if _, ok := n.SetString(s[2:], 16); ok {
			return n, nil
		}
		return nil, fmt.Errorf("cannot parse hex big.Int from %q", s)
	}
	if _, ok := n.SetString(s, 10); ok {
		return n, nil
	}
	return nil, fmt.Errorf("cannot parse big.Int from %q", s)
}

// leftPad32 pads a byte slice to 32 bytes on the left (ABI encoding).
func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[:32]
	}
	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)
	return padded
}
