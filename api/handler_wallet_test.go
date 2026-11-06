package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	nofxcrypto "nofx/crypto"
	"nofx/store"
)

func TestWalletBalanceDoesNotTreatFailureAsZero(t *testing.T) {
	for _, tc := range []struct {
		name, address string
		fail          bool
		status        int
	}{
		{"invalid address", "bad", false, 400},
		{"unavailable", "0x0000000000000000000000000000000000000001", true, 503},
		{"empty wallet", "0x0000000000000000000000000000000000000001", false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/wallet/balance?address="+tc.address, nil)
			calls := 0
			writeWalletBalance(c, func(address string) (float64, error) {
				calls++
				if tc.fail {
					return 0, errors.New("unreachable")
				}
				return 0, nil
			})
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
			if tc.status == 400 && calls != 0 {
				t.Fatal("invalid address reached RPC")
			}
			if tc.fail && strings.Contains(w.Body.String(), "balance_usdc") {
				t.Fatal("invented balance on failure")
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), `"balance_usdc":"0.000000"`) {
				t.Fatal("real zero not returned")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("balance must not be browser cached")
			}
		})
	}
}

func TestWalletGenerationAndImportDerivation(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	s.handleWalletGenerate(c)
	var generated walletGenerateResponse
	if json.Unmarshal(w.Body.Bytes(), &generated) != nil || w.Code != 200 {
		t.Fatal("wallet generation failed")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private key response must not be cached")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(generated.PrivateKey, "0x"))
	if err != nil || crypto.PubkeyToAddress(key.PublicKey).Hex() != generated.Address {
		t.Fatal("address does not match generated key")
	}
	for _, valid := range []bool{true, false} {
		privateKey := generated.PrivateKey
		if !valid {
			privateKey = "0x" + strings.Repeat("0", 64)
		}
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		body, _ := json.Marshal(walletValidateRequest{PrivateKey: privateKey, DeriveOnly: true})
		c.Request = httptest.NewRequest(http.MethodPost, "/wallet/validate", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		s.handleWalletValidate(c)
		var result walletValidateResponse
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Valid != valid {
			t.Fatal("incorrect validation result")
		}
		if valid && result.Address != generated.Address {
			t.Fatal("incorrect derived address")
		}
		if strings.Contains(w.Body.String(), privateKey) {
			t.Fatal("import response contains a private key")
		}
		if result.BalanceUSDC != "" {
			t.Fatal("derivation must not query funding or require external services")
		}
	}
}

func TestWalletExportIsExplicitOwnerScopedAndUncached(t *testing.T) {
	s, _, _ := closeFixture(t)
	db := s.store.GormDB()
	if err := db.AutoMigrate(&store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	pk, _ := crypto.GenerateKey()
	key := "0x" + fmt.Sprintf("%x", crypto.FromECDSA(pk))
	address := crypto.PubkeyToAddress(pk.PublicKey).Hex()
	for _, m := range []store.AIModel{
		{ID: "owned", UserID: "alice", Provider: "claw402", APIKey: nofxcrypto.EncryptedString(key)},
		{ID: "foreign", UserID: "bob", Provider: "claw402", APIKey: nofxcrypto.EncryptedString(key)},
		{ID: "shared", UserID: "default", Provider: "claw402", APIKey: nofxcrypto.EncryptedString(key)},
		{ID: "other-provider", UserID: "alice", Provider: "deepseek", APIKey: nofxcrypto.EncryptedString(key)},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, user, model, address string
		status                     int
	}{
		{"owner", "alice", "owned", address, 200},
		{"anonymous", "", "owned", address, 401},
		{"foreign", "alice", "foreign", address, 404},
		{"default fallback forbidden", "alice", "shared", address, 404},
		{"other provider", "alice", "other-provider", address, 404},
		{"wallet changed", "alice", "owned", "0x0000000000000000000000000000000000000001", 409},
		{"missing selection", "alice", "", address, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("user_id", tc.user)
			body, _ := json.Marshal(map[string]string{"model_id": tc.model, "address": tc.address})
			c.Request = httptest.NewRequest("POST", "/wallet/export", strings.NewReader(string(body)))
			c.Request.Header.Set("Content-Type", "application/json")
			s.handleWalletExport(c)
			if w.Code != tc.status {
				t.Fatalf("wrong export status: %d", w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("secret response is cacheable")
			}
			if tc.status == 200 {
				var out walletGenerateResponse
				json.Unmarshal(w.Body.Bytes(), &out)
				if out.PrivateKey != key || out.Address != address {
					t.Fatal("wrong backup")
				}
			} else if strings.Contains(w.Body.String(), key) {
				t.Fatal("secret leaked in rejected export")
			}
		})
	}
	var count int64
	db.Model(&store.AIModel{}).Count(&count)
	if count != 4 {
		t.Fatal("export modified wallets")
	}
}
