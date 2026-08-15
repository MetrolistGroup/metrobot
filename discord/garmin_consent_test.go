package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestGarminMemoryConsentCustomID(t *testing.T) {
	const userID = "123456789012345678"
	for _, enabled := range []bool{true, false} {
		gotEnabled, gotUserID, ok := parseGarminMemoryConsentCustomID(garminMemoryConsentCustomID(enabled, userID))
		if !ok || gotEnabled != enabled || gotUserID != userID {
			t.Fatalf("parsed consent = (%v, %q, %v), want (%v, %q, true)", gotEnabled, gotUserID, ok, enabled, userID)
		}
	}
	if _, _, ok := parseGarminMemoryConsentCustomID("another_component"); ok {
		t.Fatal("unrelated component was accepted as Garmin consent")
	}
}

func TestGarminMemoryDisclosureExplainsUseAndOptOut(t *testing.T) {
	lower := strings.ToLower(garminMemoryDisclosure)
	for _, expected := range []string{"only to personalize", "admins can review", "not sold", "profit", "continue without", "/memory personalization"} {
		if !strings.Contains(lower, expected) {
			t.Errorf("memory disclosure missing %q", expected)
		}
	}
}

func TestRequireGarminMemoryConsentPromptsOnceAndAllowsDecline(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload struct {
			Content    string `json:"content"`
			Components []any  `json:"components"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decoding consent request: %v", err)
		}
		if !strings.Contains(payload.Content, "not sold") || len(payload.Components) != 1 {
			t.Errorf("consent payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"consent","channel_id":"channel"}`))
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{DB: database, Logger: zap.NewNop()}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", ChannelID: "channel", Author: &discordgo.User{ID: "123456789012345678"},
	}}

	if !bot.requireGarminMemoryConsent(session, message) || requests != 1 {
		t.Fatalf("first consent check prompted = %v after %d requests", requests == 1, requests)
	}
	if err := database.SetGarminMemoryConsent("discord", message.Author.ID, false); err != nil {
		t.Fatal(err)
	}
	if bot.requireGarminMemoryConsent(session, message) || requests != 1 {
		t.Fatalf("declined consent prompted again after %d requests", requests)
	}
}

func TestHandleGarminMemoryConsentPersistsChoiceAndRejectsOtherUsers(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decoding interaction response: %v", err)
		}
		payloads = append(payloads, payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{DB: database, Logger: zap.NewNop()}
	const (
		userID  = "123456789012345678"
		otherID = "987654321098765432"
	)
	interaction := func(id, clickerID, customID string) *discordgo.InteractionCreate {
		return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			ID: id, AppID: "app", Token: "token", Type: discordgo.InteractionMessageComponent,
			Member: &discordgo.Member{User: &discordgo.User{ID: clickerID}},
			Data:   discordgo.MessageComponentInteractionData{CustomID: customID},
		}}
	}

	bot.handleGarminMemoryConsent(session, interaction("enable", userID, garminMemoryConsentCustomID(true, userID)))
	consent, err := database.GetGarminMemoryConsent("discord", userID)
	if err != nil || !consent.Decided || !consent.Enabled {
		t.Fatalf("enabled consent = %#v, %v", consent, err)
	}
	if err := database.SetGarminUserMemory("discord", userID, db.GarminUserMemory{Info: "temporary"}); err != nil {
		t.Fatal(err)
	}
	bot.handleGarminMemoryConsent(session, interaction("disable", userID, garminMemoryConsentCustomID(false, userID)))
	consent, err = database.GetGarminMemoryConsent("discord", userID)
	if err != nil || !consent.Decided || consent.Enabled {
		t.Fatalf("disabled consent = %#v, %v", consent, err)
	}
	if memory, err := database.GetGarminUserMemory("discord", userID); err != nil || !memory.Empty() {
		t.Fatalf("memory after component opt-out = %#v, %v", memory, err)
	}
	bot.handleGarminMemoryConsent(session, interaction("unauthorized", userID, garminMemoryConsentCustomID(true, otherID)))
	if consent, err := database.GetGarminMemoryConsent("discord", otherID); err != nil || consent.Decided {
		t.Fatalf("unauthorized consent = %#v, %v", consent, err)
	}
	if len(payloads) != 3 {
		t.Fatalf("interaction payloads = %d, want 3", len(payloads))
	}
	for index := 0; index < 2; index++ {
		if payloads[index]["type"] != float64(discordgo.InteractionResponseUpdateMessage) {
			t.Errorf("choice payload %d type = %#v", index, payloads[index]["type"])
		}
		data, _ := payloads[index]["data"].(map[string]any)
		components, present := data["components"].([]any)
		if !present || len(components) != 0 {
			t.Errorf("choice payload %d components = %#v", index, data["components"])
		}
	}
	if payloads[2]["type"] != float64(discordgo.InteractionResponseChannelMessageWithSource) {
		t.Errorf("unauthorized payload type = %#v", payloads[2]["type"])
	}
}
