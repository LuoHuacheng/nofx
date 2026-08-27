package market

import (
	"errors"
	"testing"

	"nofx/provider"
)

func TestResolveOpenInterest_PrimaryWins(t *testing.T) {
	fallbackCalls := 0
	got := resolveOpenInterest(
		"BTCUSDT",
		func(string) (*OIData, error) { return &OIData{Latest: 42, Average: 41}, nil },
		func(string) (*provider.DIYQuantData, error) {
			fallbackCalls++
			return &provider.DIYQuantData{OI: &provider.DIYOIData{Current: 99}}, nil
		},
		true,
	)
	if got.Latest != 42 || fallbackCalls != 0 {
		t.Fatalf("got %+v, fallback calls=%d; expected primary and no fallback", got, fallbackCalls)
	}
}

func TestResolveOpenInterest_FallsBackToDIY(t *testing.T) {
	primaryErr := errors.New("binance unavailable")
	got := resolveOpenInterest(
		"BTCUSDT",
		func(string) (*OIData, error) { return nil, primaryErr },
		func(string) (*provider.DIYQuantData, error) {
			return &provider.DIYQuantData{OI: &provider.DIYOIData{Current: 99}}, nil
		},
		true,
	)
	if got.Latest != 99 || got.Average != 99*0.999 {
		t.Fatalf("got %+v, want DIY OI mapped to latest/average", got)
	}
}

func TestResolveOpenInterest_DisabledUsesZeroDefaults(t *testing.T) {
	fallbackCalls := 0
	got := resolveOpenInterest(
		"BTCUSDT",
		func(string) (*OIData, error) { return nil, errors.New("primary unavailable") },
		func(string) (*provider.DIYQuantData, error) {
			fallbackCalls++
			return &provider.DIYQuantData{OI: &provider.DIYOIData{Current: 99}}, nil
		},
		false,
	)
	if got.Latest != 0 || got.Average != 0 || fallbackCalls != 0 {
		t.Fatalf("got %+v, fallback calls=%d; expected zero defaults and no fallback", got, fallbackCalls)
	}
}

func TestResolveOpenInterest_FallbackWithoutOIUsesZeroDefaults(t *testing.T) {
	got := resolveOpenInterest(
		"BTCUSDT",
		func(string) (*OIData, error) { return nil, errors.New("primary unavailable") },
		func(string) (*provider.DIYQuantData, error) {
			return &provider.DIYQuantData{}, nil
		},
		true,
	)
	if got.Latest != 0 || got.Average != 0 {
		t.Fatalf("got %+v, want zero defaults", got)
	}
}
