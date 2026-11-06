// Package plugins hosts trusted, read-only data components. It never receives
// trade storage, approval handles, credentials or an executable shell.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"nofx/planning/instrument"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

type Request struct {
	Window string `json:"window,omitempty"`
	Venue  string `json:"venue"`
	Symbol string `json:"symbol"`
}
type Metric struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit"`
}
type Result struct {
	Visual     *Visual   `json:"visual,omitempty"`
	Plugin     string    `json:"plugin"`
	Version    string    `json:"version"`
	Venue      string    `json:"venue"`
	Symbol     string    `json:"symbol"`
	Source     string    `json:"source"`
	Observed   time.Time `json:"observed"`
	SourceTime time.Time `json:"source_time,omitempty"`
	Metrics    []Metric  `json:"metrics"`
	Notice     string    `json:"notice"`
	Mode       string    `json:"mode"`
}
type Manifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Capability  string `json:"capability"`
	Enabled     bool   `json:"enabled"`
}
type Definition struct {
	Manifest Manifest
	Read     func(context.Context, Request) (Result, error)
}
type Source interface {
	PluginData(context.Context, string, Request) (Result, error)
}
type Registry struct {
	mu          sync.RWMutex
	path        string
	definitions map[string]Definition
	enabled     map[string]bool
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
var decimal = regexp.MustCompile(`^-?[0-9]{1,30}(\.[0-9]{1,20})?$`)

func Open(path string, definitions ...Definition) (*Registry, error) {
	if len(definitions) > 8 {
		return nil, errors.New("at most eight data plugins may be registered")
	}
	r := &Registry{path: path, definitions: map[string]Definition{}, enabled: map[string]bool{}}
	for _, d := range definitions {
		if !identifier.MatchString(d.Manifest.ID) || d.Manifest.Capability != "public.read" || d.Manifest.Version == "" || len(d.Manifest.Version) > 30 || len(d.Manifest.Name) > 100 || len(d.Manifest.Description) > 1200 || d.Read == nil {
			return nil, errors.New("invalid read-only plugin definition")
		}
		if _, ok := r.definitions[d.Manifest.ID]; ok {
			return nil, errors.New("duplicate plugin ID")
		}
		r.definitions[d.Manifest.ID] = d
		r.enabled[d.Manifest.ID] = d.Manifest.Enabled
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			var saved map[string]bool
			if len(b) > 4096 || json.Unmarshal(b, &saved) != nil {
				return nil, errors.New("invalid plugin settings")
			}
			for id, on := range saved {
				if _, ok := r.definitions[id]; ok {
					r.enabled[id] = on
				}
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return r, nil
}
func Builtins(source Source) []Definition {
	defs := []Definition{}
	for _, m := range []Manifest{{ID: "funding", Name: "Funding rate", Version: "1.0.0", Description: "Public funding and mark prices. Settlement conventions vary by venue; not automatically included in this trade risk estimate.", Capability: "public.read", Enabled: true}, {ID: "open_interest", Name: "Open interest", Version: "1.0.0", Description: "Current open interest in venue-native units. One snapshot does not establish a directional increase.", Capability: "public.read", Enabled: true}} {
		id := m.ID
		defs = append(defs, Definition{Manifest: m, Read: func(ctx context.Context, q Request) (Result, error) {
			if source == nil {
				return Result{}, errors.New("This market source does not provide the requested plugin")
			}
			return source.PluginData(ctx, id, q)
		}})
	}
	return defs
}
func (r *Registry) List() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []Manifest{}
	for id, d := range r.definitions {
		m := d.Manifest
		m.Enabled = r.enabled[id]
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
func (r *Registry) SetEnabled(id string, on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.definitions[id]; !ok {
		return errors.New("unknown plugin")
	}
	next := map[string]bool{}
	for k, v := range r.enabled {
		next[k] = v
	}
	next[id] = on
	if r.path != "" {
		if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(r.path), ".plugin-settings-*")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		b, _ := json.Marshal(next)
		err = f.Chmod(0600)
		if err == nil {
			_, err = f.Write(b)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(name, r.path)
		}
		if err != nil {
			return err
		}
	}
	r.enabled = next
	return nil
}
func (r *Registry) Read(ctx context.Context, id string, q Request) (Result, error) {
	if !instrument.ValidSymbol(q.Symbol) || (q.Venue != "binance" && q.Venue != "okx" && q.Venue != "bitget" && q.Venue != "hyperliquid") {
		return Result{}, errors.New("invalid plugin workspace")
	}
	r.mu.RLock()
	d, ok := r.definitions[id]
	on := r.enabled[id]
	r.mu.RUnlock()
	if !ok || !on {
		return Result{}, errors.New("plugin unavailable or disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := d.Read(ctx, q)
	if err != nil {
		return Result{}, err
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	if out.Venue != q.Venue || out.Symbol != q.Symbol || len(out.Metrics) == 0 || len(out.Metrics) > 8 || out.Source == "" || len(out.Source) > 300 || len(out.Notice) > 1200 || (out.Mode != "public" && out.Mode != "demo" && out.Mode != "paid") {
		return Result{}, errors.New("invalid canonical plugin result")
	}
	now := time.Now()
	if out.Observed.IsZero() || now.Sub(out.Observed) > 30*time.Second || out.Observed.After(now.Add(time.Second)) {
		return Result{}, errors.New("stale plugin receipt")
	}
	if !out.SourceTime.IsZero() && (now.Sub(out.SourceTime) > 60*time.Second || out.SourceTime.After(now.Add(10*time.Second))) {
		return Result{}, errors.New("stale plugin source timestamp")
	}
	seen := map[string]bool{}
	for _, m := range out.Metrics {
		n, valid := new(big.Rat).SetString(m.Value)
		if !identifier.MatchString(m.Key) || seen[m.Key] || !decimal.MatchString(m.Value) || !valid || len(m.Label) > 100 || len(m.Unit) > 30 || (m.Key == "open_interest" && n.Sign() < 0) {
			return Result{}, errors.New("invalid plugin metric")
		}
		seen[m.Key] = true
		if m.Key == "mark_price" && n.Sign() <= 0 {
			return Result{}, errors.New("invalid plugin mark price")
		}
	}
	out.Plugin = id
	out.Version = d.Manifest.Version
	return out, nil
}
