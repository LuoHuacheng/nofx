package automation

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestAlertOutboxFailure proves a failing webhook only postpones the alert: the
// fault is persisted first, protection keeps running, and a later attempt
// delivers it. The message never carries a credential.
func TestAlertOutboxFailure(t *testing.T) {
	rig := rig(t, nil)
	rt := rig.runtime(t, nil)
	ctx := context.Background()

	var hits int
	failing := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(failing.Close)
	rt.alertClient = failing.Client()

	// The fixture is a TLS server, so the same code path runs as in production;
	// only the target row is injected here.
	rt.alertURL = failing.URL

	if err := rt.QueueAlert(ctx, Alert{AccountKey: "account", Code: "protection_missing", Message: "api_key=abc123 secret_key=deadbeef signature=sig"}); err != nil {
		t.Fatal(err)
	}
	var queued Alert
	if err := rig.db.First(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(queued.Message, "abc123") || strings.Contains(queued.Message, "deadbeef") || strings.Contains(queued.Message, "sig]") {
		t.Fatalf("a credential reached the outbox: %q", queued.Message)
	}
	if queued.DeliveredAt != nil {
		t.Fatal("an alert was marked delivered before any attempt")
	}

	if err := rt.DeliverAlerts(ctx); err == nil {
		t.Fatal("a 500 webhook was reported as delivered")
	}
	// Protection is untouched by the failed delivery.
	if err := rt.Tick(ctx, "alice", "account"); err != nil {
		t.Fatal(err)
	}
	var after Alert
	if err := rig.db.First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Attempts != 1 || after.NextAttempt.Before(queued.NextAttempt) {
		t.Fatalf("retry not scheduled forward: attempts=%d next=%s", after.Attempts, after.NextAttempt)
	}

	rig.advance(after.NextAttempt.Sub(*rig.clock) + time.Second)
	if err := rt.DeliverAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	var delivered Alert
	if err := rig.db.First(&delivered).Error; err != nil {
		t.Fatal(err)
	}
	if delivered.DeliveredAt == nil {
		t.Fatal("a delivered alert stayed pending")
	}
	// A delivered alert is not resent on the next pass.
	if err := rt.DeliverAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("the outbox posted %d times, wanted 2", hits)
	}
}

// TestAlertTargetValidation rejects every address class that could be used to
// reach the host or the local network, including a DNS answer that changes.
func TestAlertTargetValidation(t *testing.T) {
	reject := []struct {
		name    string
		url     string
		resolve func(string) ([]net.IP, error)
	}{
		{name: "plain http", url: "http://alerts.example.test/hook"},
		{name: "no scheme", url: "alerts.example.test/hook"},
		{name: "loopback name", url: "https://localhost/hook"},
		{name: "private literal", url: "https://192.168.1.10/hook"},
		{name: "link local", url: "https://169.254.1.1/hook"},
		{name: "unspecified", url: "https://0.0.0.0/hook"},
		{name: "dns rebinding", url: "https://rebind.example.test/hook", resolve: func(string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("127.0.0.1")}, nil
		}},
		{name: "unresolvable", url: "https://gone.example.test/hook", resolve: func(string) ([]net.IP, error) {
			return nil, errors.New("no such host")
		}},
	}
	for _, tc := range reject {
		if _, err := validateAlertTarget(tc.url, tc.resolve); err == nil {
			t.Fatalf("%s was accepted as an alert target", tc.name)
		}
	}
	if got, err := validateAlertTarget("https://alerts.example.test/hook", func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}); err != nil || got != "https://alerts.example.test/hook" {
		t.Fatalf("a public https target was refused: %q %v", got, err)
	}
}

// TestAlertRedirectRefused proves the delivery client never follows a redirect:
// the target was validated, so another hop could carry the body elsewhere.
func TestAlertRedirectRefused(t *testing.T) {
	client := newAlertClient(nil)
	if client.CheckRedirect == nil {
		t.Fatal("the alert client follows redirects")
	}
	err := client.CheckRedirect(&http.Request{URL: &url.URL{Scheme: "http", Host: "elsewhere.example.test"}}, nil)
	if !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("the alert client followed a redirect: %v", err)
	}
}

// TestAlertDialGuard proves the connection-level check refuses a host that
// resolves to a private address even though the URL itself looked public.
func TestAlertDialGuard(t *testing.T) {
	if err := guardDial(nil)("tcp", "127.0.0.1:443", nil); err == nil {
		t.Fatal("the dial guard connected to loopback")
	}
	if err := guardDial(nil)("tcp", "10.0.0.7:443", nil); err == nil {
		t.Fatal("the dial guard connected to a private address")
	}
	if err := guardDial(nil)("tcp", "203.0.113.10:443", nil); err != nil {
		t.Fatalf("the dial guard refused a public address: %v", err)
	}
}

// TestAlertTargetIsSingle proves the webhook target is replaced, never added:
// an operator cannot end up with two delivery endpoints.
func TestAlertTargetIsSingle(t *testing.T) {
	rig := rig(t, nil)
	rt := rig.runtime(t, nil)
	for _, url := range []string{"https://a.example.test/hook", "https://b.example.test/hook"} {
		if err := rt.SetAlertTarget(url); err != nil {
			t.Fatal(err)
		}
	}
	var rows []alertTarget
	if err := rig.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].URL != "https://b.example.test/hook" {
		t.Fatalf("alert targets are not single: %+v", rows)
	}
}
