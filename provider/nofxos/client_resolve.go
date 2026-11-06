package nofxos

import (
	"fmt"
	"os"
	"strings"

	"nofx/config"
	"nofx/logger"
)

// ResolveClient returns a nofxos data client, routed through the claw402
// x402 payment gateway. Resolution order:
// the explicit walletKey argument, then the CLAW402_WALLET_KEY environment
// variable. Missing/invalid keys fail closed; they must not bypass payment.
// Legacy constructors cannot enable a direct-origin fallback.
func ResolveClient(walletKey string) *Client {
	walletKey = strings.TrimSpace(walletKey)
	if !config.IsClaw402DataEnabled() {
		// claw402 data routing disabled via FEATURE_CLAW402_ENABLED=false
		return NewClient(DefaultBaseURL, DefaultAuthKey)
	}
	if walletKey == "" {
		walletKey = strings.TrimSpace(os.Getenv("CLAW402_WALLET_KEY"))
	}
	client := NewClient(DefaultBaseURL, DefaultAuthKey)
	if walletKey == "" {
		client.gatewayErr = fmt.Errorf("Claw402 fee wallet is not configured; paid data access is unavailable")
		return client
	}

	claw402URL := strings.TrimSpace(os.Getenv("CLAW402_URL"))
	claw402Client, err := NewClaw402DataClient(claw402URL, walletKey, &logger.MCPLogger{})
	if err != nil {
		client.gatewayErr = fmt.Errorf("cannot initialize paid Claw402 data client: %w", err)
		logger.Warnf("⚠️ Claw402 data access disabled: %v", err)
		return client
	}
	client.SetClaw402(claw402Client)
	return client
}
