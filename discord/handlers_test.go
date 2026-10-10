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
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestApprovedNicknameChangePersistsAndIsConsumedOnlyByNicknameUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApproveNicknameChange("100000000000000012", "100000000000000056"); err != nil {
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
	bot := &Bot{DB: database, Config: &config.Config{DiscordGuildID: "100000000000000012"}, Logger: zap.NewNop()}

	bot.onGuildMemberUpdate(nil, &events.GuildMemberUpdate{GenericGuildMember: &events.GenericGuildMember{GuildID: snowflakeID("100000000000000012"), Member: discord.Member{GuildID: snowflakeID("100000000000000012"), Nick: ptr("same"), User: discord.User{ID: snowflakeID("100000000000000056")}}}, OldMember: discord.Member{Nick: ptr("same"), User: discord.User{ID: snowflakeID("100000000000000056")}}})
	approved, err := database.ConsumeNicknameApproval("100000000000000012", "100000000000000056")
	if err != nil || !approved {
		t.Fatalf("role-only update consumed approval: approved=%v, err=%v", approved, err)
	}

	if err := database.ApproveNicknameChange("100000000000000012", "100000000000000056"); err != nil {
		t.Fatal(err)
	}
	bot.onGuildMemberUpdate(nil, &events.GuildMemberUpdate{GenericGuildMember: &events.GenericGuildMember{GuildID: snowflakeID("100000000000000012"), Member: discord.Member{GuildID: snowflakeID("100000000000000012"), Nick: ptr("!!!alice"), User: discord.User{ID: snowflakeID("100000000000000056")}}}, OldMember: discord.Member{Nick: ptr("alice"), User: discord.User{ID: snowflakeID("100000000000000056")}}})
	approved, err = database.ConsumeNicknameApproval("100000000000000012", "100000000000000056")
	if err != nil || approved {
		t.Fatalf("nickname update did not consume approval: approved=%v, err=%v", approved, err)
	}
}

func TestBlockedGuildTagBansMemberOnJoinAndUpdate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/guilds/100000000000000012/bans/100000000000000056") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if reason := r.Header.Get("X-Audit-Log-Reason"); !strings.Contains(reason, "ROCK") || !strings.Contains(reason, blockedGuildTagID) {
			t.Errorf("ban reason = %q", reason)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	bot := &Bot{Config: &config.Config{DiscordGuildID: "100000000000000012"}, Logger: zap.NewNop()}

	var member discord.Member
	if err := json.Unmarshal([]byte(`{"guild_id":"100000000000000012","user":{"id":"100000000000000056","primary_guild":{"identity_guild_id":"1469982893056200819","identity_enabled":true,"tag":"ROCK"}}}`), &member); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		bot.onGuildTagEvent(session, &member)
	}
	member.User.PrimaryGuild.IdentityEnabled = ptr(false)
	bot.onGuildTagEvent(session, &member)

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
			Content         string                  `json:"content"`
			AllowedMentions discord.AllowedMentions `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Content != "<@100000000000000056>\nWelcome to the tester team!" {
			t.Errorf("content = %q", payload.Content)
		}
		if !slices.Equal(payload.AllowedMentions.Users, []snowflake.ID{snowflakeID("100000000000000056")}) || len(payload.AllowedMentions.Parse) != 0 {
			t.Errorf("allowed mentions = %#v", payload.AllowedMentions)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"100000000000000057","channel_id":"100000000000000005"}`))
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	bot := &Bot{
		Config: &config.Config{DiscordGuildID: "100000000000000012"},
		DB:     database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(),
	}

	bot.onGuildMemberUpdate(session, &events.GuildMemberUpdate{GenericGuildMember: &events.GenericGuildMember{GuildID: snowflakeID("100000000000000012"), Member: discord.Member{GuildID: snowflakeID("100000000000000012"), Nick: ptr("same"), User: discord.User{ID: snowflakeID("100000000000000056")}, RoleIDs: []snowflake.ID{snowflakeID(donorRoleID)}}}, OldMember: discord.Member{Nick: ptr("same"), User: discord.User{ID: snowflakeID("100000000000000056")}}})
	bot.onGuildMemberUpdate(session, &events.GuildMemberUpdate{GenericGuildMember: &events.GenericGuildMember{GuildID: snowflakeID("100000000000000012"), Member: discord.Member{GuildID: snowflakeID("100000000000000012"), Nick: ptr("same"), User: discord.User{ID: snowflakeID("100000000000000056")}, RoleIDs: []snowflake.ID{snowflakeID(donorRoleID)}}}, OldMember: discord.Member{Nick: ptr("same"), RoleIDs: []snowflake.ID{snowflakeID(donorRoleID)}, User: discord.User{ID: snowflakeID("100000000000000056")}}})

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
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/guilds/100000000000000012/members/100000000000000052"):
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
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/100000000000000005/messages/100000000000000053"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/100000000000000005/messages"):
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
			_, _ = w.Write([]byte(`{"id":"100000000000000038","channel_id":"100000000000000005"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	session.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: snowflakeID("100000000000000002")}})
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}, Roles: []discord.Role{{ID: snowflakeID("100000000000000012")}, {ID: snowflakeID("100000000000000049"), Permissions: discord.PermissionModerateMembers}, {ID: snowflakeID(discordModeratorRoleID)}}}})
	session.Caches.AddChannel(testChannel(t, map[string]any{"id": "100000000000000005", "guild_id": "100000000000000012"}))

	for _, member := range []*discord.Member{{GuildID: snowflakeID("100000000000000012"), User: discord.User{ID: snowflakeID("100000000000000049")}, RoleIDs: []snowflake.ID{snowflakeID("100000000000000049")}}, {GuildID: snowflakeID("100000000000000012"), User: discord.User{ID: snowflakeID("100000000000000024")}, RoleIDs: []snowflake.ID{snowflakeID(discordModeratorRoleID)}}, {GuildID: snowflakeID("100000000000000012"), User: discord.User{ID: snowflakeID("100000000000000007")}, RoleIDs: []snowflake.ID{snowflakeID(discordCoolPeopleRoleID)}}, {GuildID: snowflakeID("100000000000000012"), User: discord.User{ID: snowflakeID("100000000000000056")}}} {
		session.Caches.AddMember(*member)

	}

	bot := &Bot{Config: &config.Config{DiscordGuildID: "100000000000000012"}, Logger: zap.NewNop()}
	message := func(author string, reply bool, french bool) *discord.Message {
		content := "ok garmin kill"
		if french {
			content = "France Modération guillotine"
		}
		m := &discord.Message{ID: snowflakeID("100000000000000006"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: content, Author: discord.User{ID: snowflakeID(author)}}

		switch author {
		case "100000000000000024":
			m.Member = &discord.Member{RoleIDs: []snowflake.ID{snowflakeID(discordModeratorRoleID)}}
		case "100000000000000007":
			m.Member = &discord.Member{RoleIDs: []snowflake.ID{snowflakeID(discordCoolPeopleRoleID)}}
		}
		if reply {
			m.ReferencedMessage = &discord.Message{ID: snowflakeID("100000000000000053"), Author: discord.User{ID: snowflakeID("100000000000000052")}}
		}
		return m
	}

	bot.onMessageCreate(session, message("100000000000000049", true, false))
	if len(requests) != 3 {
		t.Fatalf("staff reply made %d requests, want timeout, deletion, and response: %v", len(requests), requests)
	}
	if remaining := timeouts[0]; remaining < 25*time.Second || remaining > 35*time.Second {
		t.Errorf("staff timeout duration = %s, want about 30s", remaining)
	}

	bot.onMessageCreate(session, message("100000000000000007", true, false))
	if len(requests) != 6 {
		t.Fatalf("limited user reply made %d total requests, want 6: %v", len(requests), requests)
	}
	if remaining := timeouts[1]; remaining < 4*time.Second || remaining > 6*time.Second {
		t.Errorf("limited user timeout duration = %s, want about 5s", remaining)
	}

	bot.onMessageCreate(session, message("100000000000000024", true, false))
	if len(requests) != 9 {
		t.Fatalf("moderator reply made %d total requests, want 9: %v", len(requests), requests)
	}
	if remaining := timeouts[2]; remaining < 25*time.Second || remaining > 35*time.Second {
		t.Errorf("moderator timeout duration = %s, want about 30s", remaining)
	}

	bot.onMessageCreate(session, message("100000000000000056", true, false))
	bot.onMessageCreate(session, message("100000000000000049", false, false))
	if len(requests) != 9 {
		t.Fatalf("unauthorized or non-reply command made requests: %v", requests[9:])
	}
	// tests the french variation "France Modération guillotine" with staff permissions
	// non-reply version
	bot.onMessageCreate(session, message("100000000000000049", false, true))
	if len(requests) != 9 {
		t.Fatalf("unauthorized or non-reply command made requests: %v", requests[9:])
	}
	if remaining := timeouts[0]; remaining < 25*time.Second || remaining > 35*time.Second {
		t.Errorf("staff timeout duration = %s, want about 30s", remaining)
	}
	// reply version
	bot.onMessageCreate(session, message("100000000000000049", true, true))
	if len(requests) != 12 {
		t.Fatalf("unauthorized or non-reply command made requests: %v", requests[12:])
	}
	if remaining := timeouts[0]; remaining < 25*time.Second || remaining > 35*time.Second {
		t.Errorf("staff timeout duration = %s, want about 30s", remaining)
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
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/100000000000000005/webhooks"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"100000000000000013","type":1,"channel_id":"100000000000000005","name":"` + youtubeCleanerWebhookName + `","token":"secret","user":{"id":"100000000000000002"}}]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/webhooks/100000000000000013/secret"):
			if r.URL.Query().Get("wait") != "true" {
				t.Error("webhook execution did not wait for confirmation")
			}
			var payload discord.WebhookMessageCreate
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Content != "watch https://youtu.be/video" || payload.Username != "Server Name" {
				t.Errorf("webhook payload = %#v", payload)
			}
			if !strings.Contains(payload.AvatarURL, "/guilds/100000000000000012/users/100000000000000056/avatars/server-avatar") {
				t.Errorf("avatar URL = %q", payload.AvatarURL)
			}
			if payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
				t.Errorf("allowed mentions = %#v", payload.AllowedMentions)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"100000000000000040","channel_id":"100000000000000005"}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/100000000000000005/messages/100000000000000029"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	session.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: snowflakeID("100000000000000002")}})
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}}})
	session.Caches.AddChannel(testChannel(t, map[string]any{"id": "100000000000000005", "guild_id": "100000000000000012", "type": discord.ChannelTypeGuildText}))

	bot := &Bot{Config: &config.Config{DiscordGuildID: "100000000000000012"}, Logger: zap.NewNop()}
	bot.onMessageCreate(session, &discord.Message{
		ID: snowflakeID("100000000000000029"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "watch https://youtu.be/video?si=source",
		Author: discord.User{ID: snowflakeID("100000000000000056"), Username: "account", Avatar: ptr("account-avatar")},
		Member: &discord.Member{Nick: ptr("Server Name"), Avatar: ptr("server-avatar")},
	})

	if want := []string{"GET /api/v10/channels/100000000000000005/webhooks", "POST /api/v10/webhooks/100000000000000013/secret", "DELETE /api/v10/channels/100000000000000005/messages/100000000000000029"}; !slices.Equal(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestModeratorRoleHasOnlyRequestedModerationActions(t *testing.T) {
	bot := &Bot{}
	member := &discord.Member{RoleIDs: []snowflake.ID{snowflakeID(discordModeratorRoleID)}}

	for _, action := range []string{"kill", "sban", "kick", "mute", "timeout"} {
		if !bot.canUseModerationAction(member, "100000000000000024", action) {
			t.Errorf("moderator cannot use %s", action)
		}
	}
	for _, action := range []string{"ban", "dban", "tban", "warn"} {
		if bot.canUseModerationAction(member, "100000000000000024", action) {
			t.Errorf("moderator can unexpectedly use %s", action)
		}
	}
}

func TestBulkRoleRequiresManageRolesAndLowerRole(t *testing.T) {
	roles := []discord.Role{{ID: snowflakeID("100000000000000055"), Position: 10}, {ID: snowflakeID("100000000000000020"), Position: 9}, {ID: snowflakeID("100000000000000009"), Position: 10}, {ID: snowflakeID("100000000000000021"), Position: 8, Managed: true}}
	member := &discord.ResolvedMember{Member: discord.Member{RoleIDs: []snowflake.ID{snowflakeID("100000000000000055")}}, Permissions: discord.PermissionManageRoles}

	if !canAssignRole(member, &roles[1], roles, "100000000000000012") {
		t.Fatal("lower role should be assignable")
	}
	if canAssignRole(member, &roles[2], roles, "100000000000000012") || canAssignRole(member, &roles[3], roles, "100000000000000012") {
		t.Fatal("equal or managed role should not be assignable")
	}
	member.Permissions = 0
	if canAssignRole(member, &roles[1], roles, "100000000000000012") {
		t.Fatal("member without Manage Roles could assign a role")
	}

	users := parseBulkRoleUsers("@birdy.2.0, @dubba_kench, <@234567890123456789> @BIRDY.2.0 missing")
	ids, missing := resolveBulkRoleUsers(users, []cmd.MemberInfo{{UserID: "12345678901234567", Username: "birdy.2.0"}, {UserID: "345678901234567890", Username: "dubba_kench"}})
	wantIDs := []string{"12345678901234567", "345678901234567890", "234567890123456789"}
	if !slices.Equal(ids, wantIDs) || !slices.Equal(missing, []string{"missing"}) {
		t.Fatalf("resolved ids=%v missing=%v, want ids=%v missing=[missing]", ids, missing, wantIDs)
	}
}
