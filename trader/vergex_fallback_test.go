package trader

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"nofx/store"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newStrategyFallbackStore builds an in-memory store with strategies and
// AI models tables for the fallback tests.
func newStrategyFallbackStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:fallback-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&store.Strategy{}, &store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func mustStrategyJSON(t *testing.T, sourceType string, staticCoins []string) string {
	t.Helper()
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = sourceType
	cfg.CoinSource.StaticCoins = staticCoins
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func insertStrategy(t *testing.T, st *store.Store, id, userID, name string, isActive, isDefault bool, cfgJSON string) {
	t.Helper()
	if err := st.Strategy().Create(&store.Strategy{
		ID: id, UserID: userID, Name: name, IsActive: isActive, IsDefault: isDefault, Config: cfgJSON,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveStrategyWithVergexFallback(t *testing.T) {
	st := newStrategyFallbackStore(t)

	// User strategies: one vergex (bound to trader), one active static.
	vergexJSON := mustStrategyJSON(t, "vergex_signal", []string{"BTCUSDT"})
	staticActive := mustStrategyJSON(t, "static", []string{"ETHUSDT"})
	insertStrategy(t, st, "s-vergex", "u1", "Vergex Auto", false, false, vergexJSON)
	insertStrategy(t, st, "s-static", "u1", "Static Active", true, false, staticActive)

	traderCfg := &store.Trader{ID: "t1", UserID: "u1", Name: "t1", StrategyID: "s-vergex"}

	// vergex config as loaded from the DB.
	bound, err := st.Strategy().Get("u1", "s-vergex")
	if err != nil {
		t.Fatal(err)
	}
	boundCfg, err := bound.ParseConfig()
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: no claw402 wallet -> falls back to the active static strategy.
	gotCfg, gotRaw := ResolveStrategyWithVergexFallback(st, traderCfg.UserID, boundCfg, bound.Config)
	if gotCfg == nil || gotCfg.CoinSource.SourceType != "static" {
		t.Fatalf("expected fallback to static strategy, got source=%q", sourceTypeOf(gotCfg))
	}
	if gotRaw != staticActive {
		t.Fatalf("raw config mismatch after fallback")
	}

	// Case 2: claw402 wallet configured -> vergex kept.
	if err := st.AIModel().Create("u1", "m-claw", "claw402", "claw402", true, "0xwalletkey", ""); err != nil {
		t.Fatal(err)
	}
	gotCfg, gotRaw = ResolveStrategyWithVergexFallback(st, traderCfg.UserID, boundCfg, bound.Config)
	if gotCfg == nil || gotCfg.CoinSource.SourceType != "vergex_signal" {
		t.Fatalf("expected vergex kept when wallet configured, got %q", sourceTypeOf(gotCfg))
	}
	if gotRaw != bound.Config {
		t.Fatalf("raw config should stay the vergex one")
	}
}

func TestResolveStrategyWithVergexFallbackDefaultAndNone(t *testing.T) {
	st := newStrategyFallbackStore(t)

	// Only a system default static strategy exists (no active user strategy).
	staticDefault := mustStrategyJSON(t, "static", []string{"BTCUSDT", "ETHUSDT"})
	insertStrategy(t, st, "s-def", "system", "System Default", false, true, staticDefault)

	traderCfg := &store.Trader{ID: "t2", UserID: "u2", Name: "t2", StrategyID: "s-vergex"}
	// The trader's bound vergex strategy (u2 has no active strategy).
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "vergex_signal"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}
	b, _ := json.Marshal(cfg)
	bound := &store.Strategy{ID: "s-vergex", UserID: "u2", Name: "V", Config: string(b)}
	boundCfg, _ := bound.ParseConfig()
	gotCfg, _ := ResolveStrategyWithVergexFallback(st, traderCfg.UserID, boundCfg, bound.Config)
	if gotCfg == nil || gotCfg.CoinSource.SourceType != "static" {
		t.Fatalf("expected fallback to system default static, got %q", sourceTypeOf(gotCfg))
	}
}

func TestResolveStrategyKeepsNonVergex(t *testing.T) {
	st := newStrategyFallbackStore(t)
	staticJSON := mustStrategyJSON(t, "static", []string{"BTCUSDT"})
	insertStrategy(t, st, "s-static", "u3", "Static", true, false, staticJSON)

	traderCfg := &store.Trader{ID: "t3", UserID: "u3", Name: "t3", StrategyID: "s-static"}
	bound, err := st.Strategy().Get("u3", "s-static")
	if err != nil {
		t.Fatal(err)
	}
	boundCfg, err := bound.ParseConfig()
	if err != nil {
		t.Fatal(err)
	}
	gotCfg, gotRaw := ResolveStrategyWithVergexFallback(st, traderCfg.UserID, boundCfg, bound.Config)
	if gotCfg.CoinSource.SourceType != "static" {
		t.Fatalf("non-vergex strategy must pass through untouched")
	}
	if gotRaw != bound.Config {
		t.Fatalf("raw must be untouched")
	}
}

// TestResolveStrategyNoUsableFallback verifies the original vergex
// strategy is kept (and the engine reports the wallet error) when no
// non-vergex strategy exists at all.
func TestResolveStrategyNoUsableFallback(t *testing.T) {
	st := newStrategyFallbackStore(t)
	vergexJSON := mustStrategyJSON(t, "vergex_signal", []string{"BTCUSDT"})
	insertStrategy(t, st, "s-v1", "u4", "V1", false, false, vergexJSON)
	insertStrategy(t, st, "s-v2", "u4", "V2", true, false, vergexJSON)

	traderCfg := &store.Trader{ID: "t4", UserID: "u4", Name: "t4", StrategyID: "s-v1"}
	bound, err := st.Strategy().Get("u4", "s-v1")
	if err != nil {
		t.Fatal(err)
	}
	boundCfg, err := bound.ParseConfig()
	if err != nil {
		t.Fatal(err)
	}
	gotCfg, gotRaw := ResolveStrategyWithVergexFallback(st, traderCfg.UserID, boundCfg, bound.Config)
	if gotCfg == nil || gotCfg.CoinSource.SourceType != "vergex_signal" {
		t.Fatalf("expected vergex kept when no fallback exists")
	}
	if gotRaw != bound.Config {
		t.Fatalf("raw should stay the bound vergex config")
	}
}

func sourceTypeOf(cfg *store.StrategyConfig) string {
	if cfg == nil {
		return "<nil>"
	}
	return cfg.CoinSource.SourceType
}
