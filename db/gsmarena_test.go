package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGSMArenaCacheIsolationAndExpiry(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for key, body := range map[string]string{
		"phone:42": "gsmarena", "nano:detail:phone/42": "nanoreview", "tc:device:cpu/42": "technicalcity",
	} {
		if err := database.SetGSMArenaCache(key, []byte(body)); err != nil {
			t.Fatal(err)
		}
		cached, found, err := database.GetGSMArenaCache(key, time.Hour)
		if err != nil || !found || string(cached) != body {
			t.Fatalf("cache %q = %q, %v, %v", key, cached, found, err)
		}
		cached[0] = '!'
		cached, _, err = database.GetGSMArenaCache(key, time.Hour)
		if err != nil || string(cached) != body {
			t.Fatalf("cache %q shared mutable bytes: %q, %v", key, cached, err)
		}
	}
	if _, err := database.conn.Exec("UPDATE gsmarena_cache SET fetched_at = ? WHERE cache_key = 'phone:42'", time.Now().Add(-2*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if body, found, err := database.GetGSMArenaCache("phone:42", time.Hour); err != nil || found || body != nil {
		t.Fatalf("stale cache = %q, %v, %v", body, found, err)
	}
	if body, found, err := database.GetGSMArenaCache("missing", time.Hour); err != nil || found || body != nil {
		t.Fatalf("missing cache = %q, %v, %v", body, found, err)
	}
	if _, found, err := database.GetGSMArenaCache("nano:detail:phone/42", time.Hour); err != nil || !found {
		t.Fatalf("expiry crossed namespaces: %v, %v", found, err)
	}
	database.Close()
	if _, _, err := database.GetGSMArenaCache("phone:42", time.Hour); err == nil {
		t.Fatal("cache read hid a database failure")
	}
	if err := database.SetGSMArenaCache("phone:42", []byte("new")); err == nil {
		t.Fatal("cache write hid a database failure")
	}
}
