package discord

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"go.uber.org/zap"
)

func reminderTestBot(t *testing.T, respond func(*http.Request) (*http.Response, error)) (*Bot, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "reminders.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	c := mustTestClient(t)
	c.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(respond)})
	return &Bot{DB: d, Client: c, Logger: zap.NewNop()}, d
}

func reminderResponse(r *http.Request, status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestUndeliveredReminderIsKept(t *testing.T) {
	var requests []string
	b, d := reminderTestBot(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return reminderResponse(r, http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`)
	})
	id, err := d.AddReminder("400", "200", 1, "remember")
	if err != nil {
		t.Fatal(err)
	}
	if b.deliverReminder(id, "400", "200", "remember") {
		t.Fatal("failed delivery reported success")
	}
	rows, err := d.GetPendingReminders()
	if err != nil || len(rows) != 1 {
		t.Fatalf("undelivered reminder was deleted: rows=%d err=%v", len(rows), err)
	}
	if len(requests) < 2 {
		t.Fatalf("expected channel send and DM fallback, got %v", requests)
	}
}

func TestAmbiguousReminderSendSkipsDMAndKeepsRow(t *testing.T) {
	var requests []string
	b, d := reminderTestBot(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return nil, io.ErrUnexpectedEOF
	})
	id, _ := d.AddReminder("400", "200", 1, "remember")
	if b.deliverReminder(id, "400", "200", "remember") {
		t.Fatal("ambiguous delivery reported success")
	}
	for _, request := range requests {
		if strings.Contains(request, "/users/@me/channels") {
			t.Fatalf("ambiguous channel send also opened a DM: %v", requests)
		}
	}
	if rows, _ := d.GetPendingReminders(); len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

func TestDeliveredReminderIsDeleted(t *testing.T) {
	b, d := reminderTestBot(t, func(r *http.Request) (*http.Response, error) {
		return reminderResponse(r, http.StatusOK, `{"id":"900","channel_id":"200"}`)
	})
	id, _ := d.AddReminder("400", "200", 1, "remember")
	if !b.deliverReminder(id, "400", "200", "remember") {
		t.Fatal("delivery failed")
	}
	if rows, _ := d.GetPendingReminders(); len(rows) != 0 {
		t.Fatalf("delivered reminder kept: %d rows", len(rows))
	}
}

func TestRestoreKeepsLongOverdueUndeliveredReminder(t *testing.T) {
	attempted := make(chan struct{}, 1)
	b, d := reminderTestBot(t, func(r *http.Request) (*http.Response, error) {
		select {
		case attempted <- struct{}{}:
		default:
		}
		return reminderResponse(r, http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	b.lifecycle = ctx
	t.Cleanup(func() {
		cancel()
		b.reminderMu.Lock()
		defer b.reminderMu.Unlock()
		for _, timer := range b.reminderTimers {
			timer.Stop()
		}
	})
	if _, err := d.AddReminder("400", "200", 1, "ancient"); err != nil {
		t.Fatal(err)
	}
	if err := b.RestoreReminders(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("overdue reminder was not retried")
	}
	if rows, _ := d.GetPendingReminders(); len(rows) != 1 {
		t.Fatalf("undelivered overdue reminder was deleted: %d rows", len(rows))
	}
}
