package nanoreview

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

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
			client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				file := "phone.html"
				if req.URL.Path == "/api/search" {
					file = "search.json"
				}
				return response(200, string(fixture(t, file))), nil
			})
			device, err := client.Lookup(context.Background(), "OnePlus 15")
			if err != nil || device.Name == "" || device.Slug != "phone/oneplus-15" || calls != 2 {
				t.Fatalf("lookup = %+v, %v; calls=%d", device, err, calls)
			}
		})
	}
}

func TestInvalidCachedMetadataIsRefetched(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	client := New(database)
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		file := "search.json"
		if req.URL.Path != "/api/search" {
			file = "phone.html"
		}
		return response(200, string(fixture(t, file))), nil
	})
	ctx := context.Background()
	for _, body := range []string{`null`, `[{}]`, `[{"slug":"phone/oneplus-15","name":"OnePlus 15","url":"https://evil.test/"}]`} {
		if err := database.SetGSMArenaCache("nano:search:oneplus 15", []byte(body)); err != nil {
			t.Fatal(err)
		}
		before := calls
		if devices, err := client.Search(ctx, "oneplus 15", 25); err != nil || len(devices) != 5 || calls != before+1 {
			t.Fatalf("cached %s: devices=%v err=%v calls=%d", body, devices, err, calls)
		}
	}
	if err := database.SetGSMArenaCache("nano:detail:phone/oneplus-15", []byte(`{"slug":"phone/other","name":"Other","specs":[{"name":"Wrong"}]}`)); err != nil {
		t.Fatal(err)
	}
	device, err := client.Device(ctx, "phone/oneplus-15")
	if err != nil || device.Slug != "phone/oneplus-15" {
		t.Fatalf("device = %+v, %v", device, err)
	}
	device.Specs[0].Items[0].Values[0] = "mutated"
	device, err = client.Device(ctx, "phone/oneplus-15")
	if err != nil || device.Specs[0].Items[0].Values[0] != "AMOLED" || calls != 4 {
		t.Fatalf("cache value was shared: %+v, %v; calls=%d", device, err, calls)
	}
}
