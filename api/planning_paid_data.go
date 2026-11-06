package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"net/http"
	"nofx/planning/core"
	"nofx/planning/instrument"
	"nofx/planning/plugins"
	"nofx/provider/nofxos"
	"nofx/provider/vergex"
	"strings"
	"sync"
	"time"
)

// No paid request is retried here. Errors are retained for the current budget
// window too, preventing the model from blindly resending an ambiguous payment.
func (s *Server) planningPaidData(user string) func(context.Context, string, plugins.Request) ([]byte, error) {
	type entry struct {
		raw []byte
		err error
		at  time.Time
	}
	cache := map[string]entry{}
	var mu sync.Mutex
	return func(ctx context.Context, id string, q plugins.Request) ([]byte, error) {
		if id == "hyperdata_profit" {
			return nil, errors.New("Profit leaderboard is unavailable: the Claw402 catalog does not publish a paid endpoint. Public-origin fallback is disabled.")
		}
		mu.Lock()
		defer mu.Unlock()
		k := id + "/" + q.Symbol
		if v, ok := cache[k]; ok && time.Since(v.at) < 5*time.Minute {
			return append([]byte(nil), v.raw...), v.err
		}
		key, err := s.resolveStrategyDataWalletKey(user, "")
		if err != nil || key == "" {
			return nil, errors.New("Configure a Claw402 fee wallet in NOFX settings to use paid data")
		}
		client, err := vergex.NewClient("", key, nil)
		if err != nil {
			return nil, errors.New("Invalid Claw402 fee wallet")
		}
		kind := "core_perp"
		if strings.Contains(q.Symbol, ":") {
			kind = "hip3_perp"
		}
		var raw []byte
		switch id {
		case "hyperdata_cost":
			raw, err = client.GetCostLiquidationHeatmap(ctx, vergex.Query{MarketType: kind, Symbol: q.Symbol, Chain: "mainnet"})
		case "hyperdata_winrate":
			raw, err = client.GetHolderWinrateMap(ctx, vergex.WinrateQuery{MarketType: kind, Symbol: q.Symbol, Chain: "mainnet"})
		default:
			return nil, errors.New("Unsupported paid planning data component")
		}
		if err != nil {
			err = errors.New("Claw402 data request failed. Reconcile payment status before retrying; no automatic retry was made.")
		}
		if len(cache) >= 64 {
			clear(cache)
		}
		cache[k] = entry{append([]byte(nil), raw...), err, time.Now()}
		return raw, err
	}
}

type planningAI500Cache struct {
	mu    sync.Mutex
	coins []nofxos.CoinData
	at    time.Time
	err   error
}

// Reuse only this user's already purchased board. Planning must not purchase
// additional data or fetch the unpaid origin to determine the entry policy.
func (p *planningServices) planningEntryPolicy(user, venue, symbol, group string, limits core.Constraints) core.Constraints {
	if venue != "binance" {
		return limits
	}
	if group == "ai500" {
		limits.EntryPolicy = core.EntryAI500TrendLong
		return limits
	}
	value, ok := p.ai500.Load(user)
	if !ok {
		return limits
	}
	cached := value.(*planningAI500Cache)
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if cached.err != nil || cached.at.IsZero() || time.Since(cached.at) >= 5*time.Minute {
		return limits
	}
	for _, coin := range cached.coins {
		if strings.EqualFold(coin.Pair, strings.TrimSpace(symbol)) {
			limits.EntryPolicy = core.EntryAI500TrendLong
			break
		}
	}
	return limits
}

func (s *Server) handlePlanningAI500(c *gin.Context) {
	user := c.GetString("user_id")
	key, err := s.resolveStrategyDataWalletKey(user, "")
	if err != nil || key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Configure a Claw402 fee wallet to load AI500"})
		return
	}
	p, err := s.planningService()
	if err != nil {
		planningError(c, err)
		return
	}
	value, _ := p.ai500.LoadOrStore(user, &planningAI500Cache{})
	cached := value.(*planningAI500Cache)
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if cached.at.IsZero() || time.Since(cached.at) >= 5*time.Minute {
		fetch := p.ai500Read
		if fetch == nil {
			fetch = func(ctx context.Context, key string) ([]byte, error) {
				client, e := nofxos.NewClaw402DataClient("", key, nil)
				if e != nil {
					return nil, errors.New("Invalid Claw402 fee wallet")
				}
				return client.DoRequestContext(ctx, "/api/ai500/list")
			}
		}
		raw, e := fetch(c.Request.Context(), key)
		coins := []nofxos.CoinData{}
		if e == nil {
			var envelope nofxos.AI500Response
			if json.Unmarshal(raw, &envelope) != nil || !envelope.Success {
				e = errors.New("Invalid Claw402 AI500 response")
			} else {
				for _, coin := range envelope.Data.Coins {
					coin.Pair = strings.ToUpper(strings.TrimSpace(coin.Pair))
					if instrument.ValidSymbol(coin.Pair) && strings.HasSuffix(coin.Pair, "USDT") {
						coins = append(coins, coin)
					}
				}
			}
		}
		cached.coins = coins
		cached.err = e
		cached.at = time.Now().UTC()
	}
	if cached.err != nil {
		planningError(c, errors.New("AI500 request failed; check Claw402 payment status. Automatic retry is disabled for five minutes."))
		return
	}
	c.JSON(200, gin.H{"coins": cached.coins, "source": "https://claw402.ai/api/v1/nofx/ai500/list", "fetched_at": cached.at, "cache_seconds": 300})
}
