package gsmarena

import (
	"context"
	_ "embed"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

//go:embed proxies.txt
var bundledProxies string

type proxyPool struct {
	urls []*url.URL
	next atomic.Uint64
}

func newProxyPool(raw string) *proxyPool {
	pool := &proxyPool{}
	for line := range strings.Lines(raw) {
		proxyURL, err := url.Parse(strings.TrimSpace(line))
		if err == nil && proxyURL.Host != "" && (proxyURL.Scheme == "http" || proxyURL.Scheme == "socks5") {
			pool.urls = append(pool.urls, proxyURL)
		}
	}
	return pool
}

func (p *proxyPool) do(request *http.Request, base *http.Client) (*http.Response, error) {
	if p == nil || len(p.urls) == 0 {
		return nil, errors.New("no GSMArena proxies configured")
	}
	ctx, cancel := context.WithTimeout(request.Context(), 6*time.Second)
	defer cancel()
	start := p.next.Add(1) - 1
	for attempt := range min(6, len(p.urls)) {
		transport, err := proxyTransport(p.urls[(int(start)+attempt)%len(p.urls)])
		if err != nil {
			continue
		}
		client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: base.CheckRedirect}
		response, err := client.Do(request.Clone(ctx))
		transport.CloseIdleConnections()
		if err != nil {
			continue
		}
		if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusTooManyRequests {
			return response, nil
		}
		response.Body.Close()
	}
	return nil, errors.New("no GSMArena proxy succeeded")
}

func proxyTransport(proxyURL *url.URL) (*http.Transport, error) {
	direct := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{DialContext: direct.DialContext, TLSHandshakeTimeout: 2 * time.Second, DisableKeepAlives: true}
	if proxyURL.Scheme == "http" {
		transport.Proxy = http.ProxyURL(proxyURL)
		return transport, nil
	}
	var auth *proxy.Auth
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		auth = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
	}
	dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, direct)
	if err != nil {
		return nil, err
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS5 proxy does not support contexts")
	}
	transport.DialContext = contextDialer.DialContext
	return transport, nil
}
