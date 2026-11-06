package api

import (
	"errors"
	"nofx/store"
	"nofx/trader/types"
)

// Keep account inspection independent of autonomous-trader loading. A finite
// planning lease deliberately prevents that loader from creating a trader.
// This interface cannot submit, cancel, or change account settings.
type accountReader interface {
	GetPositions() ([]map[string]interface{}, error)
	GetOpenOrders(string) ([]types.OpenOrder, error)
}

func (s *Server) accountReadConfig(user, traderID string, exchangeIDs ...string) (*store.Exchange, error) {
	if user == "" {
		return nil, errors.New("Authentication is required")
	}
	if len(exchangeIDs) > 0 && exchangeIDs[0] != "" {
		if traderID != "" {
			bound, err := s.accountReadConfig(user, traderID)
			if err != nil || bound.ID != exchangeIDs[0] {
				return nil, errors.New("Trader does not belong to the selected account")
			}
		}
		ex, err := s.store.Exchange().GetByID(user, exchangeIDs[0])
		if err != nil || !ex.Enabled {
			return nil, errors.New("Trading account is unavailable or disabled")
		}
		if (ex.ExchangeType == "binance" || ex.ExchangeType == "okx" || ex.ExchangeType == "bitget") && ex.Testnet {
			return nil, errors.New("Testnet account inspection is unsupported; mainnet fallback is disabled")
		}
		return ex, nil
	}
	owned, err := s.store.Trader().List(user)
	if err != nil {
		return nil, errors.New("Could not verify account ownership")
	}
	for _, t := range owned {
		if traderID != "" && t.ID != traderID {
			continue
		}
		ex, err := s.store.Exchange().GetByID(user, t.ExchangeID)
		if err != nil || !ex.Enabled {
			return nil, errors.New("Trading account is unavailable or disabled")
		}
		if (ex.ExchangeType == "binance" || ex.ExchangeType == "okx" || ex.ExchangeType == "bitget") && ex.Testnet {
			return nil, errors.New("Testnet account inspection is unsupported; mainnet fallback is disabled")
		}
		return ex, nil
	}
	return nil, errors.New("Trader does not belong to this account")
}

func (s *Server) accountReadClient(user, traderID string, exchangeIDs ...string) (accountReader, error) {
	ex, err := s.accountReadConfig(user, traderID, exchangeIDs...)
	if err != nil {
		return nil, err
	}
	return buildExchangeProbeTrader(ex, user)
}
