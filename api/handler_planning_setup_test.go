package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/crypto"
	"nofx/store"
)

func TestFrontierWalletSetupIsOwnerScopedIdempotentAndNeverExportsSecrets(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "setup.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = db.AutoMigrate(&store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st}
	if _, err = server.resolvePlanningSetupWallet("no-encryption", true, ""); err == nil {
		t.Fatal("unencrypted wallet creation accepted")
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(crypto.EnvRSAPrivateKey, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})))
	t.Setenv(crypto.EnvDataEncryptionKey, "frontier-local-test-storage-key")
	service, err := crypto.NewCryptoService()
	if err != nil {
		t.Fatal(err)
	}
	crypto.SetGlobalCryptoService(service)
	defer crypto.SetGlobalCryptoService(nil)

	t.Setenv("CLAW402_WALLET_KEY", "unchanged-fixture")
	alice, err := server.resolvePlanningSetupWallet("alice", true, "")
	if err != nil || !alice.Found || !alice.Enabled {
		t.Fatal(alice, err)
	}
	again, err := server.resolvePlanningSetupWallet("alice", true, alice.ModelID)
	if err != nil || alice.Address != again.Address {
		t.Fatal("wallet replaced", again, err)
	}
	bob, err := server.resolvePlanningSetupWallet("bob", false, "")
	if err != nil || bob.Found {
		t.Fatal("another owner's wallet exposed", bob, err)
	}
	if _, err = server.resolvePlanningSetupWallet("bob", true, alice.ModelID); err == nil {
		t.Fatal("cross-owner model accepted")
	}
	bob, err = server.resolvePlanningSetupWallet("bob", true, "")
	if err != nil || bob.Address == alice.Address {
		t.Fatal("cross-owner wallet adopted", bob, err)
	}
	if os.Getenv("CLAW402_WALLET_KEY") != "unchanged-fixture" {
		t.Fatal("global credentials overwritten")
	}
	models, _ := st.AIModel().List("alice")
	secret := models[0].APIKey.String()
	var stored string
	if err = db.Raw("SELECT api_key FROM ai_models WHERE id = ?", alice.ModelID).Scan(&stored).Error; err != nil || !strings.HasPrefix(stored, "ENC:v1:") || strings.Contains(stored, secret) {
		t.Fatal("wallet key not encrypted at rest")
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", "alice") })
	router.POST("/wallet", server.handlePlanningSetupWallet)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/wallet", strings.NewReader(`{}`)))
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "private_key") {
		t.Fatal("wallet response is not public-only", w.Code)
	}
	var response map[string]any
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response["address"] != alice.Address || response["balance_status"] != "unknown" {
		t.Fatal("unexpected public response", response)
	}
	// The legacy onboarding route must obey the same owner boundary. An orphan
	// wallet is not an invitation to transfer its key to the next signed-in user.
	orphan := &store.AIModel{ID: "orphan", UserID: "deleted-user", Provider: "claw402", APIKey: crypto.EncryptedString(secret)}
	if err = db.Create(orphan).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAW402_WALLET_ADDRESS", alice.Address)
	t.Chdir(t.TempDir())
	legacy := gin.New()
	legacy.Use(func(c *gin.Context) { c.Set("user_id", "carol") })
	legacy.GET("/current", server.handleCurrentBeginnerWallet)
	legacy.POST("/prepare", server.handleBeginnerOnboarding)
	w = httptest.NewRecorder()
	legacy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/current", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), alice.Address) {
		t.Fatal("legacy read exposed the environment wallet")
	}
	w = httptest.NewRecorder()
	legacy.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/prepare", nil))
	var prepared beginnerOnboardingResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &prepared) != nil || prepared.Address == alice.Address || prepared.Address == "" || prepared.EnvSaved || strings.Contains(w.Body.String(), "private_key") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("legacy onboarding leaked or adopted a wallet", w.Code)
	}
	var owner string
	db.Raw("SELECT user_id FROM ai_models WHERE id = ?", orphan.ID).Scan(&owner)
	if owner != "deleted-user" {
		t.Fatal("orphan wallet ownership changed")
	}
	carolModels, _ := st.AIModel().List("carol")
	if len(carolModels) != 1 {
		t.Fatal("legacy wallet not persisted")
	}
	var carolStored string
	db.Raw("SELECT api_key FROM ai_models WHERE id = ?", carolModels[0].ID).Scan(&carolStored)
	if !strings.HasPrefix(carolStored, "ENC:v1:") {
		t.Fatal("legacy wallet was not encrypted")
	}
	if os.Getenv("CLAW402_WALLET_KEY") != "unchanged-fixture" {
		t.Fatal("legacy route changed process credentials")
	}
	if _, err := os.Stat(".env"); !os.IsNotExist(err) {
		t.Fatal("legacy route wrote a plaintext environment file")
	}
	w = httptest.NewRecorder()
	legacy.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/prepare", nil))
	var reused beginnerOnboardingResponse
	if json.Unmarshal(w.Body.Bytes(), &reused) != nil || reused.Address != prepared.Address || !reused.ReusedExisting {
		t.Fatal("legacy setup replaced the current owner's wallet")
	}
	invalid := &store.AIModel{ID: "invalid", UserID: "invalid-user", Provider: "claw402", APIKey: crypto.EncryptedString("invalid-existing-fixture")}
	if err = db.Create(invalid).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = server.resolvePlanningSetupWallet("invalid-user", true, ""); err == nil {
		t.Fatal("invalid existing key silently replaced")
	}
}
