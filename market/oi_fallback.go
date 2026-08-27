package market

import (
	"context"

	"nofx/config"
	"nofx/provider"
)

// getOpenInterestWithFallback is the production adapter used by market data.
func getOpenInterestWithFallback(symbol string) *OIData {
	return resolveOpenInterest(
		symbol,
		getOpenInterestData,
		func(symbol string) (*provider.DIYQuantData, error) {
			return provider.GetDIYQuantData(context.Background(), symbol)
		},
		config.IsDIYQuantFallbackEnabled(),
	)
}

// resolveOpenInterest preserves the existing zero-value behavior while making
// the DIY provider an explicit opt-in fallback.
func resolveOpenInterest(
	symbol string,
	primary func(string) (*OIData, error),
	fallback func(string) (*provider.DIYQuantData, error),
	fallbackEnabled bool,
) *OIData {
	if data, err := primary(symbol); err == nil && data != nil {
		return data
	}
	if fallbackEnabled && fallback != nil {
		if data, err := fallback(symbol); err == nil && data != nil && data.OI != nil {
			return &OIData{
				Latest:  data.OI.Current,
				Average: data.OI.Current * 0.999,
			}
		}
	}
	return &OIData{Latest: 0, Average: 0}
}
