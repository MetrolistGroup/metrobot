// Package technicalcity retrieves CPU and GPU specifications from Technical City.
package technicalcity

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

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/gsmarena"
)

const (
	baseURL          = "https://technical.city"
	searchTTL        = 24 * time.Hour
	detailTTL        = 7 * 24 * time.Hour
	maxResponseBytes = 8 << 20
)

// Device is a CPU or GPU. Slug includes its category, e.g. cpu/Core-i9-14900K.
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

func New(database *db.DB) *Client {
	return &Client{db: database, httpClient: &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("Technical City: too many redirects")
			}
			return validateTarget(req.URL)
		},
	}}
}

// Search returns site search results in site order, capped at 25 for autocomplete.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Device, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" || len([]rune(query)) > 100 {
		return nil, errors.New("Technical City search requires 1–100 characters")
	}
	if limit <= 0 || limit > 25 {
		limit = 25
	}
	key := "tc:search:" + strings.ToLower(query)
	var devices []Device
	found, err := c.readCache(key, searchTTL, &devices)
	if err != nil {
		return nil, err
	}
	if !found {
		body, err := c.fetch(ctx, baseURL+"/en/search?q="+url.QueryEscape(query))
		if err != nil {
			return nil, err
		}
		devices, err = parseSearch(body)
		if err != nil {
			return nil, err
		}
		if err := c.writeCache(key, devices); err != nil {
			return nil, err
		}
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no Technical City device matched %q", query)
	}
	if len(devices) > limit {
		devices = devices[:limit]
	}
	return devices, nil
}

func (c *Client) Lookup(ctx context.Context, query string) (Device, error) {
	query = strings.TrimSpace(query)
	if strings.ContainsAny(query, "/:") {
		return c.Device(ctx, query)
	}
	devices, err := c.Search(ctx, query, 25)
	if err != nil {
		return Device{}, err
	}
	for _, device := range devices {
		if searchText(device.Name) == searchText(query) || searchText(slugName(device.Slug)) == searchText(query) {
			return c.Device(ctx, device.Slug)
		}
	}
	return c.Device(ctx, devices[0].Slug)
}

// Device accepts a site URL, a category-qualified slug, or an exact bare slug.
func (c *Client) Device(ctx context.Context, slugOrURL string) (Device, error) {
	value := strings.TrimSpace(slugOrURL)
	if slugPattern.MatchString(value) && !strings.Contains(value, "..") {
		devices, err := c.Search(ctx, strings.ReplaceAll(value, "-", " "), 25)
		if err != nil {
			return Device{}, err
		}
		value = ""
		for _, device := range devices {
			if strings.EqualFold(slugName(device.Slug), strings.TrimSpace(slugOrURL)) {
				value = device.Slug
				break
			}
		}
		if value == "" {
			return Device{}, fmt.Errorf("no Technical City device matched slug %q", slugOrURL)
		}
	}
	device, err := detailTarget(value)
	if err != nil {
		return Device{}, err
	}
	key := "tc:device:" + device.Slug
	var cached Device
	found, err := c.readCache(key, detailTTL, &cached)
	if err != nil {
		return Device{}, err
	}
	if found && cached.Slug == device.Slug && cached.Name != "" && len(cached.Specs) > 0 {
		return cached, nil
	}
	body, err := c.fetch(ctx, device.URL)
	if err != nil {
		return Device{}, err
	}
	device, err = parseDevice(body, device)
	if err != nil {
		return Device{}, err
	}
	if err := c.writeCache(key, device); err != nil {
		return Device{}, err
	}
	return device, nil
}

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_()+.\-]*$`)

func slugName(slug string) string {
	return slug[strings.LastIndex(slug, "/")+1:]
}

func detailTarget(value string) (Device, error) {
	u, err := url.Parse(value)
	if err != nil || value == "" {
		return Device{}, errors.New("invalid Technical City device URL or slug")
	}
	if u.Host != "" || u.Scheme != "" {
		if u.Scheme != "https" || !strings.EqualFold(u.Host, "technical.city") || u.User != nil {
			return Device{}, errors.New("Technical City device must use https://technical.city")
		}
	}
	if u.RawQuery != "" || u.ForceQuery || u.RawPath != "" || u.Opaque != "" {
		return Device{}, errors.New("invalid Technical City device path")
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(u.Path, "/"), "en/"), "/")
	if len(parts) != 2 || (parts[0] != "cpu" && parts[0] != "gpu" && parts[0] != "video") || !slugPattern.MatchString(parts[1]) || strings.Contains(parts[1], "..") || strings.Contains(strings.ToLower(parts[1]), "-vs-") {
		return Device{}, errors.New("Technical City requires a CPU or GPU detail path")
	}
	if parts[0] == "video" {
		parts[0] = "gpu"
	}
	slug := strings.Join(parts, "/")
	return Device{Slug: slug, URL: baseURL + "/en/" + slug}, nil
}

func validateTarget(u *url.URL) error {
	if u.Scheme != "https" || !strings.EqualFold(u.Host, "technical.city") || u.User != nil || u.Opaque != "" || u.RawPath != "" {
		return errors.New("refusing non-Technical City URL")
	}
	if u.Path == "/en/search" {
		q, err := url.ParseQuery(u.RawQuery)
		if err == nil && len(q) == 1 && len(q["q"]) == 1 && q.Get("q") != "" {
			return nil
		}
		return errors.New("invalid Technical City search URL")
	}
	device, err := detailTarget(u.String())
	if err != nil {
		return err
	}
	// Redirects must retain the site's English detail route (including its old GPU alias).
	if u.Path != "/en/"+device.Slug && u.Path != "/en/"+strings.Replace(device.Slug, "gpu/", "video/", 1) {
		return errors.New("invalid Technical City redirect path")
	}
	return nil
}

func (c *Client) fetch(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if err := validateTarget(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Metrobot)")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Technical City: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Technical City returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("Technical City response exceeds 8 MiB")
	}
	return body, nil
}

func (c *Client) readCache(key string, ttl time.Duration, value any) (bool, error) {
	if c.db == nil {
		return false, nil
	}
	body, found, err := c.db.GetGSMArenaCache(key, ttl)
	if err != nil {
		return false, fmt.Errorf("read Technical City cache: %w", err)
	}
	return found && json.Unmarshal(body, value) == nil, nil
}

func (c *Client) writeCache(key string, value any) error {
	if c.db == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err == nil {
		err = c.db.SetGSMArenaCache(key, body)
	}
	if err != nil {
		return fmt.Errorf("write Technical City cache: %w", err)
	}
	return nil
}
