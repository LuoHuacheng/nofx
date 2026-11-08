package binance

import (
	"context"
	"net/http"
	"nofx/trader/execution"
	"strings"
	"testing"
)

func TestManagedAccountIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"one_asset", `[{"accountAlias":"account-one","asset":"USDT"}]`, 200, "account-one"},
		{"multiple_assets", `[{"accountAlias":"account-one","asset":"USDT"},{"accountAlias":"account-one","asset":"USDC"}]`, 200, "account-one"},
		{"no_balances", `[]`, 200, ""},
		{"missing_alias", `[{"asset":"USDT"}]`, 200, ""},
		{"blank_alias", `[{"accountAlias":"  ","asset":"USDT"}]`, 200, ""},
		{"partial_identity", `[{"accountAlias":"account-one"},{"asset":"USDC"}]`, 200, ""},
		{"conflicting_aliases", `[{"accountAlias":"account-one"},{"accountAlias":"account-two"}]`, 200, ""},
		{"null_balance", `[null]`, 200, ""},
		{"upstream_error", `{"code":-2015,"msg":"do not expose signed URL or test-key"}`, 401, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, writes := newManagedBinanceFixture(t, func(r *http.Request) (int, string) {
				if r.URL.Path != "/fapi/v3/balance" || r.Method != http.MethodGet || r.URL.Query().Get("signature") == "" {
					t.Error("identity must use a signed, read-only balance query")
				}
				return tc.status, tc.body
			})
			reader, ok := any(b).(execution.AccountIdentity)
			if !ok {
				t.Fatal("Binance adapter cannot verify physical account identity")
			}
			got, err := reader.VenueAccountID(context.Background())
			if tc.want != "" {
				if err != nil || got != tc.want {
					t.Fatalf("identity = %q, %v", got, err)
				}
			} else if err == nil || got != "" {
				t.Fatalf("invalid identity accepted: %q, %v", got, err)
			}
			if err != nil && (strings.Contains(err.Error(), "test-key") || strings.Contains(err.Error(), "signature=")) {
				t.Fatal("identity error exposed sensitive request data")
			}
			if *writes != 0 {
				t.Fatal("identity check changed the account")
			}
		})
	}
}
