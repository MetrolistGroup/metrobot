package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

const (
	testBotUserID    = "123456789012345678"
	testBanRoleID    = "70"
	testAdminRoleID  = "71"
	testPlainRoleID  = "72"
	testHighRoleID   = "73"
	testBotRoleID    = "79"
	testStaleRoleIDs = `["70"]`
)

func testModerationAuthority() testGuildAuthority {
	return testGuildAuthority{
		guildID: "12",
		ownerID: "99",
		roles: []testRole{
			{id: testBanRoleID, permissions: discord.PermissionBanMembers, position: 5},
			{id: testAdminRoleID, permissions: discord.PermissionAdministrator, position: 6},
			{id: testPlainRoleID, position: 1},
			{id: testHighRoleID, position: 9},
			{id: testBotRoleID, permissions: discord.PermissionBanMembers | discord.PermissionKickMembers | discord.PermissionModerateMembers | discord.PermissionManageMessages, position: 8},
		},
		members: map[string][]string{
			"42":          {testBanRoleID},
			"56":          {testPlainRoleID},
			"99":          {},
			testBotUserID: {testBotRoleID},
		},
	}
}

func TestDiscordBanAuthorization(t *testing.T) {
	for _, test := range []struct {
		name                         string
		setup                        func(*testGuildAuthority, *config.Config, *db.DB)
		caller                       string
		apiDenied, wantBan, wantDone bool
	}{
		{name: "native ban permission", wantBan: true, wantDone: true},
		{name: "native administrator", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.members["42"] = []string{testAdminRoleID} }, wantBan: true, wantDone: true},
		{name: "server owner without roles", caller: "99", wantBan: true, wantDone: true},
		{name: "ordinary member", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.members["42"] = []string{testPlainRoleID} }},
		{name: "revoked role", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.members["42"] = nil }},
		{name: "legacy bot admin record", setup: func(a *testGuildAuthority, _ *config.Config, d *db.DB) {
			a.members["42"] = nil
			if err := d.AddAdmin("discord", "42", "owner"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "legacy permanent admin config", setup: func(a *testGuildAuthority, cfg *config.Config, _ *db.DB) {
			a.members["42"] = nil
			cfg.PermaAdminDiscordIDs = []string{"42"}
		}},
		{name: "protected native admin", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.members["56"] = []string{testAdminRoleID} }},
		{name: "protected owner", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.ownerID = "56" }},
		{name: "target outranks caller", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.members["56"] = []string{testBotRoleID} }},
		{name: "target outranks bot", caller: "99", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.members["56"] = []string{testHighRoleID} }},
		{name: "same position caller role sorts above target", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.roles[2].position = 5 }, wantBan: true, wantDone: true},
		{name: "same position target role sorts above caller", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) {
			a.roles[0].id = "74"
			a.members["42"] = []string{"74"}
			a.roles[2].position = 5
		}},
		{name: "same position target role sorts above bot", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.roles[4].position = 1 }},
		{name: "custom role above everyone at position zero", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) {
			a.roles[0].position = 0
			a.members["56"] = nil
		}, wantBan: true, wantDone: true},
		{name: "legacy admin target gets no protection", setup: func(_ *testGuildAuthority, cfg *config.Config, d *db.DB) {
			cfg.PermaAdminDiscordIDs = []string{"56"}
			if err := d.AddAdmin("discord", "56", "owner"); err != nil {
				t.Fatal(err)
			}
		}, wantBan: true, wantDone: true},
		{name: "target left the server", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { delete(a.members, "56") }, wantBan: true, wantDone: true},
		{name: "guild lookup fails closed", setup: func(a *testGuildAuthority, _ *config.Config, _ *db.DB) { a.failGuild = true }},
		{name: "Discord rejects ban", apiDenied: true, wantBan: true},
	} {
		for _, route := range []string{"slash", "prefix", "cached prefix"} {
			t.Run(test.name+"/"+route, func(t *testing.T) {
				database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = database.Close() })
				cfg := testConfig(config.Config{DiscordGuildID: "12"})
				authority := testModerationAuthority()
				if test.setup != nil {
					test.setup(&authority, cfg, database)
				}
				caller := test.caller
				if caller == "" {
					caller = "42"
				}
				var bans atomic.Int32
				var acknowledged atomic.Bool
				responses := make(chan string, 4)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if route == "slash" && r.Method != http.MethodPost && r.Method != http.MethodDelete && !acknowledged.Load() {
						t.Error("permission lookup or moderation ran before interaction acknowledgement")
					}
					if response, ok := authority.response(r); ok {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(response.StatusCode)
						_, _ = io.Copy(w, response.Body)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == http.MethodPut && r.URL.Path == "/api/v10/guilds/12/bans/56":
						bans.Add(1)
						if r.Header.Get("X-Audit-Log-Reason") != "test" {
							t.Error("ban lost its audit reason")
						}
						if test.apiDenied {
							w.WriteHeader(http.StatusForbidden)
							_, _ = w.Write([]byte(`{"code":50013,"message":"Missing Permissions"}`))
							return
						}
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/callback"):
						var response struct{ Type int }
						if err := json.NewDecoder(r.Body).Decode(&response); err != nil || response.Type != 5 {
							t.Errorf("initial response = %+v, error %v; want defer", response, err)
						}
						acknowledged.Store(true)
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodDelete:
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodPost || r.Method == http.MethodPatch:
						var message discord.MessageCreate
						if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
							t.Error(err)
						}
						if test.wantDone && (message.AllowedMentions == nil || len(message.AllowedMentions.Parse) != 0 || len(message.AllowedMentions.Users) != 0) {
							t.Error("ban announcement could ping the target")
						}
						responses <- message.Content
						_, _ = w.Write([]byte(`{"id":"18","channel_id":"5"}`))
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(server.Close)
				client := mustTestClient(t)
				client.Rest = testREST(server.Client())
				client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
				b := &Bot{Client: client, Config: cfg, DB: database, Logger: zap.NewNop(), Moderation: &cmd.ModerationHandler{DB: database, CaseHandler: &cmd.CaseHandler{DB: database}}}
				if route == "slash" {
					// The interaction's member data is stale on purpose; only current REST data may authorize.
					interaction := testCommandInteraction(t, client, fmt.Sprintf(`{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"12","channel":{"id":"5","type":0},"member":{"user":{"id":%q},"roles":%s,"permissions":%q},"data":{"name":"ban","type":1,"options":[{"name":"user","type":6,"value":"56"},{"name":"reason","type":3,"value":"test"}]}}`, caller, testStaleRoleIDs, fmt.Sprint(uint64(discord.PermissionBanMembers))))
					b.onInteractionCreate(client, interaction)
				} else {
					member := &discord.Member{GuildID: 12, User: discord.User{ID: snowflakeID(caller)}, RoleIDs: []snowflake.ID{snowflakeID(testBanRoleID)}}
					if route == "cached prefix" {
						client.Caches.AddMember(*member)
						member = nil
					}
					b.onMessageCreate(client, &discord.Message{ID: 17, GuildID: snowflakePtr("12"), ChannelID: 5, Author: discord.User{ID: snowflakeID(caller)}, Member: member, Content: "!ban <@56> test"})
				}
				wantBans := int32(0)
				if test.wantBan {
					wantBans = 1
				}
				if bans.Load() != wantBans {
					t.Fatalf("ban requests=%d, want %d", bans.Load(), wantBans)
				}
				select {
				case response := <-responses:
					if strings.Contains(response, "has been permanently banned") != test.wantDone {
						t.Fatalf("unexpected response: %q", response)
					}
				default:
					t.Fatal("command did not respond")
				}
				wantCases := 0
				if test.wantDone {
					wantCases = 1
				}
				cases, err := database.GetCasesForUser("discord", "56")
				if err != nil || len(cases) != wantCases {
					t.Fatalf("cases=%+v err=%v", cases, err)
				}
				if test.wantDone && (cases[0].ActionType != "ban" || cases[0].ModeratorID != caller || cases[0].Reason != "test") {
					t.Fatalf("ban audit attribution changed: %+v", cases[0])
				}
			})
		}
	}
}

func TestDiscordActionPermissionMapping(t *testing.T) {
	want := map[string]discord.Permissions{
		"ban": discord.PermissionBanMembers, "dban": discord.PermissionBanMembers, "tban": discord.PermissionBanMembers, "sban": discord.PermissionBanMembers,
		"kick": discord.PermissionKickMembers,
		"mute": discord.PermissionModerateMembers, "timeout": discord.PermissionModerateMembers, "warn": discord.PermissionModerateMembers, "unwarn": discord.PermissionModerateMembers, "kill": discord.PermissionModerateMembers,
		"dehoist": discord.PermissionManageNicknames, "approvenick": discord.PermissionManageNicknames,
		"bulkrole": discord.PermissionManageRoles,
		"purge":    discord.PermissionManageMessages, "scanreactions": discord.PermissionManageMessages, "ctx-reset": discord.PermissionManageMessages,
		"notes": discord.PermissionManageGuild, "memory": discord.PermissionManageGuild,
	}
	for action, permission := range want {
		if discordActionPermissions[action] != permission {
			t.Errorf("%s needs %v, want %v", action, discordActionPermissions[action], permission)
		}
	}
	banner := &DiscordBanner{guildID: "12"}
	if err := banner.HasPermission("42", "addadmin"); err == nil {
		t.Fatal("unknown action was allowed")
	}
}
