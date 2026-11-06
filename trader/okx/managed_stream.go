package okx

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

// OKX private socket cadence. The venue closes an idle connection after 30s
// without traffic, so the client pings well inside that window; the login is
// re-sent on every reconnect because a socket carries no session.
const (
	okxStreamPing       = 20 * time.Second
	okxStreamBackoff    = 5 * time.Second
	okxStreamMaxBackoff = time.Minute
	okxLoginPath        = "/users/self/verify"
	okxOrdersChannel    = "orders"
)

// okxStreamEndpoint pins the private socket per environment. Hosts are the
// documented OKX v5 endpoints (2026-11-06): ws.okx.com for live, wspap.okx.com
// with the demo broker id for simulated trading.
func okxStreamEndpoint(env execution.Environment) (string, error) {
	switch env {
	case execution.EnvironmentLive:
		return "wss://ws.okx.com:8443/ws/v5/private", nil
	case execution.EnvironmentSandbox:
		return "wss://wspap.okx.com:8443/ws/v5/private?brokerId=9999", nil
	}
	return "", errors.New("managed OKX environment must be sandbox or live")
}

// ManagedEvents streams the account's private order channel. The stream is an
// optimisation, never the authority: every connection starts with a reconcile
// marker, and a dropped socket is retried with backoff instead of reporting the
// account as quiet.
func (t *OKXTrader) ManagedEvents(ctx context.Context, fn func(execution.AccountEvent) error) error {
	url := t.managedStreamURL
	if url == "" {
		return errors.New("managed OKX events require a managed account")
	}
	backoff := okxStreamBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := fn(execution.AccountEvent{
			ID:         "okx:stream:" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Kind:       execution.KindReconcile,
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		err := t.streamOnce(ctx, url, fn)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("the OKX private stream ended")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < okxStreamMaxBackoff {
			backoff *= 2
			if backoff > okxStreamMaxBackoff {
				backoff = okxStreamMaxBackoff
			}
		}
	}
}

func (t *OKXTrader) streamOnce(ctx context.Context, url string, fn func(execution.AccountEvent) error) error {
	conn, err := websocket.Dial(url, "", "https://www.okx.com")
	if err != nil {
		return errors.New("could not connect the OKX private stream")
	}
	closed := atomic.Bool{}
	defer func() {
		if !closed.Load() {
			_ = conn.Close()
		}
	}()
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

	if err := t.streamLogin(conn); err != nil {
		return err
	}
	// The order channel is the only subscription: a fill is the one message that
	// proves exposure changed, and everything else belongs to the REST record.
	if err := websocket.Message.Send(conn, `{"op":"subscribe","args":[{"channel":"`+okxOrdersChannel+`","instType":"SWAP"}]}`); err != nil {
		return errors.New("could not subscribe the OKX private stream")
	}
	ping := make(chan struct{})
	defer close(ping)
	go t.streamPing(ctx, conn, ping)

	for {
		var raw []byte
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("the OKX private stream dropped")
		}
		event, ok := okxStreamEvent(raw)
		if !ok {
			continue
		}
		if err := fn(event); err != nil {
			return err
		}
	}
}

// streamLogin signs the login frame. The signature covers the timestamp, the
// verb and the verify path exactly as the REST signer does, and the passphrase
// never leaves the frame it belongs to.
func (t *OKXTrader) streamLogin(conn *websocket.Conn) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	frame := struct {
		Op   string `json:"op"`
		Args []struct {
			APIKey     string `json:"apiKey"`
			Passphrase string `json:"passphrase"`
			Timestamp  string `json:"timestamp"`
			Sign       string `json:"sign"`
		} `json:"args"`
	}{Op: "login", Args: make([]struct {
		APIKey     string `json:"apiKey"`
		Passphrase string `json:"passphrase"`
		Timestamp  string `json:"timestamp"`
		Sign       string `json:"sign"`
	}, 1)}
	frame.Args[0].APIKey = t.apiKey
	frame.Args[0].Passphrase = t.passphrase
	frame.Args[0].Timestamp = ts
	frame.Args[0].Sign = t.sign(ts, "GET", okxLoginPath, "")
	payload, err := json.Marshal(frame)
	if err != nil {
		return errors.New("could not build the OKX login frame")
	}
	if err := websocket.Message.Send(conn, string(payload)); err != nil {
		return errors.New("could not send the OKX login frame")
	}
	// A rejected login must fail the connection: continuing unauthenticated would
	// report a quiet account that is really an unread one.
	var raw []byte
	if err := websocket.Message.Receive(conn, &raw); err != nil {
		return errors.New("the OKX private stream closed before login")
	}
	var reply struct {
		Event string `json:"event"`
		Code  string `json:"code"`
		Msg   string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return errors.New("the OKX login reply is not readable")
	}
	if reply.Event != "login" || reply.Code != "0" {
		return errors.New("the OKX private stream rejected the login")
	}
	return nil
}

func (t *OKXTrader) streamPing(ctx context.Context, conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(okxStreamPing)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := websocket.Message.Send(conn, "ping"); err != nil {
				return
			}
		}
	}
}

// okxStreamEvent converts one private channel payload. Order pushes are the only
// messages that become events; a subscription ack or a pong is not a fill.
func okxStreamEvent(raw []byte) (execution.AccountEvent, bool) {
	var payload struct {
		Arg struct {
			Channel string `json:"channel"`
		} `json:"arg"`
		Data []struct {
			InstID     string `json:"instId"`
			OrdID      string `json:"ordId"`
			ClOrdID    string `json:"clOrdId"`
			Side       string `json:"side"`
			State      string `json:"state"`
			AccFillSz  string `json:"accFillSz"`
			AvgPx      string `json:"avgPx"`
			FillSz     string `json:"fillSz"`
			TradeID    string `json:"tradeId"`
			UTime      string `json:"uTime"`
			ReduceOnly string `json:"reduceOnly"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return execution.AccountEvent{}, false
	}
	if payload.Arg.Channel != okxOrdersChannel || len(payload.Data) == 0 {
		return execution.AccountEvent{}, false
	}
	out := payload.Data[0]
	if out.OrdID == "" {
		return execution.AccountEvent{}, false
	}
	id := "okx:order:" + out.OrdID
	if out.TradeID != "" {
		id = "okx:trade:" + out.TradeID
	}
	at := time.Now().UTC()
	if out.UTime != "" {
		if ms, err := strconv.ParseInt(out.UTime, 10, 64); err == nil {
			at = time.UnixMilli(ms).UTC()
		}
	}
	side := "long"
	if out.Side == "sell" {
		side = "short"
	}
	return execution.AccountEvent{
		ID:         id,
		Kind:       execution.KindFill,
		Symbol:     out.InstID,
		OccurredAt: at,
		Order: execution.Order{
			ClientID:   out.ClOrdID,
			OrderID:    out.OrdID,
			Symbol:     out.InstID,
			Side:       side,
			Quantity:   execution.Decimal(out.AccFillSz),
			Filled:     execution.Decimal(out.AccFillSz),
			Average:    execution.Decimal(out.AvgPx),
			Status:     okxStreamStatus(out.State),
			ReduceOnly: out.ReduceOnly == "true",
		},
	}, true
}

// okxStreamStatus maps the venue's order state onto the adapter's vocabulary.
// An unknown state keeps the venue's spelling rather than inventing a terminal
// one that would let a run believe it is protected.
func okxStreamStatus(state string) string {
	switch state {
	case "filled":
		return "filled"
	case "partially_filled":
		return "partially_filled"
	case "canceled", "mmp_canceled":
		return "cancelled"
	case "live":
		return "new"
	}
	return state
}
