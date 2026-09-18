package gsmarena

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
		if err == nil && proxyURL.Host != "" && (proxyURL.Scheme == "http" || proxyURL.Scheme == "socks4" || proxyURL.Scheme == "socks5") {
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
	if proxyURL.Scheme == "socks4" {
		transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialSOCKS4(ctx, proxyURL.Host, address)
		}
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

func dialSOCKS4(ctx context.Context, proxyAddress, targetAddress string) (net.Conn, error) {
	connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}
	closeWithError := func(err error) (net.Conn, error) {
		connection.Close()
		return nil, err
	}
	host, portText, err := net.SplitHostPort(targetAddress)
	if err != nil {
		return closeWithError(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return closeWithError(fmt.Errorf("invalid SOCKS4 target port %q", portText))
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		addresses, lookupErr := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if lookupErr != nil {
			return closeWithError(fmt.Errorf("resolve SOCKS4 target %q: %w", host, lookupErr))
		}
		if len(addresses) == 0 {
			return closeWithError(fmt.Errorf("resolve SOCKS4 target %q: no addresses", host))
		}
		ip = addresses[0].To4()
	}
	if ip == nil {
		return closeWithError(errors.New("SOCKS4 target has no IPv4 address"))
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return closeWithError(err)
	}
	request := []byte{4, 1, byte(port >> 8), byte(port), ip[0], ip[1], ip[2], ip[3], 0}
	if _, err := connection.Write(request); err != nil {
		return closeWithError(err)
	}
	response := make([]byte, 8)
	if _, err := io.ReadFull(connection, response); err != nil {
		return closeWithError(err)
	}
	if response[1] != 0x5a {
		return closeWithError(fmt.Errorf("SOCKS4 proxy rejected request with code %d", response[1]))
	}
	_ = connection.SetDeadline(time.Time{})
	return connection, nil
}
