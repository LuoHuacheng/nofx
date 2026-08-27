package market

import (
	"testing"
	"time"
)

func TestOrderBookMonitor_DetectsBuyImbalance(t *testing.T) {
	monitor := NewOrderBookMonitor("BTCUSDT")
	monitor.UpdatePrice(100)
	monitor.UpdateOrderBook(80, 20)

	triggers := monitor.CheckTriggers()
	if len(triggers) != 1 || triggers[0].TriggerType != "order_imbalance" {
		t.Fatalf("got %+v, want one order_imbalance trigger", triggers)
	}
	if triggers[0].Severity <= 0 || triggers[0].Severity > 1 {
		t.Fatalf("severity=%v outside (0,1]", triggers[0].Severity)
	}
	if dominated, ok := triggers[0].Details["buy_dominated"].(bool); !ok || !dominated {
		t.Fatalf("details buy_dominated=%v, want true", triggers[0].Details["buy_dominated"])
	}
}

func TestOrderBookMonitor_CooldownSuppressesDuplicate(t *testing.T) {
	monitor := NewOrderBookMonitor("BTCUSDT")
	monitor.SetCooldown(time.Hour)
	monitor.UpdateOrderBook(90, 10)
	if got := len(monitor.CheckTriggers()); got != 1 {
		t.Fatalf("first check got %d triggers, want 1", got)
	}
	if got := len(monitor.CheckTriggers()); got != 0 {
		t.Fatalf("second check got %d triggers during cooldown, want 0", got)
	}
}

func TestOrderBookMonitor_DetectsVolumeSpike(t *testing.T) {
	monitor := NewOrderBookMonitor("BTCUSDT")
	for i := 0; i < 20; i++ {
		monitor.UpdateVolume(100)
	}
	monitor.UpdatePrice(100)
	monitor.UpdateVolume(300)

	triggers := monitor.CheckTriggers()
	for _, trigger := range triggers {
		if trigger.TriggerType == "volume_spike" {
			if trigger.VolumeRatio < 2 {
				t.Fatalf("volume ratio=%v, want >=2", trigger.VolumeRatio)
			}
			return
		}
	}
	t.Fatalf("got %+v, want volume_spike trigger", triggers)
}

func TestOrderBookMonitor_DetectsPriceMove(t *testing.T) {
	monitor := NewOrderBookMonitor("BTCUSDT")
	monitor.SetCooldown(0)
	for i := 0; i < 12; i++ {
		monitor.UpdatePrice(100)
	}
	monitor.UpdatePrice(101)

	triggers := monitor.CheckTriggers()
	for _, trigger := range triggers {
		if trigger.TriggerType == "price_movement" {
			if trigger.PriceChange2Min <= 0 {
				t.Fatalf("price change=%v, want positive", trigger.PriceChange2Min)
			}
			return
		}
	}
	t.Fatalf("got %+v, want price_movement trigger", triggers)
}

func TestOrderBookMonitor_RejectsInvalidThresholds(t *testing.T) {
	monitor := NewOrderBookMonitor("BTCUSDT")
	monitor.SetThresholds(-1, 0, -1)
	metrics := monitor.GetCurrentMetrics()
	if metrics["imbalance_threshold"] != 0.35 {
		t.Fatalf("invalid imbalance threshold was accepted: %v", metrics["imbalance_threshold"])
	}
}
