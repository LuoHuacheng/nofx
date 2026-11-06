package harness

import (
	"context"
	"testing"

	"nofx/planning/core"
	"nofx/planning/model"
)

type previewMarket struct{ textMarketFixture }

func (s previewMarket) Snapshot(ctx context.Context, venue, symbol string) (core.Snapshot, error) {
	book, err := s.textMarketFixture.Snapshot(ctx, venue, symbol)
	book.Venue, book.Symbol = venue, symbol
	return book, err
}

type previewDrafts struct {
	textDraftFixture
	previous map[string]core.Record
}

func (s *previewDrafts) Records() map[string]core.Record { return s.previous }

func TestEquityPreviewPlansRepeatedlyOnEveryVenue(t *testing.T) {
	for venue, symbol := range map[string]string{"hyperliquid": "BTC", "binance": "BTCUSDT", "okx": "BTC-USDT-SWAP", "bitget": "BTCUSDT"} {
		t.Run(venue, func(t *testing.T) {
			limits := core.Constraints{PlanningOnly: true, SizingPolicy: core.SizingAccountMargin, Leverage: 5, AvailableMargin: "0", Equity: "259.47", MaxRisk: "259.47", MaxNotional: "1200", MinRewardRisk: "0", FeeBPS: "10", SlippageBPS: "10"}
			store := &previewDrafts{previous: map[string]core.Record{}}
			for turn := 0; turn < 2; turn++ {
				gateway := &textGatewayFixture{}
				source := previewMarket{}
				engine := Engine{Model: &model.NOFXAdapter{Client: gateway, TextTools: true}, Store: store, Market: source, Charts: source, Limits: limits}
				input := Input{Message: "Compare entry plans", Venue: venue, Symbol: symbol, Interval: "1h", CompareStructures: true}
				if turn == 1 {
					// Revising a previously funded draft cannot hide today's zero margin.
					previous := store.plans[0]
					previous.Constraints.PlanningOnly = false
					previous.Constraints.AvailableMargin = "100"
					store.previous[previous.ID] = core.Record{Plan: previous, State: "draft"}
					input.PlanID = previous.ID
				}
				notice, outcome := false, false
				before := len(store.plans)
				err := engine.Run(context.Background(), input, func(event Event) error {
					if event.Type == "planning_sizing" {
						notice = event.Data.(map[string]any)["available_margin"] == core.Decimal("0")
					}
					outcome = outcome || event.Type == "outcome"
					return nil
				})
				if err != nil || !notice || !outcome || len(store.plans)-before != 4 || gateway.calls != 2 {
					t.Fatalf("turn %d failed: %v notice=%v outcome=%v drafts=%d model_calls=%d", turn, err, notice, outcome, len(store.plans)-before, gateway.calls)
				}
				for _, plan := range store.plans[before:] {
					if !plan.Constraints.PlanningOnly || plan.Constraints.AvailableMargin != "0" {
						t.Fatal("preview lost its unfunded status")
					}
				}
			}
		})
	}
}
