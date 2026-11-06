package binance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"nofx/trader/execution"
)

// binanceOrderUpdateFixture is a recorded ORDER_TRADE_UPDATE payload. Field
// names and the execution-report envelope come from the USDⓈ-M user-data docs
// (2026-11-06); the socket path itself is not exercised against a venue.
const binanceOrderUpdateFixture = `{"e":"ORDER_TRADE_UPDATE","E":1700000000000,"T":1700000000001,"o":{"s":"BTCUSDT","c":"nofx-entry-1","S":"BUY","o":"LIMIT","f":"GTC","q":"0.01","p":"100000","ap":"100000","X":"FILLED","i":42,"l":"0.01","z":"0.01","L":"100000","T":1700000000001,"t":7,"R":false}}`

// streamFixture serves the listen-key REST call and one user-data socket. The
// socket sends the recorded payload and then closes, which is exactly the
// reconnect path the runtime must treat as a gap.
type streamFixture struct {
	requests int
	sends    []string
	closeNow bool
}

func newStreamFixture(t *testing.T, f *streamFixture) (*FuturesTrader, *int) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v1/time" {
			fmt.Fprint(w, `{"serverTime":1700000000000}`)
			return
		}
		if r.URL.Path != "/fapi/v1/listenKey" {
			t.Fatalf("unexpected Binance endpoint %s %s", r.Method, r.URL.String())
		}
		f.requests++
		fmt.Fprint(w, `{"listenKey":"stream-key-1"}`)
	}))
	t.Cleanup(api.Close)
	ws := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		for _, payload := range f.sends {
			if err := websocket.Message.Send(conn, payload); err != nil {
				return
			}
		}
		if f.closeNow {
			// Give the frame time to reach the client before dropping the socket.
			<-time.After(50 * time.Millisecond)
			_ = conn.Close()
			return
		}
		// Keep the socket open until the caller cancels, so a test can observe
		// that nothing else is emitted.
		<-time.After(30 * time.Second)
	}))
	t.Cleanup(ws.Close)

	b, err := newManagedAccountTrader(context.Background(), "test-key", "test-secret", "", execution.EnvironmentSandbox, &managedEndpoint{
		baseURL:   api.URL,
		client:    api.Client(),
		streamURL: strings.Replace(ws.URL, "http://", "ws://", 1) + "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b, &f.requests
}

// TestManagedEventsStreamReconnect proves the stream emits a reconcile marker
// before any fill, so a caller can never trust its own view across a gap, and
// that a dropped socket is reported rather than silently treated as quiet.
func TestManagedEventsStreamReconnect(t *testing.T) {
	f := &streamFixture{sends: []string{binanceOrderUpdateFixture}, closeNow: true}
	b, _ := newStreamFixture(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got []execution.AccountEvent
	err := b.ManagedEvents(ctx, func(e execution.AccountEvent) error {
		mu.Lock()
		got = append(got, e)
		n := len(got)
		mu.Unlock()
		t.Logf("event %d: kind=%s id=%s", n, e.Kind, e.ID)
		if n >= 3 {
			return errStreamTestDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStreamTestDone) {
		t.Fatalf("the stream failed instead of reconnecting: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("the stream produced %d events", len(got))
	}
	// The first message of every connection is the gap marker.
	if got[0].Kind != execution.KindReconcile {
		t.Fatalf("the first stream event was %q, wanted a reconcile marker", got[0].Kind)
	}
	var fill *execution.AccountEvent
	for i := range got {
		if got[i].Kind == execution.KindFill {
			fill = &got[i]
			break
		}
	}
	if fill == nil {
		t.Fatal("a dropped socket produced no fill event")
	}
	if fill.ID != "binance:trade:7" {
		t.Fatalf("the fill used identity %q, wanted the venue trade id", fill.ID)
	}
	if fill.Order.Filled != "0.01" || fill.Order.Status != "filled" {
		t.Fatalf("the fill was misreported: %+v", fill.Order)
	}
	// A reconnect must appear as a second gap marker, never as silence.
	gaps := 0
	for _, e := range got {
		if e.Kind == execution.KindReconcile {
			gaps++
		}
	}
	if gaps < 2 {
		t.Fatalf("a dropped socket emitted %d gap markers, wanted a second one", gaps)
	}
	// Credentials must never reach the event stream.
	for _, e := range got {
		if strings.Contains(e.ID, "test-key") || strings.Contains(e.ID, "test-secret") {
			t.Fatal("the stream leaked credentials")
		}
	}
}

var errStreamTestDone = errors.New("test collected enough events")

// TestManagedStreamIgnoresUnrelatedMessages proves only execution reports become
// events: a balance update is not a fill and must not wake protection as one.
func TestManagedStreamIgnoresUnrelatedMessages(t *testing.T) {
	cases := map[string]bool{
		binanceOrderUpdateFixture:                                    true,
		`{"e":"ACCOUNT_UPDATE","E":1700000000000,"a":{"m":"ORDER"}}`: false,
		`{"e":"listenKeyExpired","E":1700000000000}`:                 false,
		`not json`: false,
	}
	for payload, want := range cases {
		_, ok := binanceStreamEvent([]byte(payload))
		if ok != want {
			t.Fatalf("%q became an event=%v, wanted %v", payload, ok, want)
		}
	}
}
