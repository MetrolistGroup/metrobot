// Package nanoreview retrieves NanoReview product search results and specifications.
package nanoreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/gsmarena"
)

const (
	baseURL          = "https://nanoreview.net"
	searchTTL        = 24 * time.Hour
	detailTTL        = 7 * 24 * time.Hour
	maxResponseBytes = 4 << 20
	maxResults       = 25
)

// Device describes a phone, CPU, GPU, laptop, or mobile SoC. Slug includes the
// category, for example "phone/oneplus-15", and can be passed directly to Device.
type Device struct {
	Name    string                 `json:"name"`
	Slug    string                 `json:"slug"`
	URL     string                 `json:"url"`
	Image   string                 `json:"image,omitempty"`
	Summary string                 `json:"summary,omitempty"`
	Specs   []gsmarena.SpecSection `json:"specs"`
}

type Client struct {
	db         *db.DB
	httpClient *http.Client
}

// New creates a client. A nil database disables caching.
func New(database *db.DB) *Client {
	return &Client{db: database, httpClient: &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("NanoReview: too many redirects")
			}
			return validateTarget(req.URL)
		},
	}}
}

// Search uses NanoReview's autocomplete API. Results contain names, stable
// category-qualified slugs and URLs, but not specifications. Limits outside
// 1..25 default to 25. Search failures are returned, never cached as no matches.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.Join(strings.Fields(query), " "))
	if query == "" || len([]rune(query)) > 100 || strings.IndexFunc(query, unicode.IsControl) >= 0 {
		return nil, errors.New("NanoReview search requires 1 to 100 characters")
	}
	if limit <= 0 || limit > maxResults {
		limit = maxResults
	}
	key := "nano:search:" + query
	var devices []Device
	found, err := c.cached(key, searchTTL, &devices)
	if err != nil {
		return nil, err
	}
	if !found {
		params := url.Values{"q": {query}, "limit": {"25"}}
		body, err := c.fetch(ctx, baseURL+"/api/search?"+params.Encode(), "application/json")
		if err != nil {
			return nil, fmt.Errorf("NanoReview search: %w", err)
		}
		devices, err = parseSearch(body)
		if err != nil {
			return nil, err
		}
		if err := c.store(key, devices); err != nil {
			return nil, err
		}
	}
	if len(devices) > limit {
		devices = devices[:limit]
	}
	return devices, nil
}

// Lookup resolves a URL/category-qualified slug directly, or searches a plain
// product name and loads the first match. It never turns a name into a URL slug.
func (c *Client) Lookup(ctx context.Context, query string) (Device, error) {
	query = strings.TrimSpace(query)
	if strings.Contains(query, "://") || strings.HasPrefix(query, "/") {
		return c.Device(ctx, query)
	}
	for _, kind := range []string{"phone", "cpu", "gpu", "laptop", "soc"} {
		if strings.HasPrefix(query, kind+"/") {
			return c.Device(ctx, query)
		}
	}
	devices, err := c.Search(ctx, query, 1)
	if err != nil {
		return Device{}, err
	}
	if len(devices) == 0 {
		return Device{}, fmt.Errorf("no NanoReview device matched %q", query)
	}
	return c.Device(ctx, devices[0].Slug)
}

// Device accepts a category-qualified slug, /en/category/slug path, or HTTPS
// NanoReview URL. Configuration query parameters are intentionally unsupported.
func (c *Client) Device(ctx context.Context, slugOrURL string) (Device, error) {
	if err := ctx.Err(); err != nil {
		return Device{}, err
	}
	device, err := detailTarget(slugOrURL)
	if err != nil {
		return Device{}, err
	}
	key := "nano:detail:" + device.Slug
	var cached Device
	found, err := c.cached(key, detailTTL, &cached)
	if err != nil {
		return Device{}, err
	}
	if found && cached.Slug == device.Slug && cached.URL == device.URL && cached.Name != "" && len(cached.Specs) > 0 {
		return cached, nil
	}
	body, err := c.fetch(ctx, device.URL, "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if err != nil {
		return Device{}, fmt.Errorf("NanoReview device: %w", err)
	}
	device, err = parseDevice(body, device)
	if err != nil {
		return Device{}, err
	}
	if err := c.store(key, device); err != nil {
		return Device{}, err
	}
	return device, nil
}

var detailPath = regexp.MustCompile(`^/en/(phone|cpu|gpu|laptop|soc)/[a-z0-9]+(?:-[a-z0-9]+)*$`)

func detailTarget(value string) (Device, error) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, ":") && !strings.HasPrefix(value, "/") {
		value = "/en/" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return Device{}, fmt.Errorf("invalid NanoReview device: %w", err)
	}
	if u.Scheme == "" && u.Host == "" {
		u.Scheme, u.Host = "https", "nanoreview.net"
	}
	if u.Host == "www.nanoreview.net" {
		u.Host = "nanoreview.net"
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Scheme != "https" || u.Host != "nanoreview.net" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !detailPath.MatchString(u.Path) {
		return Device{}, errors.New("invalid NanoReview device: use a category/slug or https://nanoreview.net/en/category/slug URL")
	}
	return Device{Slug: strings.TrimPrefix(u.Path, "/en/"), URL: baseURL + u.Path}, nil
}

func validateTarget(u *url.URL) error {
	if u.Scheme != "https" || u.Host != "nanoreview.net" || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return errors.New("refusing non-NanoReview request")
	}
	if u.Path == "/api/search" {
		return nil
	}
	_, err := detailTarget(u.String())
	return err
}

func (c *Client) fetch(ctx context.Context, target, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if err := validateTarget(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36")
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d (NanoReview may be blocking automated requests)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("NanoReview response exceeded 4 MiB")
	}
	return body, nil
}

func (c *Client) cached(key string, ttl time.Duration, value any) (bool, error) {
	if c.db == nil {
		return false, nil
	}
	body, found, err := c.db.GetGSMArenaCache(key, ttl)
	if err != nil {
		return false, fmt.Errorf("read NanoReview cache: %w", err)
	}
	return found && json.Unmarshal(body, value) == nil, nil
}

func (c *Client) store(key string, value any) error {
	if c.db == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := c.db.SetGSMArenaCache(key, body); err != nil {
		return fmt.Errorf("write NanoReview cache: %w", err)
	}
	return nil
}
