// Package nofxos provides data access to the NofxOS API (https://nofxos.ai)
// for quantitative trading data including AI500 scores, OI rankings,
// fund flow (NetFlow), price rankings, and coin details.
package nofxos

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Default configuration
const (
	DefaultBaseURL = "https://nofxos.ai"
	DefaultTimeout = 30 * time.Second
	// Deprecated: data access requires an explicitly configured Claw402 wallet.
	DefaultAuthKey = ""
)

// Client is the NofxOS API client
type Client struct {
	BaseURL    string
	AuthKey    string
	Timeout    time.Duration
	mu         sync.RWMutex
	claw402    *Claw402DataClient // If set, routes requests through claw402
	gatewayErr error              // fail closed when automatic paid-client setup fails
}

var (
	defaultClient *Client
	clientOnce    sync.Once
)

// DefaultClient returns the singleton default client
func DefaultClient() *Client {
	clientOnce.Do(func() {
		defaultClient = ResolveClient("")
	})
	return defaultClient
}

// NewClient creates a new NofxOS API client
func NewClient(baseURL, authKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if authKey == "" {
		authKey = DefaultAuthKey
	}
	return &Client{
		BaseURL: baseURL,
		AuthKey: authKey,
		Timeout: DefaultTimeout,
	}
}

// SetClaw402 enables routing requests through claw402 payment gateway.
func (c *Client) SetClaw402(claw402Client *Claw402DataClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claw402 = claw402Client
	c.gatewayErr = nil
}

// SetConfig updates client configuration
func (c *Client) SetConfig(baseURL, authKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if baseURL != "" {
		c.BaseURL = baseURL
	}
	if authKey != "" {
		c.AuthKey = authKey
	}
}

// GetBaseURL returns the current base URL
func (c *Client) GetBaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.BaseURL
}

// GetAuthKey returns the current auth key
func (c *Client) GetAuthKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AuthKey
}

// doRequest requires the paid gateway, including for legacy constructor callers.
// A supplied origin URL/auth key must never enable a payment bypass.
func (c *Client) doRequest(endpoint string) ([]byte, error) {
	c.mu.RLock()
	paid, gatewayErr := c.claw402, c.gatewayErr
	c.mu.RUnlock()
	if gatewayErr != nil {
		return nil, gatewayErr
	}
	if paid == nil {
		return nil, errors.New("Claw402 paid data routing is required; direct origin access is disabled")
	}
	return paid.DoRequest(endpoint)
}

// APIError represents an API error response
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

// ExtractAuthKey extracts auth key from a URL string
func ExtractAuthKey(url string) string {
	if idx := strings.Index(url, "auth="); idx != -1 {
		authKey := url[idx+5:]
		if ampIdx := strings.Index(authKey, "&"); ampIdx != -1 {
			authKey = authKey[:ampIdx]
		}
		return authKey
	}
	return ""
}
