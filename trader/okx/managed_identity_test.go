package okx

import (
	"context"
	"net/http"
	"testing"

	"nofx/trader/execution"
)

func TestManagedVenueAccountID(t *testing.T) {
	ctx := context.Background()
	client, _ := newManagedOKXFixture(t, execution.EnvironmentSandbox, okxManagedRoutes{
		okxAccountConfigPath: func(*http.Request) (int, string) {
			return 200, okxManagedData(`[{"posMode":"net_mode","acctLv":"2","uid":"4123456789"}]`)
		},
	})
	id, err := client.VenueAccountID(ctx)
	if err != nil || id != "4123456789" {
		t.Fatalf("uid = %q, %v", id, err)
	}
}

func TestManagedVenueAccountIDRejectsAmbiguousRead(t *testing.T) {
	ctx := context.Background()
	for name, data := range map[string]string{
		"missing uid":  `[{"posMode":"net_mode","acctLv":"2"}]`,
		"blank uid":    `[{"uid":"  "}]`,
		"multiple uid": `[{"uid":"1"},{"uid":"2"}]`,
		"no rows":      `[]`,
	} {
		client, _ := newManagedOKXFixture(t, execution.EnvironmentLive, okxManagedRoutes{
			okxAccountConfigPath: func(*http.Request) (int, string) { return 200, okxManagedData(data) },
		})
		if _, err := client.VenueAccountID(ctx); err == nil {
			t.Fatalf("%s: ambiguous identity accepted", name)
		}
	}
}
