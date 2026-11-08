package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicMarketGETRetriesTransientFailure(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					return
				}
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer srv.Close()
			c := New()
			var out struct {
				OK bool `json:"ok"`
			}
			if err := c.request(context.Background(), "GET", srv.URL, nil, &out); err != nil {
				t.Fatal(err)
			}
			if !out.OK || calls.Load() != 2 {
				t.Fatalf("result=%v requests=%d", out.OK, calls.Load())
			}
		})
	}
}
func TestPublicMarketGETRetriesRequestTimeout(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	c := New()
	c.HTTP.Timeout = 60 * time.Millisecond
	var out map[string]any
	if err := c.request(context.Background(), "GET", srv.URL, nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("requests=%d", calls.Load())
	}
}
func TestPublicMarketRetryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		status, want int
		body         string
	}{
		{"persistent outage", "GET", 503, 2, `{}`}, {"client error", "GET", 400, 1, `{}`}, {"post never replayed", "POST", 503, 1, `{}`}, {"bad JSON", "GET", 200, 1, `broken`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			c := New()
			var out any
			if err := c.request(context.Background(), tc.method, srv.URL, nil, &out); err == nil {
				t.Fatal("failure accepted")
			}
			if int(calls.Load()) != tc.want {
				t.Fatalf("requests=%d want%d", calls.Load(), tc.want)
			}
		})
	}
}
func TestPublicMarketRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); cancel(); w.WriteHeader(503) }))
	defer srv.Close()
	c := New()
	var out any
	if err := c.request(ctx, "GET", srv.URL, nil, &out); err == nil {
		t.Fatal("cancelled request accepted")
	}
	if calls.Load() != 1 {
		t.Fatalf("cancelled request replayed %d times", calls.Load())
	}
}
