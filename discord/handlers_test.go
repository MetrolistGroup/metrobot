package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestApprovedNicknameChangePersistsAndIsConsumedOnlyByNicknameUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApproveNicknameChange("guild", "user"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bot := &Bot{DB: database, Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}

	bot.onGuildMemberUpdate(nil, &discordgo.GuildMemberUpdate{
		Member:       &discordgo.Member{GuildID: "guild", Nick: "same", User: &discordgo.User{ID: "user"}},
		BeforeUpdate: &discordgo.Member{Nick: "same"},
	})
	approved, err := database.ConsumeNicknameApproval("guild", "user")
	if err != nil || !approved {
		t.Fatalf("role-only update consumed approval: approved=%v, err=%v", approved, err)
	}

	if err := database.ApproveNicknameChange("guild", "user"); err != nil {
		t.Fatal(err)
	}
	bot.onGuildMemberUpdate(nil, &discordgo.GuildMemberUpdate{
		Member:       &discordgo.Member{GuildID: "guild", Nick: "!!!alice", User: &discordgo.User{ID: "user"}},
		BeforeUpdate: &discordgo.Member{Nick: "alice"},
	})
	approved, err = database.ConsumeNicknameApproval("guild", "user")
	if err != nil || approved {
		t.Fatalf("nickname update did not consume approval: approved=%v, err=%v", approved, err)
	}
}

func TestBlockedGuildTagBansMemberOnJoinAndUpdate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/guilds/guild/bans/user") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if reason := r.URL.Query().Get("reason"); !strings.Contains(reason, "ROCK") || !strings.Contains(reason, blockedGuildTagID) {
			t.Errorf("ban reason = %q", reason)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}

	payload := json.RawMessage(`{"guild_id":"guild","user":{"id":"user","primary_guild":{"identity_guild_id":"1469982893056200819","identity_enabled":true,"tag":"ROCK"}}}`)
	for _, eventType := range []string{"GUILD_MEMBER_ADD", "GUILD_MEMBER_UPDATE"} {
		bot.onGuildTagEvent(session, &discordgo.Event{Type: eventType, RawData: payload})
	}
	bot.onGuildTagEvent(session, &discordgo.Event{Type: "GUILD_MEMBER_UPDATE", RawData: json.RawMessage(`{"guild_id":"guild","user":{"id":"safe","primary_guild":{"identity_guild_id":"1469982893056200819","identity_enabled":false,"tag":"ROCK"}}}`)})

	if requests != 2 {
		t.Fatalf("ban requests = %d, want 2", requests)
	}
}

func TestNewDonorGetsPingingWelcomeNote(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote(donorWelcomeNote, "Tester onboarding", "Welcome to the tester team!"); err != nil {
		t.Fatal(err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/channels/"+donorWelcomeChannelID+"/messages") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Content         string                           `json:"content"`
			AllowedMentions discordgo.MessageAllowedMentions `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Content != "<@user>\nWelcome to the tester team!" {
			t.Errorf("content = %q", payload.Content)
		}
		if !slices.Equal(payload.AllowedMentions.Users, []string{"user"}) || len(payload.AllowedMentions.Parse) != 0 {
			t.Errorf("allowed mentions = %#v", payload.AllowedMentions)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"welcome","channel_id":"channel"}`))
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{
		Config: &config.Config{DiscordGuildID: "guild"},
		DB:     database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(),
	}

	bot.onGuildMemberUpdate(session, &discordgo.GuildMemberUpdate{
		Member:       &discordgo.Member{GuildID: "guild", Nick: "same", User: &discordgo.User{ID: "user"}, Roles: []string{donorRoleID}},
		BeforeUpdate: &discordgo.Member{Nick: "same"},
	})
	bot.onGuildMemberUpdate(session, &discordgo.GuildMemberUpdate{
		Member:       &discordgo.Member{GuildID: "guild", Nick: "same", User: &discordgo.User{ID: "user"}, Roles: []string{donorRoleID}},
		BeforeUpdate: &discordgo.Member{Nick: "same", Roles: []string{donorRoleID}},
	})

	if requests != 1 {
		t.Fatalf("welcome requests = %d, want 1", requests)
	}
}

func TestGarminKillRequiresAuthorizedReplyAndUsesUserTimeout(t *testing.T) {
	var requests []string
	var timeouts []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/guilds/guild/members/target"):
			var payload struct {
				Until time.Time `json:"communication_disabled_until"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode timeout request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			timeouts = append(timeouts, time.Until(payload.Until))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/channel/messages/target-message"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/channel/messages"):
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode response request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if payload.Content != "Target eliminated." {
				t.Errorf("reply = %q", payload.Content)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"reply","channel_id":"channel"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	session.State.User = &discordgo.User{ID: "bot"}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{
		{ID: "guild"},
		{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
		{ID: discordModeratorRoleID},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.State.ChannelAdd(&discordgo.Channel{ID: "channel", GuildID: "guild"}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []*discordgo.Member{
		{GuildID: "guild", User: &discordgo.User{ID: "staff"}, Roles: []string{"staff"}},
		{GuildID: "guild", User: &discordgo.User{ID: "moderator"}, Roles: []string{discordModeratorRoleID}},
		{GuildID: "guild", User: &discordgo.User{ID: "user"}},
	} {
		if err := session.State.MemberAdd(member); err != nil {
			t.Fatal(err)
		}
	}

	bot := &Bot{Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}
	message := func(author string, reply bool) *discordgo.MessageCreate {
		m := &discordgo.Message{ID: "command", GuildID: "guild", ChannelID: "channel", Content: "ok garmin kill", Author: &discordgo.User{ID: author}}

		switch author {
		case "moderator":
			m.Member = &discordgo.Member{Roles: []string{discordModeratorRoleID}}
		case "coolpeople":
			m.Member = &discordgo.Member{Roles: []string{discordCoolPeopleRoleID}}
		}
		if reply {
			m.ReferencedMessage = &discordgo.Message{ID: "target-message", Author: &discordgo.User{ID: "target"}}
		}
		return &discordgo.MessageCreate{Message: m}
	}

	bot.onMessageCreate(session, message("staff", true))
	if len(requests) != 3 {
		t.Fatalf("staff reply made %d requests, want timeout, deletion, and response: %v", len(requests), requests)
	}
	if remaining := timeouts[0]; remaining < 25*time.Second || remaining > 35*time.Second {
		t.Errorf("staff timeout duration = %s, want about 30s", remaining)
	}

	bot.onMessageCreate(session, message("coolpeople", true))
	if len(requests) != 6 {
		t.Fatalf("limited user reply made %d total requests, want 6: %v", len(requests), requests)
	}
	if remaining := timeouts[1]; remaining < 4*time.Second || remaining > 6*time.Second {
		t.Errorf("limited user timeout duration = %s, want about 5s", remaining)
	}

	bot.onMessageCreate(session, message("moderator", true))
	if len(requests) != 9 {
		t.Fatalf("moderator reply made %d total requests, want 9: %v", len(requests), requests)
	}
	if remaining := timeouts[2]; remaining < 25*time.Second || remaining > 35*time.Second {
		t.Errorf("moderator timeout duration = %s, want about 30s", remaining)
	}

	bot.onMessageCreate(session, message("user", true))
	bot.onMessageCreate(session, message("staff", false))
	if len(requests) != 9 {
		t.Fatalf("unauthorized or non-reply command made requests: %v", requests[9:])
	}
}

func TestCleanYouTubeSourceIndicators(t *testing.T) {
	input := `one https://youtu.be/abc?si=source. two https://www.youtube.com/watch?v=def&si=source&t=12#part three https://music.youtube.com/watch?v=ghi&si=source four https://example.com/?si=keep`
	want := `one https://youtu.be/abc. two https://www.youtube.com/watch?v=def&t=12#part three https://music.youtube.com/watch?v=ghi four https://example.com/?si=keep`
	if got, changed := cleanYouTubeSourceIndicators(input); !changed || got != want {
		t.Fatalf("cleanYouTubeSourceIndicators() = %q, %v; want %q, true", got, changed, want)
	}
	if got, changed := cleanYouTubeSourceIndicators("https://youtube.example/watch?si=keep"); changed || got != "https://youtube.example/watch?si=keep" {
		t.Fatalf("non-YouTube URL changed to %q", got)
	}
}

func TestYouTubeSourceLinkIsRepostedAsUserThenDeleted(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/channel/webhooks"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"hook","type":1,"channel_id":"channel","name":"` + youtubeCleanerWebhookName + `","token":"secret","user":{"id":"bot"}}]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/webhooks/hook/secret"):
			if r.URL.Query().Get("wait") != "true" {
				t.Error("webhook execution did not wait for confirmation")
			}
			var payload discordgo.WebhookParams
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Content != "watch https://youtu.be/video" || payload.Username != "Server Name" {
				t.Errorf("webhook payload = %#v", payload)
			}
			if !strings.Contains(payload.AvatarURL, "/guilds/guild/users/user/avatars/server-avatar") {
				t.Errorf("avatar URL = %q", payload.AvatarURL)
			}
			if payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
				t.Errorf("allowed mentions = %#v", payload.AllowedMentions)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"repost","channel_id":"channel"}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/channel/messages/original"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	session.State.User = &discordgo.User{ID: "bot"}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild"}); err != nil {
		t.Fatal(err)
	}
	if err := session.State.ChannelAdd(&discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}); err != nil {
		t.Fatal(err)
	}

	bot := &Bot{Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}
	bot.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "original", GuildID: "guild", ChannelID: "channel", Content: "watch https://youtu.be/video?si=source",
		Author: &discordgo.User{ID: "user", Username: "account", Avatar: "account-avatar"},
		Member: &discordgo.Member{Nick: "Server Name", Avatar: "server-avatar"},
	}})

	if want := []string{"GET /api/v9/channels/channel/webhooks", "POST /api/v9/webhooks/hook/secret", "DELETE /api/v9/channels/channel/messages/original"}; !slices.Equal(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestModeratorRoleHasOnlyRequestedModerationActions(t *testing.T) {
	bot := &Bot{}
	member := &discordgo.Member{Roles: []string{discordModeratorRoleID}}

	for _, action := range []string{"kill", "sban", "kick", "mute", "timeout"} {
		if !bot.canUseModerationAction(member, "moderator", action) {
			t.Errorf("moderator cannot use %s", action)
		}
	}
	for _, action := range []string{"ban", "dban", "tban", "warn"} {
		if bot.canUseModerationAction(member, "moderator", action) {
			t.Errorf("moderator can unexpectedly use %s", action)
		}
	}
}

func TestBulkRoleRequiresManageRolesAndLowerRole(t *testing.T) {
	roles := []*discordgo.Role{
		{ID: "top", Position: 10},
		{ID: "lower", Position: 9},
		{ID: "equal", Position: 10},
		{ID: "managed", Position: 8, Managed: true},
	}
	member := &discordgo.Member{Roles: []string{"top"}, Permissions: discordgo.PermissionManageRoles}

	if !canAssignRole(member, roles[1], roles, "guild") {
		t.Fatal("lower role should be assignable")
	}
	if canAssignRole(member, roles[2], roles, "guild") || canAssignRole(member, roles[3], roles, "guild") {
		t.Fatal("equal or managed role should not be assignable")
	}
	member.Permissions = 0
	if canAssignRole(member, roles[1], roles, "guild") {
		t.Fatal("member without Manage Roles could assign a role")
	}

	users := parseBulkRoleUsers("@birdy.2.0, @dubba_kench, <@234567890123456789> @BIRDY.2.0 missing")
	ids, missing := resolveBulkRoleUsers(users, []cmd.MemberInfo{
		{UserID: "12345678901234567", Username: "birdy.2.0"},
		{UserID: "345678901234567890", Username: "dubba_kench"},
	})
	wantIDs := []string{"12345678901234567", "345678901234567890", "234567890123456789"}
	if !slices.Equal(ids, wantIDs) || !slices.Equal(missing, []string{"missing"}) {
		t.Fatalf("resolved ids=%v missing=%v, want ids=%v missing=[missing]", ids, missing, wantIDs)
	}
}
