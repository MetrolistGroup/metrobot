package auditsearch

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
)

const (
	maxAttachmentBytes     = 128 * 1024
	maxAttachmentBudget    = 1024 * 1024
	maxAttachmentDownloads = 20
)

type attachmentBudget struct{ bytes, downloads int }

func safeAttachmentURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Host != "cdn.discordapp.com" && u.Host != "media.discordapp.net") {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 5 || parts[1] != "attachments" || parts[4] == "" || path.Clean(u.Path) != u.Path || strings.Contains(u.Path, "\\") {
		return false
	}
	for _, id := range parts[2:4] {
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != id {
			return false
		}
	}
	return true
}

func textAttachment(a discord.Attachment) bool {
	if strings.EqualFold(path.Ext(a.Filename), ".txt") {
		return true
	}
	if a.ContentType == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(*a.ContentType)
	return err == nil && strings.HasPrefix(mediaType, "text/")
}

func attachmentClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = publicCDNDial
	transport.MaxConnsPerHost = 4
	transport.MaxResponseHeaderBytes = 16 * 1024
	transport.ResponseHeaderTimeout = 3 * time.Second
	return &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func publicCDNDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || (host != "cdn.discordapp.com" && host != "media.discordapp.net") {
		return nil, fmt.Errorf("unapproved CDN")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("CDN unavailable")
	}
	for _, ip := range addresses {
		ip = ip.Unmap()
		if !publicIP(ip) {
			return nil, fmt.Errorf("nonpublic CDN address")
		}
	}
	var dialer net.Dialer
	dialer.Timeout = 3 * time.Second
	for _, ip := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("CDN unavailable")
}

func publicIP(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func (s *Service) readAttachment(ctx context.Context, a discord.Attachment, budget *attachmentBudget) (string, string) {
	if !safeAttachmentURL(a.URL) {
		return "", "Partial search: a text attachment has an unapproved URL and was not downloaded."
	}
	if a.Size > maxAttachmentBytes {
		return "", "Partial search: a text attachment exceeds the 128 KiB limit and was not searched."
	}
	if budget.downloads >= maxAttachmentDownloads || budget.bytes <= 0 {
		return "", "Partial search: the text attachment download budget was exhausted; some attachment content was not searched."
	}
	budget.downloads++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", "Partial search: a text attachment could not be downloaded."
	}
	response, err := s.attachments.Do(req)
	if err != nil {
		return "", "Partial search: a text attachment could not be downloaded (unavailable or timed out)."
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "Partial search: a text attachment is unavailable, expired, or redirected; its content was not searched."
	}
	limit := min(maxAttachmentBytes, budget.bytes)
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	budget.bytes -= len(data)
	warning := ""
	if err != nil {
		warning = "Partial search: a text attachment download was interrupted; only received content was searched."
	}
	if len(data) > limit {
		data = data[:limit]
		warning = "Partial search: a text attachment was truncated by the download size budget; only its prefix was searched."
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return "", "Partial search: a text attachment was not valid UTF-8 text and was not searched."
	}
	return string(data), warning
}
