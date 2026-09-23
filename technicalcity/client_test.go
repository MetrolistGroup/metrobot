package technicalcity

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name + ".html")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestParseDevice(t *testing.T) {
	for _, tc := range []struct{ file, slug, name, section, value string }{
		{"cpu", "cpu/Core-i9-14900K", "Core i9-14900K", "Compatibility", "FCLGA1700"},
		{"gpu", "video/GeForce-RTX-5090", "GeForce RTX 5090", "VRAM capacity and type", "GDDR7"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			target, err := detailTarget(tc.slug)
			if err != nil {
				t.Fatal(err)
			}
			device, err := parseDevice(fixture(t, tc.file), target)
			if err != nil {
				t.Fatal(err)
			}
			if device.Name != tc.name || device.Summary == "" || len(device.Specs) != 2 || device.Specs[1].Name != tc.section || device.Specs[1].Items[0].Values[0] != tc.value {
				t.Fatalf("unexpected device: %+v", device)
			}
			for _, section := range device.Specs {
				for _, item := range section.Items {
					if len(item.Values) != 1 || strings.Contains(item.Values[0], "of ") {
						t.Fatalf("comparison leaked into spec: %+v", item)
					}
				}
			}
			if tc.file == "cpu" && (!strings.HasPrefix(device.Image, baseURL+"/en/cpu_logotypes/") || !strings.Contains(device.Summary, "DDR4, DDR5")) {
				t.Fatalf("missing image/summary: %+v", device)
			}
		})
	}
	target, _ := detailTarget("cpu/Core-i9-14900K")
	for _, body := range [][]byte{fixture(t, "gpu"), fixture(t, "search"), []byte(`<title>Just a moment...</title>`), []byte(`<h1>Not found</h1>`)} {
		if _, err := parseDevice(body, target); err == nil {
			t.Fatal("accepted a challenge or nonmatching page")
		}
	}
}

func TestParseSearch(t *testing.T) {
	devices, err := parseSearch(fixture(t, "search"))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 || devices[0].Name != "Core i9 14900K" || devices[1].Name != "Core i9 14900KF" || devices[2].Slug != "gpu/GeForce-RTX-5090" {
		t.Fatalf("unexpected results: %+v", devices)
	}
	if _, err := parseSearch(fixture(t, "cpu")); err == nil {
		t.Fatal("accepted a detail page as search")
	}
	if _, err := parseSearch([]byte(`<div class="head-title"><h1>Search results:</h1></div><span><p>Nothing has been found.</p></span>`)); err != nil {
		t.Fatal(err)
	}
	if _, err := parseSearch([]byte(`<title>Just a moment...</title>`)); err == nil {
		t.Fatal("accepted challenge")
	}
}

func TestTargets(t *testing.T) {
	for _, value := range []string{"cpu/Core-i9-14900K", "/en/cpu/Core-i9-14900K", baseURL + "/en/cpu/Core-i9-14900K#characteristics", "/en/video/GeForce-RTX-5090"} {
		target, err := detailTarget(value)
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		u, _ := url.Parse(target.URL)
		if err := validateTarget(u); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{
		"", "https://evil.example/en/cpu/Core-i9-14900K", "https://technical.city.evil.example/en/cpu/Core-i9-14900K",
		"https://technical.city:443/en/cpu/Core-i9-14900K", "https://user@technical.city/en/cpu/Core-i9-14900K",
		"http://technical.city/en/cpu/Core-i9-14900K", "file:///en/cpu/Core-i9-14900K", "//evil.example/en/cpu/Core-i9-14900K",
		"/en/cpu/../search", "/en/cpu/%2e%2e", "/en/cpu/A%2fB", "/en/cpu/A?redirect=https://evil.example",
		"/en/cpu/A-vs-B", "/en/cpu/A/extra", "/ru/cpu/A", "/en/search?q=A",
	} {
		if _, err := detailTarget(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	client := New(nil)
	for _, value := range []string{"http://127.0.0.1/", "https://technical.city/admin", "https://technical.city/en/search?url=evil", "https://technical.city/cpu/A"} {
		u, _ := url.Parse(value)
		if err := client.httpClient.CheckRedirect(&http.Request{URL: u}, nil); err == nil {
			t.Errorf("accepted redirect %q", value)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(req *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func TestClientCacheAndLookup(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	client := New(database)
	calls := 0
	client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		file := "cpu"
		if req.URL.Path == "/en/search" {
			file = "search"
		}
		return response(req, 200, string(fixture(t, file))), nil
	})
	ctx := context.Background()
	for _, limit := range []int{1, 25} {
		devices, err := client.Search(ctx, "Core i9 14900K", limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != min(limit, 3) {
			t.Fatalf("incorrect cached limit: %d", len(devices))
		}
	}
	for _, query := range []string{"Core i9 14900K", "cpu/Core-i9-14900K", baseURL + "/en/cpu/Core-i9-14900K"} {
		device, err := client.Lookup(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if device.Name != "Core i9-14900K" {
			t.Fatalf("unexpected device: %+v", device)
		}
	}
	if _, err := client.Device(ctx, " Core-i9-14900K "); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("cache missed: %d HTTP calls", calls)
	}
	for key, ttl := range map[string]time.Duration{"tc:search:core i9 14900k": searchTTL, "tc:device:cpu/Core-i9-14900K": detailTTL} {
		if _, found, err := database.GetGSMArenaCache(key, ttl); err != nil || !found {
			t.Fatalf("missing cache %q: %v", key, err)
		}
	}
	if _, err := client.Device(ctx, "Missing-slug"); err == nil {
		t.Fatal("accepted wrong bare slug result")
	}
}

func TestFetchFailures(t *testing.T) {
	client := New(nil)
	for _, tc := range []struct {
		status int
		body   string
	}{{403, "blocked"}, {200, strings.Repeat("x", maxResponseBytes+1)}, {200, "<title>Just a moment...</title>"}, {200, `<div class="head-title"><h1>Search results:</h1></div><span><p>Nothing has been found.</p></span>`}} {
		client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) { return response(req, tc.status, tc.body), nil })
		if _, err := client.Search(context.Background(), "missing", 10); err == nil {
			t.Fatal("accepted failed/empty search")
		}
	}
	calls := 0
	client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		resp := response(req, 302, "")
		resp.Header.Set("Location", "https://127.0.0.1/private")
		return resp, nil
	})
	if _, err := client.Device(context.Background(), "cpu/Core-i9-14900K"); err == nil || calls != 1 {
		t.Fatalf("redirect escaped validation: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(nil).Search(ctx, "RTX 5090", 1); err == nil {
		t.Fatal("ignored canceled context")
	}
}
