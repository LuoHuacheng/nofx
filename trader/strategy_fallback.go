package trader

import (
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
// unusable), fall back to the user's active non-vergex strategy, then
// the system default, then any non-vergex strategy. When nothing
// suitable exists, the original strategy is kept and the engine reports
// the concrete claw402-wallet error at runtime.
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

	// 1) The user's active strategy, when it is usable.
	if active, err := st.Strategy().GetActive(userID); err == nil {
		if cfg, raw, ok := parseNonVergexStrategy(active); ok {
			logger.Infof("↘️ Trader (user %s) fell back to active strategy \"%s\"", userID, active.Name)
			return cfg, raw
		}
	}
	// 2) The system default strategy, when it is usable.
	if def, err := st.Strategy().GetDefault(); err == nil {
		if cfg, raw, ok := parseNonVergexStrategy(def); ok {
			logger.Infof("↘️ Trader (user %s) fell back to default strategy \"%s\"", userID, def.Name)
			return cfg, raw
		}
	}
	// 3) Any other non-vergex strategy of the user.
	if list, err := st.Strategy().List(userID); err == nil {
		for _, s := range list {
			if cfg, raw, ok := parseNonVergexStrategy(s); ok {
				logger.Infof("↘️ Trader (user %s) fell back to strategy \"%s\"", userID, s.Name)
				return cfg, raw
			}
		}
	}

	logger.Warnf("⚠️ Trader (user %s): no non-vergex strategy found; keeping vergex_signal (requires a claw402 wallet)", userID)
	return strategyConfig, strategyConfigRaw
}
