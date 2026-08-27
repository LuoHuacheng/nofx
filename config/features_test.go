package config

import "testing"

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
