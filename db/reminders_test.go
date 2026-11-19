package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReminderRoundTrip(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	due := time.Now().Add(time.Hour).Unix()
	id, err := database.AddReminder("user", "channel", due, "pizza out")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := database.CountPendingReminders("user"); err != nil || n != 1 {
		t.Fatalf("count = %d %v", n, err)
	}
	reminders, err := database.GetPendingReminders()
	if err != nil || len(reminders) != 1 {
		t.Fatalf("pending = %d %v", len(reminders), err)
	}
	r := reminders[0]
	if r.ID != id || r.UserID != "user" || r.ChannelID != "channel" || r.DueAt != due || r.Content != "pizza out" {
		t.Fatalf("reminder = %+v", r)
	}
	if err := database.DeleteReminder(id); err != nil {
		t.Fatal(err)
	}
	if n, err := database.CountPendingReminders("user"); err != nil || n != 0 {
		t.Fatalf("count after delete = %d %v", n, err)
	}
	if reminders, err := database.GetPendingReminders(); err != nil || len(reminders) != 0 {
		t.Fatalf("pending after delete = %d %v", len(reminders), err)
	}
}
