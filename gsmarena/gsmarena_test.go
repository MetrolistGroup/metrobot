package gsmarena

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func TestLookupCachesIndexAndPhone(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/":
			fmt.Fprint(response, `<script src="quicksearch-123.jpg"></script>`)
		case "/quicksearch-123.jpg":
			response.Header().Set("Content-Type", "application/json")
			fmt.Fprint(response, `[{"1":"Test"},[[1,42,"Phone","Alias","phone.jpg","Phone"]]]`)
		case "/test_phone-42.php":
			fmt.Fprintf(response, `<html><head><link rel="canonical" href="%stest_phone-42.php"><meta name="Description" content="Test phone summary."></head><body>
<script>HISTORY_ITEM_ID = "42";</script><h1 data-spec="modelname">Test Phone</h1>
<div id="specs-list"><table><tr><th scope="row">Platform</th><td class="ttl">Chipset</td><td class="nfo">Test SoC</td></tr></table></div></body></html>`, serverURL(request))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	phone, err := newClient(database, server.URL, server.Client()).Lookup(context.Background(), "Test Phone")
	if err != nil {
		t.Fatal(err)
	}
	if phone.ID != 42 || phone.Name != "Test Phone" || phone.Brand != "Test" || phone.Specs[0].Items[0].Values[0] != "Test SoC" {
		t.Fatalf("unexpected phone: %+v", phone)
	}
	client := newClient(database, server.URL, server.Client())
	phone, err = client.Lookup(context.Background(), "42")
	if err != nil || phone.ID != 42 {
		t.Fatalf("cached lookup = %+v, %v", phone, err)
	}
	phone, err = client.Lookup(context.Background(), "https://www.GSMArena.com/test_phone-42.php?ref=test")
	if err != nil || phone.ID != 42 {
		t.Fatalf("mixed-case URL lookup = %+v, %v", phone, err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("HTTP requests = %d, want 3 before persistent cache hits", got)
	}
}

func TestParsePhoneSpecsGroupsDuplicateLabels(t *testing.T) {
	fixture := `<html><head><link rel="canonical" href="https://www.gsmarena.com/test_phone-42.php"></head><body>
<script>HISTORY_ITEM_ID = "42";</script><h1 data-spec="modelname">Test Phone</h1><div id="specs-list">
<table><tr><th rowspan="3" scope="row">Network</th><td class="ttl">Band</td><td class="nfo">Band one</td></tr>
<tr><td class="ttl">Band</td><td class="nfo">Band two</td></tr><tr><td class="ttl">&nbsp;</td><td class="nfo">Optional band</td></tr></table>
</div></body></html>`
	phone, err := parsePhonePage([]byte(fixture), Phone{ID: 42}, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	values := phone.Specs[0].Items[0].Values
	if len(values) != 3 || values[0] != "Band one" || values[2] != "Optional band" {
		t.Fatalf("duplicate specification values = %v", values)
	}
}

func TestLookupRejectsNonGSMArenaURL(t *testing.T) {
	client := New(nil)
	_, err := client.Lookup(context.Background(), "https://example.com/fake_phone-42.php")
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("expected invalid host error, got %v", err)
	}
}

func TestSearchSimplifiesPhoneQuestions(t *testing.T) {
	client := newClient(nil, baseURL, nil)
	client.index = &quickSearchIndex{Brands: map[int64]string{1: "Samsung", 2: "Xiaomi"}, Records: []quickSearchRecord{
		{BrandID: 1, ID: 1, Model: "Galaxy A51", Display: "Galaxy A51"},
		{BrandID: 1, ID: 2, Model: "Galaxy A51 5G", Display: "Galaxy A51 5G"},
		{BrandID: 2, ID: 3, Model: "Redmi 6A", Display: "Redmi 6A"},
	}}
	client.indexAt = time.Now()

	for query, want := range map[string]int64{
		"what are the specs of the redmi 6a?": 3,
		"specs of galaxy a51 4g?":             1,
		"specs of galaxy a51 5g?":             2,
	} {
		phones, err := client.Search(context.Background(), query, 5)
		if err != nil || len(phones) == 0 || phones[0].ID != want {
			t.Errorf("Search(%q) = %+v, %v; want ID %d", query, phones, err, want)
		}
	}
}

func TestFetchFallsBackToBundledProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer origin.Close()
	proxyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, "proxied")
	}))
	defer proxyServer.Close()

	client := newClient(nil, origin.URL, origin.Client())
	client.proxies = newProxyPool(proxyServer.URL)
	body, err := client.fetch(context.Background(), origin.URL, "text/plain")
	if err != nil || string(body) != "proxied" {
		t.Fatalf("fetch through proxy = %q, %v", body, err)
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host + "/"
}
