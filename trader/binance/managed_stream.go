package binance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
	"nofx/trader/execution"
)

// binanceStreamPing is the venue's user-stream keepalive cadence. The listen key
// itself expires after 60 minutes unless it is extended, so a healthy socket
// must keep both the ping and the extension running.
const (
	binanceStreamPing       = 3 * time.Minute
	binanceListenKeyExtend  = 30 * time.Minute
	binanceStreamBackoff    = 5 * time.Second
	binanceStreamMaxBackoff = time.Minute
)

// binanceStreamEndpoint pins the user-data socket per environment. Both hosts
// are the pinned SDK constants (v2.8.9, checked 2026-11-06); the listen key
// endpoint stays the REST endpoint the trader already uses.
func binanceStreamEndpoint(env execution.Environment) (string, error) {
	switch env {
	case execution.EnvironmentSandbox:
		return "wss://stream.binancefuture.com/ws/", nil
	case execution.EnvironmentLive:
		return "wss://fstream.binance.com/ws/", nil
	}
	return "", errors.New("managed Binance environment must be sandbox or live")
}

// ManagedEvents streams the account's private user data. The stream is an
// optimisation, never the authority: a reconnect emits a reconcile marker so the
// caller re-reads the venue record, and a broken stream returns an error rather
// than pretending the account is quiet.
func (b *FuturesTrader) ManagedEvents(ctx context.Context, fn func(execution.AccountEvent) error) error {
	if !b.managedEnvironment.Valid() {
		return errors.New("managed Binance events require a managed account")
	}
	url := b.managedStreamURL
	if url == "" {
		return errors.New("managed Binance events require a managed account")
	}
	backoff := binanceStreamBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		// Every reconnect starts with a reconcile marker: the caller must not
		// trust its own view of the account until the REST record is re-read.
		if err := fn(execution.AccountEvent{
			ID:         "binance:stream:" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Kind:       execution.KindReconcile,
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		wait, err := b.streamOnce(ctx, url, fn)
		if ctx.Err() != nil {
			return nil
		}
		if wait {
			// The venue closed the socket normally; reconnect at once but keep a
			// floor so a flapping venue cannot spin this loop.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(binanceStreamBackoff):
			}
			continue
		}
		if err == nil {
			err = errors.New("the user stream ended")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < binanceStreamMaxBackoff {
			backoff *= 2
			if backoff > binanceStreamMaxBackoff {
				backoff = binanceStreamMaxBackoff
			}
		}
	}
}

// streamOnce opens one socket and pumps it until it fails. It reports whether
// the venue closed the socket normally, so the caller can tell a healthy
// reconnect from a broken stream.
func (b *FuturesTrader) streamOnce(ctx context.Context, baseURL string, fn func(execution.AccountEvent) error) (bool, error) {
	key, err := b.client.NewStartUserStreamService().Do(ctx)
	if err != nil || key == "" {
		return false, errors.New("could not open the Binance user stream")
	}
	conn, err := websocket.Dial(baseURL+key, "", "https://fstream.binance.com")
	if err != nil {
		return false, errors.New("could not connect the Binance user stream")
	}
	closed := atomic.Bool{}
	defer func() {
		if !closed.Load() {
			_ = conn.Close()
		}
	}()

	// The socket is bounded by the caller's context: cancelling it must stop the
	// stream, not leave a reader blocked on a dead connection.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			closed.Store(true)
			_ = conn.Close()
		case <-done:
		}
	}()
	// The listen key expires after 60 minutes; extend it while the socket lives.
	extend := make(chan struct{})
	defer close(extend)
	go b.keepListenKey(ctx, key, extend)

	for {
		var raw []byte
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return false, errors.New("the Binance user stream dropped")
		}
		if len(raw) == 0 {
			continue
		}
		event, ok := binanceStreamEvent(raw)
		if !ok {
			continue
		}
		if err := fn(event); err != nil {
			return false, err
		}
	}
}

// keepListenKey extends the listen key until the socket closes. A failed
// extension is not fatal on its own: the socket drops within the hour and the
// reconnect path emits the reconcile marker.
func (b *FuturesTrader) keepListenKey(ctx context.Context, key string, done <-chan struct{}) {
	ticker := time.NewTicker(binanceListenKeyExtend)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			_ = b.client.NewKeepaliveUserStreamService().ListenKey(key).Do(ctx)
		}
	}
}

// binanceStreamEvent converts one user-data payload. Only execution reports
// become events: they are the only stream message that proves exposure changed.
// Everything else — balance updates, config changes, margin calls — is left to
// the REST record, which the ledger already reads.
func binanceStreamEvent(raw []byte) (execution.AccountEvent, bool) {
	// The envelope carries both "e" (event type) and "E" (event time). Go's JSON
	// decoder falls back to case-insensitive matching, so without an explicit
	// EventTime field the numeric "E" would be decoded into the string type.
	var payload struct {
		Event     string `json:"e"`
		EventTime int64  `json:"E"`
		Order     struct {
			Symbol               string `json:"s"`
			ClientOrderID        string `json:"c"`
			Side                 string `json:"S"`
			Status               string `json:"X"`
			OrderID              int64  `json:"i"`
			LastFilledQty        string `json:"l"`
			AccumulatedFilledQty string `json:"z"`
			LastFilledPrice      string `json:"L"`
			TradeID              int64  `json:"t"`
			TradeTime            int64  `json:"T"`
			IsReduceOnly         bool   `json:"R"`
		} `json:"o"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return execution.AccountEvent{}, false
	}
	if payload.Event != "ORDER_TRADE_UPDATE" {
		return execution.AccountEvent{}, false
	}
	at := time.Now().UTC()
	if payload.Order.TradeTime > 0 {
		at = time.UnixMilli(payload.Order.TradeTime).UTC()
	}
	id := "binance:order:" + strconv.FormatInt(payload.Order.OrderID, 10)
	if payload.Order.TradeID > 0 {
		// A trade is the finer identity: two fills on one order are two events,
		// and a replayed trade id is exactly the duplicate the ledger must drop.
		id = "binance:trade:" + strconv.FormatInt(payload.Order.TradeID, 10)
	}
	side := "long"
	if payload.Order.Side == "SELL" {
		// One-way SELL reduces a long position.
		side = "short"
	}
	return execution.AccountEvent{
		ID:         id,
		Kind:       execution.KindFill,
		Symbol:     payload.Order.Symbol,
		OccurredAt: at,
		Order: execution.Order{
			ClientID:   payload.Order.ClientOrderID,
			OrderID:    strconv.FormatInt(payload.Order.OrderID, 10),
			Symbol:     payload.Order.Symbol,
			Side:       side,
			Quantity:   execution.Decimal(payload.Order.AccumulatedFilledQty),
			Filled:     execution.Decimal(payload.Order.AccumulatedFilledQty),
			Average:    execution.Decimal(payload.Order.LastFilledPrice),
			Status:     binanceStreamStatus(payload.Order.Status),
			ReduceOnly: payload.Order.IsReduceOnly,
		},
	}, true
}

// binanceStreamStatus maps the venue's order status onto the adapter's
// vocabulary. An unknown status keeps the venue's own spelling rather than
// inventing a terminal one.
func binanceStreamStatus(status string) string {
	switch status {
	case "FILLED":
		return "filled"
	case "PARTIALLY_FILLED":
		return "partially_filled"
	case "CANCELED", "EXPIRED":
		return "cancelled"
	case "NEW":
		return "new"
	}
	return status
}
