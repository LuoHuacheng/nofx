package payment

import (
	"crypto/tls"
	"net/http"
)

// singleUsePaidClient retains the caller's TLS verification, proxy, dialer and
// timeout, but prevents redirect forwarding and transparent retries on reused
// connections. Never mutate the shared discovery transport.
func singleUsePaidClient(client *http.Client) *http.Client {
	paid := *client
	paid.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if original, ok := transport.(*http.Transport); ok {
		fresh := original.Clone()
		fresh.DisableKeepAlives = true
		fresh.ForceAttemptHTTP2 = false
		fresh.Protocols = new(http.Protocols)
		fresh.Protocols.SetHTTP1(true)
		// Clone can retain ALPN "h2" from an earlier discovery request, even
		// though HTTP/2 dispatch is now disabled. Both the TLS handshake and
		// HTTP parser must use HTTP/1.1, or HTTP/2 frames look like garbage.
		if fresh.TLSClientConfig == nil {
			fresh.TLSClientConfig = &tls.Config{}
		}
		fresh.TLSClientConfig.NextProtos = []string{"http/1.1"}
		fresh.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
		paid.Transport = fresh
	}
	return &paid
}
