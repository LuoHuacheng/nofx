package config

import "testing"

func TestAdaptiveScanIntervalFlag(t *testing.T) {
	SetFeatures(FeatureFlags{})
	if IsAdaptiveScanIntervalEnabled() {
		t.Fatal("adaptive scan interval should be disabled by default test configuration")
	}

	SetFeatures(FeatureFlags{EnableAdaptiveScanInterval: true})
	if !IsAdaptiveScanIntervalEnabled() {
		t.Fatal("adaptive scan interval should be enabled when configured")
	}
}

func TestDIYQuantFallbackFlag(t *testing.T) {
	SetFeatures(FeatureFlags{})
	if IsDIYQuantFallbackEnabled() {
		t.Fatal("DIY quant fallback should be disabled by default test configuration")
	}

	SetFeatures(FeatureFlags{EnableDIYQuantFallback: true})
	if !IsDIYQuantFallbackEnabled() {
		t.Fatal("DIY quant fallback should be enabled when configured")
	}
}

func TestClaw402AndVergexFlags(t *testing.T) {
	// Defaults: both on
	SetFeatures(DefaultFeatureFlags())
	if !IsClaw402DataEnabled() {
		t.Fatal("claw402 data routing should be enabled by default")
	}
	if !IsVergexDataEnabled() {
		t.Fatal("vergex should be enabled by default")
	}

	// claw402 off -> vergex off too (Vergex is paid via claw402)
	SetFeatures(FeatureFlags{EnableClaw402Data: false, EnableVergexSignals: true})
	if IsClaw402DataEnabled() {
		t.Fatal("claw402 flag off must disable claw402 data routing")
	}
	if IsVergexDataEnabled() {
		t.Fatal("vergex must be disabled when claw402 data routing is off (cascade)")
	}

	// vergex off alone -> vergex off, claw402 stays on
	SetFeatures(FeatureFlags{EnableClaw402Data: true, EnableVergexSignals: false})
	if IsVergexDataEnabled() {
		t.Fatal("vergex flag off must disable vergex")
	}
	if !IsClaw402DataEnabled() {
		t.Fatal("vergex flag off must not disable claw402")
	}
}
