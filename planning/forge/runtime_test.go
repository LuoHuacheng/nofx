package forge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/geometric_pyramid.star")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func sampleOutput(t *testing.T) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{"ideas":[{"name":"Geometric weighted ladder","side":"long","entry":"100000","stop":"99000","target":"104000","rationale":"Conditional pullback ladder with quantity weights 1:2:4; no fill or profit guarantee","execution":{"kind":"ladder","levels":[{"price":"100000","weight":"0.142857"},{"price":"99900","weight":"0.285714"},{"price":"99800","weight":"0.571429"}]}}]}`)
}
func definition(t *testing.T) Definition {
	return Definition{ID: "geometric_pyramid", Description: "finite allocation", Source: sample(t), Tests: []TestCase{{Name: "geometric weights", Args: map[string]any{"gap_bps": float64(10)}, Expected: sampleOutput(t)}, {Name: "invalid gap", Args: map[string]any{"gap_bps": float64(0)}, ExpectError: true}}}
}
func TestWorkerAndImmutableRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forge.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Develop(context.Background(), "session-a", definition(t))
	if err != nil || a.Status != "passed" {
		t.Fatalf("develop: %+v %v", a, err)
	}
	a.Tests[0].Args["gap_bps"] = 999.
	safe, _ := r.Get("session-a", a.Hash)
	if safe.Tests[0].Args["gap_bps"] != float64(10) {
		t.Fatal("returned development package mutated registry")
	}
	out, err := r.Run(context.Background(), "session-a", a.Hash, fixtureMarket(), map[string]any{"gap_bps": float64(10)})
	if err != nil || len(out.Ideas) != 1 {
		t.Fatalf("run: %+v %v", out, err)
	}
	if _, err = r.Run(context.Background(), "other", a.Hash, fixtureMarket(), nil); err == nil {
		t.Fatal("cross-session use")
	}
	listed := r.List("session-a")
	listed[0].Source = "changed"
	listed[0].Tests[0].Args["gap_bps"] = 999.
	saved, _ := r.Get("session-a", a.Hash)
	if saved.Source != a.Source || saved.Tests[0].Args["gap_bps"] != float64(10) {
		t.Fatal("external mutation")
	}
	if err = r.SetEnabled("session-a", a.Hash, false); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Run(context.Background(), "session-a", a.Hash, fixtureMarket(), nil); err == nil {
		t.Fatal("disabled calculator ran")
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ = r.Get("session-a", a.Hash)
	if saved.Enabled {
		t.Fatal("disable lost on restart")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal("unsafe file mode")
	}
	d := definition(t)
	d.Tests[0].Expected = json.RawMessage(`{"ideas":[]}`)
	failed, err := r.Develop(context.Background(), "session-a", d)
	if err != nil || failed.Status != "failed" || failed.Enabled {
		t.Fatalf("failed test enabled: %+v %v", failed, err)
	}
	if err = r.SetEnabled("session-a", failed.Hash, true); err == nil {
		t.Fatal("failed version installed")
	}
	raw, _ := os.ReadFile(path)
	raw = []byte(strings.Replace(string(raw), "finite allocation", "tampered", 1))
	os.WriteFile(path, raw, 0600)
	if _, err = Open(path); err == nil {
		t.Fatal("tampered package accepted")
	}
}
func TestDialectDeniesUnsafeOrUnboundedOperations(t *testing.T) {
	for _, source := range []string{`load("/etc/passwd", "secret")`, "def plan(m,a):\n    return open('/etc/passwd')", "def plan(m,a):\n    return 'x' * 1000000000", "def plan(m,a):\n    return [1] + [2]", "def plan(m,a):\n    return a.get('key')", "def plan(m,a):\n    return print('secret')", "def plan(m,a):\n    return {\"ideas\":[],\"orders\":[1]}"} {
		t.Run(source, func(t *testing.T) {
			if _, err := Run(context.Background(), Invocation{Source: source, Market: fixtureMarket(), Args: map[string]any{}}); err == nil {
				t.Fatal("unsafe code accepted")
			}
		})
	}
}
func TestStepBudgetCancellationAndContainerBound(t *testing.T) {
	for _, source := range []string{"def plan(m,a):\n    for i in range(64):\n        for j in range(64):\n            for k in range(64):\n                x = i - j\n    return {\"ideas\":[]}", "def plan(m,a):\n    x=[]\n    for i in range(64):\n        for j in range(64):\n            append(x,i)\n    return {\"ideas\":[]}", "def plan(m,a):\n    return range(1000000)"} {
		start := time.Now()
		if _, err := Run(context.Background(), Invocation{Source: source, Market: fixtureMarket(), Args: map[string]any{}}); err == nil {
			t.Fatal("unbounded program passed")
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("resource bound failed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Invocation{Source: sample(t), Market: fixtureMarket(), Args: map[string]any{"gap_bps": 10.}}); err == nil {
		t.Fatal("cancelled worker ran")
	}
}
