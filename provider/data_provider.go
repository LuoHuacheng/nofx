package provider

import (
	"context"
	"errors"
	"fmt"
)

// QuantProvider supplies optional symbol-level market context. It is kept
// separate from the trading engine so callers can opt into it without
// changing the existing decision path.
type QuantProvider interface {
	GetDIYQuantData(ctx context.Context, symbol string) (*DIYQuantData, error)
}

// FallbackQuantProvider tries Primary first and Fallback only when the primary
// returns an error or empty data. Both errors are retained when both fail.
type FallbackQuantProvider struct {
	Primary  QuantProvider
	Fallback QuantProvider
}

func (p FallbackQuantProvider) GetDIYQuantData(ctx context.Context, symbol string) (*DIYQuantData, error) {
	if p.Primary == nil && p.Fallback == nil {
		return nil, errors.New("no quant providers configured")
	}
	if p.Primary != nil {
		data, err := p.Primary.GetDIYQuantData(ctx, symbol)
		if err == nil && data != nil {
			return data, nil
		}
		if p.Fallback == nil {
			if err == nil {
				return nil, errors.New("primary quant provider returned empty data")
			}
			return nil, fmt.Errorf("primary quant provider: %w", err)
		}
		fallbackData, fallbackErr := p.Fallback.GetDIYQuantData(ctx, symbol)
		if fallbackErr == nil && fallbackData != nil {
			return fallbackData, nil
		}
		if err == nil {
			err = errors.New("primary quant provider returned empty data")
		}
		if fallbackErr == nil {
			fallbackErr = errors.New("fallback quant provider returned empty data")
		}
		return nil, fmt.Errorf("primary quant provider: %w; fallback quant provider: %w", err, fallbackErr)
	}
	return p.Fallback.GetDIYQuantData(ctx, symbol)
}
