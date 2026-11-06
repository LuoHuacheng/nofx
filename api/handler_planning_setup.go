package api

import (
	"encoding/hex"
	"errors"
	"net/http"
	"nofx/crypto"
	"nofx/wallet"
	"strconv"
	"strings"

	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"nofx/mcp/payment"
	"nofx/planning"
)

// Setup exposes only the current owner's public fee-wallet details. It never
// exports keys, changes process-wide credentials or adopts another user's wallet.
type planningSetupWallet struct {
	Found         bool   `json:"found"`
	Enabled       bool   `json:"enabled"`
	Address       string `json:"address,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	Chain         string `json:"chain"`
	Asset         string `json:"asset"`
	BalanceUSDC   string `json:"balance_usdc,omitempty"`
	BalanceStatus string `json:"balance_status"`
}

func (s *Server) resolvePlanningSetupWallet(user string, create bool, modelID string) (planningSetupWallet, error) {
	out := planningSetupWallet{Chain: "base", Asset: "USDC", BalanceStatus: "unknown"}
	unlock := planning.LockAccount("setup-wallet/" + user)
	defer unlock()
	models, err := s.store.AIModel().List(user)
	if err != nil {
		return out, err
	}
	invalid := false
	for _, m := range models {
		if m == nil || m.Provider != "claw402" || (modelID != "" && m.ID != modelID) {
			continue
		}
		key := strings.TrimSpace(m.APIKey.String())
		if key == "" {
			continue
		}
		address, e := walletAddressFromPrivateKey(key)
		if e != nil {
			invalid = true
			continue
		}
		if create && !m.Enabled {
			if e = s.store.AIModel().Update(user, m.ID, true, "", m.CustomAPIURL, m.CustomModelName); e != nil {
				return out, e
			}
		}
		out.Found, out.Enabled, out.Address, out.ModelID = true, m.Enabled || create, address, m.ID
		return out, nil
	}
	if !create {
		return out, nil
	}
	if invalid || modelID != "" {
		return out, errors.New("The existing AI wallet configuration is invalid. Repair it in Settings; no replacement wallet was created.")
	}
	key, err := gethcrypto.GenerateKey()
	if err != nil {
		return out, err
	}
	secret := "0x" + hex.EncodeToString(gethcrypto.FromECDSA(key))
	encoded, encryptionErr := crypto.EncryptedString(secret).Value()
	if encryptionErr != nil || encoded == secret {
		return out, errors.New("Enable encrypted storage before creating a fee wallet")
	}
	if err = s.store.AIModel().Update(user, "claw402", true, secret, "", payment.DefaultClaw402Model); err != nil {
		return out, err
	}
	models, err = s.store.AIModel().List(user)
	if err != nil {
		return out, err
	}
	for _, m := range models {
		if m.Provider == "claw402" && m.APIKey.String() == secret {
			out.Found, out.Enabled, out.Address, out.ModelID = true, true, gethcrypto.PubkeyToAddress(key.PublicKey).Hex(), m.ID
			return out, nil
		}
	}
	return out, errors.New("Could not verify the saved AI wallet")
}

func (s *Server) handlePlanningSetupWallet(c *gin.Context) {
	user := c.GetString("user_id")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	modelID := c.Query("model_id")
	if c.Request.Method == http.MethodPost {
		var request struct {
			ModelID string `json:"model_id"`
		}
		if c.ShouldBindJSON(&request) != nil {
			c.JSON(400, gin.H{"error": "Invalid setup request"})
			return
		}
		modelID = request.ModelID
	}
	out, err := s.resolvePlanningSetupWallet(user, c.Request.Method == http.MethodPost, modelID)
	if err != nil {
		planningError(c, errors.New("AI wallet setup could not be completed. Check the configuration in Settings before retrying."))
		return
	}
	// Creation is local and fast. Balance checks are explicit read-only GETs.
	if out.Found && c.Request.Method == http.MethodGet {
		balance, balanceErr := wallet.QueryUSDCBalanceCached(out.Address)
		if balanceErr == nil {
			out.BalanceUSDC, out.BalanceStatus = strconv.FormatFloat(balance, 'f', 6, 64), "ok"
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}
