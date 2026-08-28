package trader

import (
	"encoding/json"
	"strings"

	"nofx/config"
	"nofx/logger"
	"nofx/store"
)

// isVergexSource reports whether a coin source is the paid Vergex
// signal source (which requires a configured claw402 wallet).
func isVergexSource(sourceType string) bool {
	return strings.EqualFold(strings.TrimSpace(sourceType), "vergex_signal")
}

// parseNonVergexStrategy parses a strategy and returns its config only
// when it is NOT a vergex_signal strategy (a usable fallback).
func parseNonVergexStrategy(s *store.Strategy) (*store.StrategyConfig, string, bool) {
	if s == nil {
		return nil, "", false
	}
	cfg, err := s.ParseConfig()
	if err != nil || isVergexSource(cfg.CoinSource.SourceType) {
		return nil, "", false
	}
	cfg.ClampLimits()
	return cfg, s.Config, true
}

// ResolveStrategyWithVergexFallback implements the strategy priority
// order: keep the trader's vergex_signal strategy while a claw402 wallet
// is configured; when the wallet is missing (vergex data source
// unusable), fall back to the user's active non-vergex strategy. When no
// active non-vergex strategy exists, fall back to a synthesized
// hyper_main strategy (Hyperliquid 24h volume top-N) so the trader keeps
// producing dynamic candidates without a paid wallet.
//
// It is applied both when a trader is loaded from the store and when the
// running trader reloads its strategy from the DB, so a runtime refresh
// cannot silently undo the fallback.
func ResolveStrategyWithVergexFallback(st *store.Store, userID string, strategyConfig *store.StrategyConfig, strategyConfigRaw string) (*store.StrategyConfig, string) {
	if st == nil || strategyConfig == nil || !isVergexSource(strategyConfig.CoinSource.SourceType) {
		return strategyConfig, strategyConfigRaw
	}
	if !config.IsVergexDataEnabled() {
		// Vergex disabled via FEATURE_VERGEX_ENABLED=false (or claw402 data
		// routing off) — treat the paid source as unusable and fall back.
	} else if walletKey, err := st.AIModel().ResolveClaw402WalletKey(userID, ""); err == nil && walletKey != "" {
		return strategyConfig, strategyConfigRaw // wallet available, keep vergex
	}

	logger.Warnf("⚠️ Trader (user %s) uses vergex_signal but the Vergex data source is unavailable — falling back to a non-vergex strategy", userID)

	// 1) The user's active strategy, when it is an explicit choice.
	// GetActive falls back to the system default strategy when the user has
	// no active one — that placeholder is not an explicit choice and must not
	// win over the hyper_main default, so it is skipped here.
	if active, err := st.Strategy().GetActive(userID); err == nil && active != nil && !active.IsDefault {
		if cfg, raw, ok := parseNonVergexStrategy(active); ok {
			logger.Infof("↘️ Trader (user %s) fell back to active strategy \"%s\"", userID, active.Name)
			return cfg, raw
		}
	}

	// 2) Fallback: synthesize a hyper_main strategy (24h volume top 30,
	// Hyperliquid public API, no claw402 wallet required) so the trader
	// keeps a dynamic candidate pool instead of stalling on a dead source.
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "hyper_main"
	cfg.CoinSource.UseHyperMain = true
	cfg.CoinSource.HyperMainLimit = 30
	cfg.NormalizeProductSchema()
	cfg.ClampLimits()
	raw, err := json.Marshal(cfg)
	if err != nil {
		raw = []byte(`{"strategy_type":"ai_trading","coin_source":{"source_type":"hyper_main","use_hyper_main":true,"hyper_main_limit":30}}`)
	}
	logger.Infof("↘️ Trader (user %s) has no usable non-vergex strategy; defaulting to hyper_main (Hyperliquid top 30 by 24h volume)", userID)
	return &cfg, string(raw)
}
