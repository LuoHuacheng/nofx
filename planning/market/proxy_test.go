package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalProxyRoutesOnlyConfiguredClient(t *testing.T) {
	called := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Host != "unresolved.invalid" {
			t.Error("original host not retained")
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer proxy.Close()
	c, err := NewWithProxy(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]bool
	if err = c.request(context.Background(), "GET", "http://unresolved.invalid/public", nil, &result); err != nil {
		t.Fatal(err)
	}
	if !called || !result["ok"] {
		t.Fatal("proxy not used")
	}
	for _, bad := range []string{"http://remote.example:7897", "http://user:secret@127.0.0.1:7897", "socks5://127.0.0.1:7897", "http://127.0.0.1:7897/private"} {
		if _, err := NewWithProxy(bad); err == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
}
