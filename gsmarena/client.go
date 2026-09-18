package gsmarena

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/db"
)

const (
	baseURL          = "https://www.gsmarena.com/"
	imageURL         = "https://fdn2.gsmarena.com/vv/bigpic/"
	indexCacheTTL    = 24 * time.Hour
	phoneCacheTTL    = 7 * 24 * time.Hour
	maxResponseBytes = 8 << 20
)

type Client struct {
	db         *db.DB
	httpClient *http.Client
	proxies    *proxyPool
	baseURL    string
	indexMu    sync.Mutex
	index      *quickSearchIndex
	indexAt    time.Time
}

type Phone struct {
	Brand   string        `json:"brand,omitempty"`
	ID      int64         `json:"id"`
	Name    string        `json:"name"`
	Slug    string        `json:"slug"`
	URL     string        `json:"url"`
	Image   string        `json:"image,omitempty"`
	Summary string        `json:"summary,omitempty"`
	Specs   []SpecSection `json:"specs"`
}

type SpecSection struct {
	Name  string     `json:"name"`
	Items []SpecItem `json:"items"`
}

type SpecItem struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

func New(database *db.DB) *Client {
	client := newClient(database, baseURL, nil)
	client.proxies = newProxyPool(bundledProxies)
	return client
}

func newClient(database *db.DB, root string, httpClient *http.Client) *Client {
	root = strings.TrimRight(root, "/") + "/"
	if httpClient == nil {
		origin, _ := url.Parse(root)
		httpClient = &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				if request.URL.Scheme != origin.Scheme || !strings.EqualFold(request.URL.Host, origin.Host) {
					return fmt.Errorf("refusing cross-origin redirect to %s", request.URL.Host)
				}
				return nil
			},
		}
	}
	return &Client{db: database, httpClient: httpClient, baseURL: root}
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]Phone, error) {
	query = normalizeSearch(query)
	if query == "" {
		return nil, errors.New("GSMArena search query is required")
	}
	if len([]rune(query)) > 100 {
		return nil, errors.New("GSMArena search query cannot exceed 100 characters")
	}
	if limit <= 0 || limit > 25 {
		limit = 25
	}
	index, err := c.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	phones := rankSearch(index, query, limit)
	if len(phones) == 0 {
		phones = rankSearch(index, simplifySearch(query, false), limit)
	}
	if len(phones) == 0 {
		phones = rankSearch(index, simplifySearch(query, true), limit)
	}
	return phones, nil
}

func (c *Client) Lookup(ctx context.Context, query string) (Phone, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Phone{}, errors.New("GSMArena phone is required")
	}
	if len([]rune(query)) > 200 {
		return Phone{}, errors.New("GSMArena phone cannot exceed 200 characters")
	}
	if _, err := strconv.ParseInt(query, 10, 64); err == nil || strings.Contains(strings.ToLower(query), "gsmarena.com/") || phoneIDFromSlug(query) != 0 {
		return c.Phone(ctx, query)
	}
	phones, err := c.Search(ctx, query, 25)
	if err != nil {
		return Phone{}, err
	}
	if len(phones) == 0 {
		return Phone{}, fmt.Errorf("no GSMArena phone matched %q", query)
	}
	return c.Phone(ctx, strconv.FormatInt(phones[0].ID, 10))
}

func (c *Client) Phone(ctx context.Context, slugOrID string) (Phone, error) {
	value := strings.TrimSpace(slugOrID)
	if value == "" {
		return Phone{}, errors.New("GSMArena phone is required")
	}

	var target string
	var fallback Phone
	if id, err := strconv.ParseInt(value, 10, 64); err == nil {
		index, err := c.loadIndex(ctx)
		if err != nil {
			return Phone{}, err
		}
		for _, record := range index.Records {
			if record.ID == id {
				fallback = index.phone(record, c.baseURL)
				target = fallback.URL
				break
			}
		}
		if target == "" {
			return Phone{}, fmt.Errorf("GSMArena phone ID %d was not found", id)
		}
	} else {
		var err error
		target, fallback, err = detailTarget(c.baseURL, value)
		if err != nil {
			return Phone{}, err
		}
	}

	cacheKey := "phone:" + fallback.Slug
	if fallback.ID != 0 {
		cacheKey = "phone:" + strconv.FormatInt(fallback.ID, 10)
	}
	if c.db != nil {
		if cached, found, err := c.db.GetGSMArenaCache(cacheKey, phoneCacheTTL); err != nil {
			return Phone{}, fmt.Errorf("read GSMArena cache: %w", err)
		} else if found {
			var phone Phone
			if json.Unmarshal(cached, &phone) == nil && phone.ID != 0 && len(phone.Specs) > 0 {
				return phone, nil
			}
		}
	}

	contents, err := c.fetch(ctx, target, "text/html")
	if err != nil {
		return Phone{}, fmt.Errorf("fetch GSMArena phone: %w", err)
	}
	phone, err := parsePhonePage(contents, fallback, c.baseURL)
	if err != nil {
		return Phone{}, fmt.Errorf("parse GSMArena phone: %w", err)
	}
	if c.db != nil {
		encoded, _ := json.Marshal(phone)
		if err := c.db.SetGSMArenaCache(cacheKey, encoded); err != nil {
			return Phone{}, fmt.Errorf("write GSMArena cache: %w", err)
		}
	}
	return phone, nil
}

func (c *Client) loadIndex(ctx context.Context) (quickSearchIndex, error) {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	if c.index != nil && time.Since(c.indexAt) < indexCacheTTL {
		return *c.index, nil
	}
	if c.db != nil {
		if cached, found, err := c.db.GetGSMArenaCache("index", indexCacheTTL); err != nil {
			return quickSearchIndex{}, fmt.Errorf("read GSMArena cache: %w", err)
		} else if found {
			var index quickSearchIndex
			if json.Unmarshal(cached, &index) == nil && len(index.Records) > 0 {
				c.index, c.indexAt = &index, time.Now()
				return index, nil
			}
		}
	}

	homepage, err := c.fetch(ctx, c.baseURL, "text/html")
	if err != nil {
		return quickSearchIndex{}, fmt.Errorf("fetch GSMArena homepage: %w", err)
	}
	indexPath := quickSearchPattern.FindString(string(homepage))
	if indexPath == "" {
		return quickSearchIndex{}, errors.New("GSMArena homepage did not contain a quicksearch index")
	}
	contents, err := c.fetch(ctx, c.baseURL+indexPath, "application/json")
	if err != nil {
		return quickSearchIndex{}, fmt.Errorf("fetch GSMArena quicksearch index: %w", err)
	}
	index, err := parseQuickSearch(contents)
	if err != nil {
		return quickSearchIndex{}, err
	}
	if c.db != nil {
		encoded, _ := json.Marshal(index)
		if err := c.db.SetGSMArenaCache("index", encoded); err != nil {
			return quickSearchIndex{}, fmt.Errorf("write GSMArena cache: %w", err)
		}
	}
	c.index, c.indexAt = &index, time.Now()
	return index, nil
}

func (c *Client) fetch(ctx context.Context, target, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/138.0.0.0 Safari/537.36")
	response, err := c.httpClient.Do(request)
	if c.proxies != nil && (err != nil || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests) {
		if response != nil {
			response.Body.Close()
		}
		response, err = c.proxies.do(request, c.httpClient)
	}
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeded %d bytes", maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if antiBotPage(body) {
		return nil, errors.New("GSMArena returned an anti-bot challenge")
	}
	return body, nil
}

type quickSearchIndex struct {
	Brands  map[int64]string    `json:"brands"`
	Records []quickSearchRecord `json:"records"`
}

type quickSearchRecord struct {
	BrandID int64    `json:"brand_id"`
	ID      int64    `json:"id"`
	Model   string   `json:"model"`
	Aliases []string `json:"aliases,omitempty"`
	Image   string   `json:"image,omitempty"`
	Display string   `json:"display,omitempty"`
}

type rankedPhone struct {
	phone Phone
	rank  int
}

func rankSearch(index quickSearchIndex, query string, limit int) []Phone {
	query = normalizeSearch(query)
	if query == "" {
		return nil
	}
	ranked := make([]rankedPhone, 0)
	seen := make(map[int64]int)
	for _, record := range index.Records {
		rank := record.matchRank(index.Brands[record.BrandID], query)
		if rank < 0 {
			continue
		}
		phone := index.phone(record, baseURL)
		if previous, found := seen[record.ID]; found {
			if rank < ranked[previous].rank {
				ranked[previous] = rankedPhone{phone: phone, rank: rank}
			}
			continue
		}
		seen[record.ID] = len(ranked)
		ranked = append(ranked, rankedPhone{phone: phone, rank: rank})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].rank < ranked[j].rank })
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	phones := make([]Phone, len(ranked))
	for index, match := range ranked {
		phones[index] = match.phone
	}
	return phones
}

func (index quickSearchIndex) phone(record quickSearchRecord, root string) Phone {
	name := record.Display
	if name == "" {
		name = record.Model
	}
	brand := index.Brands[record.BrandID]
	slug := phoneSlug(brand, record.Model, record.ID)
	phone := Phone{Brand: brand, ID: record.ID, Name: name, Slug: slug, URL: root + slug + ".php"}
	if record.Image != "" {
		phone.Image = imageURL + record.Image
	}
	return phone
}

func (record quickSearchRecord) matchRank(brand, query string) int {
	fields := []string{brand, record.Model, record.Display, brand + " " + record.Model, brand + " " + record.Display}
	fields = append(fields, record.Aliases...)
	for _, field := range fields {
		if normalizeSearch(field) == query {
			return 0
		}
	}
	for _, field := range fields {
		if strings.HasPrefix(normalizeSearch(field), query) {
			return 1
		}
	}
	for _, field := range fields {
		if strings.Contains(normalizeSearch(field), query) {
			return 2
		}
	}
	return -1
}

func phoneSlug(brand, model string, id int64) string {
	return strings.Trim(normalizeSlug(brand)+"_"+normalizeSlug(model), "_") + "-" + strconv.FormatInt(id, 10)
}

func normalizeSlug(value string) string {
	var result strings.Builder
	separator := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '(' || char == ')' {
			if separator && result.Len() > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(char)
			separator = false
		} else {
			separator = true
		}
	}
	return strings.Trim(result.String(), "_")
}

func normalizeSearch(value string) string {
	var normalized strings.Builder
	space := false
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if space && normalized.Len() > 0 {
				normalized.WriteByte(' ')
			}
			normalized.WriteRune(char)
			space = false
		} else {
			space = true
		}
	}
	return normalized.String()
}

func simplifySearch(query string, drop4G bool) string {
	stop := map[string]bool{
		"a": true, "about": true, "an": true, "are": true, "battery": true, "camera": true,
		"cameras": true, "can": true, "charging": true, "chipset": true, "details": true,
		"display": true, "do": true, "does": true, "for": true, "garmin": true, "get": true,
		"give": true, "gsmarena": true, "has": true, "have": true, "how": true, "info": true,
		"information": true, "is": true, "its": true, "look": true, "me": true, "memory": true,
		"metrobot": true, "much": true, "network": true, "of": true, "on": true, "os": true,
		"please": true, "processor": true, "ram": true, "released": true, "s": true, "screen": true,
		"search": true, "show": true, "size": true, "soc": true, "software": true, "spec": true,
		"specification": true, "specifications": true, "specs": true, "storage": true, "tell": true,
		"the": true, "use": true, "uses": true, "using": true, "what": true, "whats": true,
		"which": true, "with": true,
	}
	fields := strings.Fields(normalizeSearch(query))
	kept := fields[:0]
	for _, field := range fields {
		if !stop[field] && (!drop4G || field != "4g") {
			kept = append(kept, field)
		}
	}
	return strings.Join(kept, " ")
}

func detailTarget(root, value string) (string, Phone, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", Phone{}, fmt.Errorf("invalid GSMArena phone %q", value)
	}
	if parsed.Host != "" {
		host := strings.ToLower(parsed.Hostname())
		if host != "gsmarena.com" && !strings.HasSuffix(host, ".gsmarena.com") {
			return "", Phone{}, fmt.Errorf("invalid GSMArena phone host %q", parsed.Host)
		}
	}
	filename := strings.TrimSuffix(strings.Trim(strings.TrimSpace(parsed.Path), "/"), ".php")
	if index := strings.LastIndex(filename, "/"); index >= 0 {
		filename = filename[index+1:]
	}
	if filename == "" || strings.ContainsAny(filename, "?#") {
		return "", Phone{}, fmt.Errorf("invalid GSMArena phone %q", value)
	}
	id := phoneIDFromSlug(filename)
	if id == 0 {
		return "", Phone{}, fmt.Errorf("invalid GSMArena phone %q", value)
	}
	target := root + filename + ".php"
	return target, Phone{ID: id, Slug: filename, URL: target}, nil
}
