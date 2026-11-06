package okx

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"nofx/trader/execution"
)

// okxOrderPushFixture is a recorded `orders` channel push. Field names come from
// the OKX v5 private-channel docs (2026-11-06); the socket itself is not
// exercised against a venue.
const okxOrderPushFixture = `{"arg":{"channel":"orders","instType":"SWAP"},"data":[{"instId":"BTC-USDT-SWAP","ordId":"555","clOrdId":"nofx-entry-1","side":"buy","state":"filled","accFillSz":"0.01","avgPx":"100000","fillSz":"0.01","tradeId":"99","uTime":"1700000000002","reduceOnly":"false"}]}`

// okxStreamFixture serves one private socket: it answers the login frame, then
// sends the recorded push, then optionally closes to exercise the reconnect path.
type okxStreamFixture struct {
	loginErr bool
	sends    []string
	closeNow bool
	logins   int
}

func newOKXStreamFixture(t *testing.T, f *okxStreamFixture) *OKXTrader {
	t.Helper()
	ws := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		var raw []byte
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			return
		}
		f.logins++
		if f.loginErr {
			_ = websocket.Message.Send(conn, `{"event":"error","code":"60009","msg":"Login failed"}`)
			return
		}
		_ = websocket.Message.Send(conn, `{"event":"login","code":"0","msg":""}`)
		// The client subscribes next; a dropped subscription must fail the stream.
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			return
		}
		if !strings.Contains(string(raw), okxOrdersChannel) {
			_ = websocket.Message.Send(conn, `{"event":"error","code":"60012","msg":"unexpected op"}`)
			return
		}
		_ = websocket.Message.Send(conn, `{"event":"subscribe","arg":{"channel":"orders"}}`)
		for _, payload := range f.sends {
			if err := websocket.Message.Send(conn, payload); err != nil {
				return
			}
		}
		if f.closeNow {
			<-time.After(50 * time.Millisecond)
			_ = conn.Close()
			return
		}
		<-time.After(30 * time.Second)
	}))
	t.Cleanup(ws.Close)

	trader := NewManagedAccountTraderForTest(t, "key", "secret", "pass")
	trader.managedStreamURL = strings.Replace(ws.URL, "http://", "ws://", 1)
	return trader
}

// NewManagedAccountTraderForTest builds the account-only managed client without
// the production endpoint, so a test socket can stand in for the venue.
func NewManagedAccountTraderForTest(t *testing.T, key, secret, passphrase string) *OKXTrader {
	t.Helper()
	tr, err := NewManagedAccountTrader(context.Background(), key, secret, passphrase, execution.EnvironmentSandbox)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

var errOKXStreamDone = errors.New("test collected enough events")

// TestManagedEventsStreamReconnect proves every connection starts with a gap
// marker, that a fill is read from the order channel, and that a dropped socket
// is reconnected instead of reported as a quiet account.
func TestManagedEventsStreamReconnect(t *testing.T) {
	f := &okxStreamFixture{sends: []string{okxOrderPushFixture}, closeNow: true}
	tr := newOKXStreamFixture(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []execution.AccountEvent
	err := tr.ManagedEvents(ctx, func(e execution.AccountEvent) error {
		mu.Lock()
		got = append(got, e)
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			return errOKXStreamDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errOKXStreamDone) {
		t.Fatalf("the stream failed instead of reconnecting: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 || got[0].Kind != execution.KindReconcile {
		t.Fatalf("the stream did not open with a gap marker: %+v", got)
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
	if fill.ID != "okx:trade:99" || fill.Order.Filled != "0.01" || fill.Order.Status != "filled" {
		t.Fatalf("the fill was misreported: %+v", fill)
	}
	gaps := 0
	for _, e := range got {
		if e.Kind == execution.KindReconcile {
			gaps++
		}
	}
	if gaps < 2 {
		t.Fatalf("a dropped socket emitted %d gap markers", gaps)
	}
	for _, e := range got {
		if strings.Contains(e.ID, "secret") || strings.Contains(e.Order.ClientID, "pass") {
			t.Fatal("the stream leaked credentials")
		}
	}
}

// TestManagedStreamRejectsBadLogin proves an unauthenticated socket fails loudly:
// continuing would report a quiet account that is really an unread one.
func TestManagedStreamRejectsBadLogin(t *testing.T) {
	f := &okxStreamFixture{loginErr: true}
	tr := newOKXStreamFixture(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events := 0
	err := tr.ManagedEvents(ctx, func(execution.AccountEvent) error {
		events++
		return nil
	})
	if errors.Is(err, errOKXStreamDone) {
		t.Fatal("a rejected login was reported as a healthy stream")
	}
	// Only the gap marker: no fill may be invented from an unauthenticated socket.
	if events != 1 {
		t.Fatalf("a rejected login produced %d events", events)
	}
}

// TestManagedStreamIgnoresUnrelatedMessages proves only order pushes become
// events: an ack or a pong is not a fill.
func TestManagedStreamIgnoresUnrelatedMessages(t *testing.T) {
	cases := map[string]bool{
		okxOrderPushFixture: true,
		`{"event":"subscribe","arg":{"channel":"orders"}}`:       false,
		`{"event":"error","code":"60012","msg":"unexpected op"}`: false,
		`pong`: false,
		`{"arg":{"channel":"account"},"data":[{"ccy":"USDT"}]}`: false,
	}
	for payload, want := range cases {
		_, ok := okxStreamEvent([]byte(payload))
		if ok != want {
			t.Fatalf("%q became an event=%v, wanted %v", payload, ok, want)
		}
	}
}

func TestOKXStreamEndpointIsPinned(t *testing.T) {
	live, err := okxStreamEndpoint(execution.EnvironmentLive)
	if err != nil || live != "wss://ws.okx.com:8443/ws/v5/private" {
		t.Fatalf("live endpoint = %q, %v", live, err)
	}
	sandbox, err := okxStreamEndpoint(execution.EnvironmentSandbox)
	if err != nil || !strings.Contains(sandbox, "brokerId=9999") {
		t.Fatalf("sandbox endpoint = %q, %v", sandbox, err)
	}
	if _, err := okxStreamEndpoint(""); err == nil {
		t.Fatal("an empty environment selected an endpoint")
	}
}
