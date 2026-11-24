package telegram

import (
	"net/http"
	"path"
	"testing"
)

func TestTelegramDBanRevokesMessagesWithNativeBan(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{10: creator, 20: member})
	client := bot.API.Client
	banCalls := 0
	bot.API.Client = telegramNoteHTTPFunc(func(r *http.Request) (*http.Response, error) {
		if path.Base(r.URL.Path) == "banChatMember" {
			banCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("revoke_messages") != "true" || r.FormValue("user_id") != "20" {
				t.Errorf("ban did not request message deletion for the target")
			}
		}
		return client.Do(r)
	})
	if _, _, err := bot.Moderation.DBan(bot.newBanner(), "10", "20", "spam"); err != nil {
		t.Fatal(err)
	}
	if banCalls != 1 || group.count("banChatMember") != 1 {
		t.Fatalf("native ban calls = %d, want 1", banCalls)
	}
}
