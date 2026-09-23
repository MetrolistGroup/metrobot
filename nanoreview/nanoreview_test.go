package nanoreview

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestParsers(t *testing.T) {
	results, err := parseSearch(fixture(t, "search.json"))
	if err != nil || len(results) != 5 {
		t.Fatalf("search: %v, %v", results, err)
	}
	for i, kind := range []string{"phone", "cpu", "gpu", "laptop", "soc"} {
		t.Run(kind, func(t *testing.T) {
			metadata := results[i]
			if !strings.HasPrefix(metadata.Slug, kind+"/") || metadata.Name == "" || metadata.URL != baseURL+"/en/"+metadata.Slug {
				t.Fatalf("metadata: %+v", metadata)
			}
			device, err := parseDevice(fixture(t, kind+".html"), metadata)
			if err != nil {
				t.Fatal(err)
			}
			if device.Name == "" || device.Summary == "" || len(device.Specs) != 1 || len(device.Specs[0].Items) == 0 {
				t.Fatalf("incomplete detail: %+v", device)
			}
			if kind == "phone" {
				if device.Image != baseURL+"/common/images/phone/oneplus-15-mini@2x.jpeg" || device.Specs[0].Name != "Display" {
					t.Fatalf("phone: %+v", device)
				}
				items := device.Specs[0].Items
				if len(items) != 18 || items[0].Name != "Type" || items[0].Values[0] != "AMOLED" || len(items[13].Values) != 3 || items[17].Values[0] != "∞ Infinity" {
					t.Fatalf("tables / line breaks: %+v", items)
				}
			}
		})
	}
	for _, body := range []string{`null`, `{}`, `<html>Blocked</html>`, `[{"slug":"../../evil","name":"Bad","content_type":"phone"}]`} {
		if _, err := parseSearch([]byte(body)); err == nil {
			t.Fatalf("accepted search %s", body)
		}
	}
	if empty, err := parseSearch([]byte(`[]`)); err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}
	for _, body := range []string{`<title>Just a moment...</title>`, `<h1>Not found</h1>`, strings.ReplaceAll(string(fixture(t, "phone.html")), "/en/phone/oneplus-15", "/en/phone/other-phone")} {
		if _, err := parseDevice([]byte(body), results[0]); err == nil {
			t.Fatal("accepted invalid detail")
		}
	}
}

func TestCacheLookupAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	searchCalls, detailCalls := 0, 0
	client := New(database)
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "nanoreview.net" || req.Header.Get("User-Agent") == "" || req.Header.Get("Accept-Language") == "" {
			t.Fatalf("request: %+v", req)
		}
		switch req.URL.Path {
		case "/api/search":
			searchCalls++
			if req.URL.Query().Get("q") != "oneplus 15" || req.URL.Query().Get("limit") != "25" {
				t.Fatalf("query: %v", req.URL)
			}
			return response(200, string(fixture(t, "search.json"))), nil
		case "/en/phone/oneplus-15":
			detailCalls++
			return response(200, string(fixture(t, "phone.html"))), nil
		default:
			t.Fatalf("unexpected request: %v", req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	results, err := client.Search(ctx, " OnePlus   15 ", 1)
	if err != nil || len(results) != 1 {
		t.Fatalf("search: %v %v", results, err)
	}
	results, err = client.Search(ctx, "oneplus 15", 25)
	if err != nil || len(results) != 5 || searchCalls != 1 {
		t.Fatalf("limit/cache: %v %v, calls %d", results, err, searchCalls)
	}
	device, err := client.Lookup(ctx, "OnePlus 15")
	if err != nil || device.Slug != "phone/oneplus-15" {
		t.Fatalf("lookup: %+v %v", device, err)
	}
	for _, target := range []string{device.Slug, device.URL, "/en/phone/oneplus-15"} {
		if _, err := client.Lookup(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if detailCalls != 1 {
		t.Fatalf("detail cache calls: %d", detailCalls)
	}
	for _, key := range []string{"nano:search:oneplus 15", "nano:detail:phone/oneplus-15"} {
		if _, ok, err := database.GetGSMArenaCache(key, detailTTL); err != nil || !ok {
			t.Fatalf("cache key %s: %v %v", key, ok, err)
		}
	}
	age := func(key string, duration time.Duration) {
		t.Helper()
		if _, err := conn.Exec("UPDATE gsmarena_cache SET fetched_at = ? WHERE cache_key = ?", time.Now().Add(-duration).Unix(), key); err != nil {
			t.Fatal(err)
		}
	}
	age("nano:search:oneplus 15", 25*time.Hour)
	age("nano:detail:phone/oneplus-15", 25*time.Hour)
	if _, err := client.Lookup(ctx, "oneplus 15"); err != nil {
		t.Fatal(err)
	}
	if searchCalls != 2 || detailCalls != 1 {
		t.Fatalf("24h/7d TTL: search %d detail %d", searchCalls, detailCalls)
	}
	age("nano:detail:phone/oneplus-15", 8*24*time.Hour)
	if _, err := client.Device(ctx, device.Slug); err != nil {
		t.Fatal(err)
	}
	if detailCalls != 2 {
		t.Fatalf("detail expiry: %d", detailCalls)
	}
	if err := database.SetGSMArenaCache("nano:search:oneplus 15", []byte("broken JSON")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(ctx, "oneplus 15", 1); err != nil {
		t.Fatal(err)
	}
	if searchCalls != 3 {
		t.Fatal("corrupt cache was not refreshed")
	}
}

func TestValidationAndFailures(t *testing.T) {
	client := New(nil)
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) { calls++; return response(200, `[]`), nil })
	ctx := context.Background()
	for _, target := range []string{"oneplus 15", "oneplus-15", "https://evil.test/en/phone/oneplus-15", "https://nanoreview.net.evil.test/en/phone/oneplus-15", "https://nanoreview.net:443/en/phone/oneplus-15", "http://nanoreview.net/en/phone/oneplus-15", "https://user@nanoreview.net/en/phone/oneplus-15", "//evil.test/en/phone/oneplus-15", "phone/../cpu/test", "phone/%2e%2e", "phone/test?url=http://localhost", "phone/test#fragment", "https://nanoreview.net/en/phone/%74est", "phone-compare/a-vs-b"} {
		if _, err := client.Device(ctx, target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
	for _, query := range []string{"", "   ", strings.Repeat("a", 101)} {
		if _, err := client.Search(ctx, query, 1); err == nil {
			t.Errorf("accepted query %q", query)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached network")
	}
	if _, err := client.Lookup(ctx, "No such phone"); err == nil {
		t.Fatal("missing search match accepted")
	}
	if calls != 1 {
		t.Fatal("lookup fabricated a detail URL")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.Search(cancelled, "oneplus", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("context: %v", err)
	}
	if _, err := client.Device(cancelled, "phone/oneplus-15"); !errors.Is(err, context.Canceled) {
		t.Fatalf("context: %v", err)
	}
	for _, tc := range []struct {
		code int
		body string
	}{{403, "blocked"}, {200, `<title>Just a moment...</title>`}, {200, strings.Repeat("x", maxResponseBytes+1)}} {
		client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.code, tc.body), nil })
		if _, err := client.Search(ctx, "oneplus", 1); err == nil {
			t.Fatalf("accepted status/body %d, %d bytes", tc.code, len(tc.body))
		}
	}
	for _, target := range []string{"https://127.0.0.1/en/phone/test", "https://nanoreview.net/engine/go.php", "http://nanoreview.net/en/phone/test"} {
		calls = 0
		client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			r := response(http.StatusFound, "")
			r.Header.Set("Location", target)
			return r, nil
		})
		if _, err := client.Device(ctx, "phone/oneplus-15"); err == nil || calls != 1 {
			t.Fatalf("unsafe redirect %s: %v, calls %d", target, err, calls)
		}
	}
}
