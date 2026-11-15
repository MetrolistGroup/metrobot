package discord

import (
	"encoding/json"
	"fmt"
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
	"go.uber.org/zap"
)

func TestDiscordBanAuthorization(t *testing.T) {
	for _, test := range []struct {
		name                                       string
		roles                                      []string
		callerAdmin, targetAdmin, targetPermaAdmin bool
		apiDenied, wantBan, wantSuccess            bool
		permissions                                discord.Permissions
	}{
		{name: "moderator", roles: []string{discordModeratorRoleID}, wantBan: true, wantSuccess: true},
		{name: "ordinary member"},
		{name: "native permission alone", permissions: discord.PermissionBanMembers},
		{name: "limited role", roles: []string{discordCoolPeopleRoleID}},
		{name: "bot admin", callerAdmin: true, wantBan: true, wantSuccess: true},
		{name: "protected bot admin", roles: []string{discordModeratorRoleID}, targetAdmin: true},
		{name: "protected permanent admin", roles: []string{discordModeratorRoleID}, targetPermaAdmin: true},
		{name: "Discord rejects ban", roles: []string{discordModeratorRoleID}, apiDenied: true, wantBan: true},
	} {
		for _, route := range []string{"slash", "prefix", "cached prefix"} {
			t.Run(test.name+"/"+route, func(t *testing.T) {
				database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = database.Close() })
				cfg := &config.Config{DiscordGuildID: "12"}
				if test.callerAdmin {
					if err := database.AddAdmin("discord", "42", "owner"); err != nil {
						t.Fatal(err)
					}
				}
				if test.targetAdmin {
					if err := database.AddAdmin("discord", "56", "owner"); err != nil {
						t.Fatal(err)
					}
				}
				if test.targetPermaAdmin {
					cfg.PermaAdminDiscordIDs = []string{"56"}
				}
				var bans atomic.Int32
				responses := make(chan string, 4)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v10/guilds/12/members/"):
						_, _ = fmt.Fprintf(w, `{"user":{"id":%q,"username":"member"},"roles":[]}`, filepath.Base(r.URL.Path))
					case r.Method == http.MethodPost:
						var message discord.MessageCreate
						if route == "slash" {
							var response struct{ Data discord.MessageCreate }
							if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
								t.Error(err)
							}
							message = response.Data
						} else if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
							t.Error(err)
						}
						if test.wantSuccess && (message.AllowedMentions == nil || len(message.AllowedMentions.Parse) != 0 || len(message.AllowedMentions.Users) != 0) {
							t.Error("ban announcement could ping the target")
						}
						responses <- message.Content
						if route == "slash" {
							w.WriteHeader(http.StatusNoContent)
						} else {
							_, _ = w.Write([]byte(`{"id":"18","channel_id":"5"}`))
						}
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
					roles := test.roles
					if roles == nil {
						roles = []string{}
					}
					encodedRoles, _ := json.Marshal(roles)
					interaction := testCommandInteraction(t, client, fmt.Sprintf(`{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"12","channel":{"id":"5","type":0},"member":{"user":{"id":"42"},"roles":%s,"permissions":%q},"data":{"name":"ban","type":1,"options":[{"name":"user","type":6,"value":"56"},{"name":"reason","type":3,"value":"test"}]}}`, encodedRoles, fmt.Sprint(uint64(test.permissions))))
					b.onInteractionCreate(client, interaction)
				} else {
					member := &discord.Member{GuildID: 12, User: discord.User{ID: 42}}
					for _, role := range test.roles {
						member.RoleIDs = append(member.RoleIDs, snowflakeID(role))
					}
					if route == "cached prefix" {
						client.Caches.AddMember(*member)
						member = nil
					}
					b.onMessageCreate(client, &discord.Message{ID: 17, GuildID: snowflakePtr("12"), ChannelID: 5, Author: discord.User{ID: 42}, Member: member, Content: "!ban <@56> test"})
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
					if strings.Contains(response, "has been permanently banned") != test.wantSuccess {
						t.Fatalf("unexpected success response: %q", response)
					}
				default:
					t.Fatal("command did not respond")
				}
				wantCases := 0
				if test.wantSuccess {
					wantCases = 1
				}
				cases, err := database.GetCasesForUser("discord", "56")
				if err != nil || len(cases) != wantCases {
					t.Fatalf("cases=%+v err=%v", cases, err)
				}
				if test.wantSuccess && (cases[0].ActionType != "ban" || cases[0].ModeratorID != "42" || cases[0].Reason != "test") {
					t.Fatalf("ban audit attribution changed: %+v", cases[0])
				}
			})
		}
	}
}
