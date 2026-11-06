package market

import (
	"errors"
	"net/http"
	"net/url"
)

// A project-specific local proxy resolves exchange hosts without changing global
// network settings. HTTPS continues to verify the original exchange certificate.
// It is used for public exchange requests only, never model credentials.
func NewWithProxy(address string) (*Client, error) {
	c := New()
	if address == "" {
		return c, nil
	}
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.User != nil || u.Port() == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("VERGEX_MARKET_PROXY must be an HTTP(S) loopback proxy with a port and no credentials/path")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(u)
	c.HTTP.Transport = transport
	return c, nil
}
