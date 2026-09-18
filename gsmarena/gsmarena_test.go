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
	phone, err = newClient(database, server.URL, server.Client()).Lookup(context.Background(), "42")
	if err != nil || phone.ID != 42 {
		t.Fatalf("cached lookup = %+v, %v", phone, err)
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

func serverURL(request *http.Request) string {
	return "http://" + request.Host + "/"
}
