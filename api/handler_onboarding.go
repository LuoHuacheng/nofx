package api

import (
	"fmt"
	"net/http"
	"strings"

	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"nofx/mcp/payment"
	"nofx/wallet"
)

// Legacy onboarding returns public wallet details only. Private-key export
// requires a separate, explicit owner-scoped request from wallet settings.
type beginnerOnboardingResponse struct {
	Address           string `json:"address"`
	Chain             string `json:"chain"`
	Asset             string `json:"asset"`
	Provider          string `json:"provider"`
	DefaultModel      string `json:"default_model"`
	ConfiguredModelID string `json:"configured_model_id"`
	BalanceUSDC       string `json:"balance_usdc"`
	BalanceStatus     string `json:"balance_status"`
	EnvSaved          bool   `json:"env_saved"`
	ReusedExisting    bool   `json:"reused_existing"`
}

type currentBeginnerWalletResponse struct {
	Found         bool   `json:"found"`
	Address       string `json:"address,omitempty"`
	BalanceUSDC   string `json:"balance_usdc,omitempty"`
	BalanceStatus string `json:"balance_status,omitempty"`
	Source        string `json:"source,omitempty"`
	Claw402Status string `json:"claw402_status"`
}

func (s *Server) handleBeginnerOnboarding(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	user := c.GetString("user_id")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	before, err := s.resolvePlanningSetupWallet(user, false, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read your AI wallet"})
		return
	}
	current, err := s.resolvePlanningSetupWallet(user, true, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Could not prepare your AI wallet. Check encrypted storage and your saved model in Config."})
		return
	}
	c.JSON(http.StatusOK, beginnerOnboardingResponse{
		Address: current.Address, Chain: current.Chain, Asset: current.Asset,
		Provider: "claw402", DefaultModel: payment.DefaultClaw402Model,
		ConfiguredModelID: current.ModelID, BalanceStatus: "unknown",
		ReusedExisting: before.Found,
	})
}

func (s *Server) handleCurrentBeginnerWallet(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	user := c.GetString("user_id")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	current, err := s.resolvePlanningSetupWallet(user, false, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read your AI wallet"})
		return
	}
	out := currentBeginnerWalletResponse{Found: current.Found, Address: current.Address, BalanceStatus: "unknown", Claw402Status: "unknown"}
	if current.Found {
		out.Source = "model"
		out.BalanceUSDC, out.BalanceStatus = queryBeginnerWalletBalance(current.Address)
	}
	c.JSON(http.StatusOK, out)
}

func walletAddressFromPrivateKey(privateKey string) (string, error) {
	key := strings.TrimSpace(privateKey)
	if !strings.HasPrefix(key, "0x") {
		return "", fmt.Errorf("private key must start with 0x")
	}
	if len(key) != 66 {
		return "", fmt.Errorf("private key must be 66 characters")
	}

	privateKeyObj, err := gethcrypto.HexToECDSA(strings.TrimPrefix(key, "0x"))
	if err != nil {
		return "", err
	}

	return gethcrypto.PubkeyToAddress(privateKeyObj.PublicKey).Hex(), nil
}

func queryBeginnerWalletBalance(address string) (string, string) {
	balance, err := wallet.QueryUSDCBalanceCached(address)
	if err != nil {
		return "", "unknown"
	}
	return fmt.Sprintf("%.2f", balance), "ok"
}
