package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func definition() Definition {
	return Definition{Manifest: Manifest{ID: "funding", Name: "funding", Version: "1", Capability: "public.read", Enabled: true}, Read: func(ctx context.Context, q Request) (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{Venue: q.Venue, Symbol: q.Symbol, Source: "fixture", Observed: time.Now(), Mode: "demo", Metrics: []Metric{{Key: "funding_rate", Label: "funding", Value: "-0.0001", Unit: "fraction"}}}, nil
	}}
}
func TestRegistryAuthorityPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	d := definition()
	calls := 0
	read := d.Read
	d.Read = func(ctx context.Context, q Request) (Result, error) { calls++; return read(ctx, q) }
	r, err := Open(path, d)
	if err != nil {
		t.Fatal(err)
	}
	q := Request{Venue: "binance", Symbol: "BTCUSDT"}
	if _, err = r.Read(context.Background(), "funding", q); err != nil {
		t.Fatal(err)
	}
	if err = r.SetEnabled("funding", false); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Read(context.Background(), "funding", q); err == nil || calls != 1 {
		t.Fatal("disabled plugin executed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("settings not private")
	}
	reopened, err := Open(path, d)
	if err != nil || reopened.List()[0].Enabled {
		t.Fatal("disabled state not retained")
	}
	if err = r.SetEnabled("approve", true); err == nil {
		t.Fatal("unregistered capability accepted")
	}
	d.Manifest.Capability = "trade.execute"
	if _, err = Open("", d); err == nil {
		t.Fatal("non-read plugin accepted")
	}
	if _, err = Open("", definition(), definition()); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestRegistryRejectsStaleMismatchedAndMalformedData(t *testing.T) {
	q := Request{Venue: "binance", Symbol: "BTCUSDT"}
	for _, mutate := range []func(*Result){func(r *Result) { r.Symbol = "ETHUSDT" }, func(r *Result) { r.SourceTime = time.Now().Add(-61 * time.Second) }, func(r *Result) { r.SourceTime = time.Now().Add(11 * time.Second) }, func(r *Result) { r.Metrics[0].Value = "NaN" }, func(r *Result) { r.Metrics = append(r.Metrics, r.Metrics[0]) }, func(r *Result) { r.Mode = "" }} {
		d := definition()
		original := d.Read
		d.Read = func(ctx context.Context, q Request) (Result, error) {
			v, e := original(ctx, q)
			mutate(&v)
			return v, e
		}
		r, _ := Open("", d)
		if _, err := r.Read(context.Background(), "funding", q); err == nil {
			t.Fatal("bad data accepted")
		}
	}
	r, _ := Open("", definition())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx, "funding", q); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := r.Read(context.Background(), "funding", Request{Venue: "binance", Symbol: "BTCUSDT&url=evil"}); err == nil {
		t.Fatal("injection accepted")
	}
}
