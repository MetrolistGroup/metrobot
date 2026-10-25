package technicalcity

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func TestCacheFailuresDoNotDiscardSourceData(t *testing.T) {
	for _, failure := range []string{"read", "write"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			database, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
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
			device, err := client.Lookup(context.Background(), "Core i9 14900K")
			if err != nil || device.Name == "" || device.Slug != "cpu/Core-i9-14900K" || calls != 2 {
				t.Fatalf("lookup = %+v, %v; calls=%d", device, err, calls)
			}
		})
	}
}

func TestInvalidCacheAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	client := New(database)
	calls := 0
	client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		file := "search"
		if req.URL.Path != "/en/search" {
			file = "cpu"
		}
		return response(req, 200, string(fixture(t, file))), nil
	})
	ctx := context.Background()
	for _, body := range []string{`null`, `[{}]`, `[{"slug":"cpu/Core-i9-14900K","name":"Core i9","url":"https://evil.test/"}]`} {
		if err := database.SetGSMArenaCache("tc:search:core i9 14900k", []byte(body)); err != nil {
			t.Fatal(err)
		}
		before := calls
		if devices, err := client.Search(ctx, "Core i9 14900K", 25); err != nil || len(devices) != 3 || calls != before+1 {
			t.Fatalf("cached %s: devices=%v err=%v calls=%d", body, devices, err, calls)
		}
	}
	if err := database.SetGSMArenaCache("tc:device:cpu/Core-i9-14900K", []byte(`{"slug":"cpu/Core-i9-14900K","url":"https://evil.test/","name":"Wrong","specs":[{"name":"Wrong"}]}`)); err != nil {
		t.Fatal(err)
	}
	if device, err := client.Device(ctx, "cpu/Core-i9-14900K"); err != nil || device.Name != "Core i9-14900K" || calls != 4 {
		t.Fatalf("device = %+v, %v; calls=%d", device, err, calls)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	age := func(key string, hours int) {
		t.Helper()
		if _, err := conn.Exec("UPDATE gsmarena_cache SET fetched_at = ? WHERE cache_key = ?", time.Now().Add(-time.Duration(hours)*time.Hour).Unix(), key); err != nil {
			t.Fatal(err)
		}
	}
	age("tc:search:core i9 14900k", 25)
	age("tc:device:cpu/Core-i9-14900K", 25)
	if _, err := client.Lookup(ctx, "Core i9 14900K"); err != nil || calls != 5 {
		t.Fatalf("24h/7d TTL: calls=%d err=%v", calls, err)
	}
	age("tc:device:cpu/Core-i9-14900K", 8*24)
	if _, err := client.Device(ctx, "cpu/Core-i9-14900K"); err != nil || calls != 6 {
		t.Fatalf("expired detail: calls=%d err=%v", calls, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.Search(cancelled, "Core i9 14900K", 25); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached search cancellation = %v", err)
	}
	if _, err := client.Device(cancelled, "cpu/Core-i9-14900K"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached device cancellation = %v", err)
	}
}
