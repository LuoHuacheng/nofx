package provider

import (
	"context"
	"errors"
	"testing"
)

type fakeQuantProvider struct {
	data  *DIYQuantData
	err   error
	calls int
}

func (f *fakeQuantProvider) GetDIYQuantData(context.Context, string) (*DIYQuantData, error) {
	f.calls++
	return f.data, f.err
}

func TestFallbackQuantProvider_UsesPrimaryWhenAvailable(t *testing.T) {
	primary := &fakeQuantProvider{data: &DIYQuantData{Symbol: "BTCUSDT"}}
	fallback := &fakeQuantProvider{data: &DIYQuantData{Symbol: "ETHUSDT"}}
	provider := FallbackQuantProvider{Primary: primary, Fallback: fallback}

	got, err := provider.GetDIYQuantData(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Symbol != "BTCUSDT" {
		t.Fatalf("got symbol %q, want BTCUSDT", got.Symbol)
	}
	if primary.calls != 1 || fallback.calls != 0 {
		t.Fatalf("calls primary=%d fallback=%d, want 1/0", primary.calls, fallback.calls)
	}
}

func TestFallbackQuantProvider_UsesFallbackAfterPrimaryError(t *testing.T) {
	primaryErr := errors.New("primary unavailable")
	primary := &fakeQuantProvider{err: primaryErr}
	fallback := &fakeQuantProvider{data: &DIYQuantData{Symbol: "BTCUSDT"}}
	provider := FallbackQuantProvider{Primary: primary, Fallback: fallback}

	got, err := provider.GetDIYQuantData(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Symbol != "BTCUSDT" {
		t.Fatalf("got symbol %q, want BTCUSDT", got.Symbol)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls primary=%d fallback=%d, want 1/1", primary.calls, fallback.calls)
	}
}

func TestFallbackQuantProvider_ReturnsBothErrors(t *testing.T) {
	primaryErr := errors.New("primary unavailable")
	fallbackErr := errors.New("fallback unavailable")
	provider := FallbackQuantProvider{
		Primary:  &fakeQuantProvider{err: primaryErr},
		Fallback: &fakeQuantProvider{err: fallbackErr},
	}

	_, err := provider.GetDIYQuantData(context.Background(), "BTCUSDT")
	if err == nil || !errors.Is(err, primaryErr) || !errors.Is(err, fallbackErr) {
		t.Fatalf("error %v does not wrap both provider errors", err)
	}
}

func TestCalculateMarketSentiment(t *testing.T) {
	cases := []struct {
		name                         string
		priceChange, oiChange, ratio float64
		want                         string
	}{
		{"bullish", 3, 2, 60, "bullish"},
		{"bearish", -3, 2, 40, "bearish"},
		{"neutral", 0.1, 0.1, 50, "neutral"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := calculateMarketSentiment(tc.priceChange, tc.oiChange, tc.ratio); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
