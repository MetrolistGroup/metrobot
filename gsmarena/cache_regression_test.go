package gsmarena

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func cacheTestServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<script src="quicksearch-123.jpg"></script>`)
		case "/quicksearch-123.jpg":
			fmt.Fprint(w, `[{"1":"Test"},[[1,42,"Phone","Alias","phone.jpg","Phone"]]]`)
		case "/test_phone-42.php":
			fmt.Fprint(w, `<h1 data-spec="modelname">Test Phone</h1><div id="specs-list"><table><tr><th>Platform</th><td class="ttl">Chipset</td><td class="nfo">Test SoC</td></tr></table></div>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func TestCacheFailuresDoNotDiscardSourceData(t *testing.T) {
	for _, failure := range []string{"read", "write"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			database, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { database.Close() })
			if failure == "read" {
				database.Close()
			} else {
				conn, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.Exec(`CREATE TRIGGER fail_cache BEFORE INSERT ON gsmarena_cache BEGIN SELECT RAISE(FAIL, 'cache unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			server, calls := cacheTestServer(t)
			client := newClient(database, server.URL, server.Client())
			phone, err := client.Lookup(context.Background(), "Test Phone")
			if err != nil || phone.ID != 42 || phone.Name != "Test Phone" || calls.Load() != 3 {
				t.Fatalf("lookup = %+v, %v; calls=%d", phone, err, calls.Load())
			}
		})
	}
}

func TestPersistedIndexKeepsOriginalExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetGSMArenaCache("index", []byte(`{"brands":{"1":"Test"},"records":[{"brand_id":1,"id":42,"model":"Phone"}]}`)); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	age := func(hours int) {
		t.Helper()
		if _, err := conn.Exec("UPDATE gsmarena_cache SET fetched_at = ? WHERE cache_key = 'index'", time.Now().Add(-time.Duration(hours)*time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	server, calls := cacheTestServer(t)
	client := newClient(database, server.URL, server.Client())
	age(23)
	if _, err := client.Search(context.Background(), "Test Phone", 1); err != nil || calls.Load() != 0 {
		t.Fatalf("fresh persisted index: calls=%d err=%v", calls.Load(), err)
	}
	age(25)
	if _, err := client.Search(context.Background(), "Test Phone", 1); err != nil || calls.Load() != 2 {
		t.Fatalf("expired persisted index: calls=%d err=%v", calls.Load(), err)
	}
}

func TestEmptyIndexIsNotCached(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<script src="quicksearch-123.jpg"></script>`)
		} else {
			fmt.Fprint(w, `[{},[]]`)
		}
	}))
	defer server.Close()
	client := newClient(database, server.URL, server.Client())
	for range 2 {
		if _, err := client.Search(context.Background(), "Test Phone", 1); err == nil {
			t.Fatal("empty index accepted as current phone metadata")
		}
	}
	if _, found, err := database.GetGSMArenaCache("index", indexCacheTTL); err != nil || found || client.index != nil || calls.Load() != 4 {
		t.Fatalf("empty index was cached: found=%v calls=%d err=%v", found, calls.Load(), err)
	}
}

func TestWrongCachedPhoneIsRefetched(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	server, calls := cacheTestServer(t)
	if err := database.SetGSMArenaCache("phone:42", []byte(`{"id":99,"name":"Wrong phone","specs":[{"name":"Wrong"}]}`)); err != nil {
		t.Fatal(err)
	}
	client := newClient(database, server.URL, server.Client())
	phone, err := client.Phone(context.Background(), "test_phone-42")
	if err != nil || phone.ID != 42 || phone.Name != "Test Phone" || calls.Load() != 1 {
		t.Fatalf("phone = %+v, %v; calls=%d", phone, err, calls.Load())
	}
	phone.Specs[0].Items[0].Values[0] = "mutated"
	phone, err = client.Phone(context.Background(), "test_phone-42")
	if err != nil || phone.Specs[0].Items[0].Values[0] != "Test SoC" || calls.Load() != 1 {
		t.Fatalf("cache value was shared: %+v, %v", phone, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Phone(ctx, "test_phone-42"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached phone cancellation = %v", err)
	}
}

func TestProxyResponseContextSurvivesUntilBodyClose(t *testing.T) {
	releaseBody := make(chan struct{})
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "prefix")
		w.(http.Flusher).Flush()
		select {
		case <-releaseBody:
			fmt.Fprint(w, "suffix")
		case <-r.Context().Done():
		}
	}))
	defer proxyServer.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://source.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := newProxyPool(proxyServer.URL).do(request, &http.Client{})
	close(releaseBody)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := response.Request.Context().Err(); err != nil {
		t.Fatalf("response context already cancelled: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "prefixsuffix" {
		t.Fatalf("streamed response = %q, %v", body, err)
	}
	response.Body.Close()
	if !errors.Is(response.Request.Context().Err(), context.Canceled) {
		t.Fatal("closing the body did not release its context")
	}
}

func TestCancelledProxyRequestDoesNotRetry(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://source.invalid/", nil)
	pool := newProxyPool(strings.Repeat(server.URL+"\n", 6))
	if _, err := pool.do(request, &http.Client{}); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancelled request: calls=%d err=%v", calls.Load(), err)
	}
}
