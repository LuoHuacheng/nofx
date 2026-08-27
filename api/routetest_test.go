package api

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestBacktestRouteRegistration asserts every documented backtest route
// is wired into the router (P3.4).
func TestBacktestRouteRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{router: gin.New()}
	s.setupRoutes()
	for _, r := range s.router.Routes() {
		if len(r.Path) >= 15 && r.Path[:15] == "/api/backtest/" {
			fmt.Println(r.Method, r.Path)
		}
	}
	got := map[string]bool{}
	for _, r := range s.router.Routes() {
		if strings.Contains(r.Path, "backtest") {
			got[r.Method+" "+r.Path] = true
		}
	}
	want := []string{
		"POST /api/backtest/start", "POST /api/backtest/pause", "POST /api/backtest/resume",
		"POST /api/backtest/stop", "POST /api/backtest/delete",
		"GET /api/backtest/status", "GET /api/backtest/runs", "GET /api/backtest/equity",
		"GET /api/backtest/trades", "GET /api/backtest/metrics", "GET /api/backtest/trace",
		"GET /api/backtest/decisions", "GET /api/backtest/export", "GET /api/backtest/klines",
		"GET /api/backtest/download/:filename",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing route: %s", w)
		}
	}
}
