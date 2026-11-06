package instrument

import (
	"strings"
	"testing"
)

func TestNativeSymbolsPreserveHanAndRejectSeparators(t *testing.T) {
	for _, s := range []string{"BTCUSDT", "BTC-USDT-SWAP", "1000PEPEUSDT", "\u725b\u6765USDT", "\u5e01\u5b89\u4eba\u751fUSDT", "\u725b\u6765", "xyz:GOOGL", "xyz:SKHY"} {
		if !ValidSymbol(s) {
			t.Fatal("valid identifier rejected", s)
		}
	}
	for _, s := range []string{"", "btcUSDT", "XYZ:GOOGL", "xyz:googl", "xyz:GOOGL:EXTRA", "xyz/evil:GOOGL", "\u725b\u6765 USDT", "\u725b\u6765/USDT", "\u725b\u6765USDT&limit=999", "\u725b\u6765USDT%0A", "\u725b\u6765\nUSDT", "\u725b\u6765\u202eUSDT", "\u725b\u6765🚀USDT", "ΒTCUSDT", strings.Repeat("\u725b", 41), string([]byte{0xff})} {
		if ValidSymbol(s) {
			t.Fatal("unsafe identifier accepted", s)
		}
	}
}
