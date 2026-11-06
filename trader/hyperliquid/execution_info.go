package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

func (t *HyperliquidTrader) directAccountInfo(ctx context.Context, v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, t.apiBaseURL()+"/info", bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, e := client.Do(r)
	if e != nil {
		return nil, errors.New("Hyperliquid account request unavailable")
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 || resp.StatusCode != 200 {
		return nil, errors.New("invalid Hyperliquid account response")
	}
	return body, nil
}
