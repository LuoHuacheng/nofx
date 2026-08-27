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
