package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestDiscordTimedOutMembersLoseNativePermissions(t *testing.T) {
	until := time.Now().Add(time.Hour)
	guild := &discord.RestGuild{Guild: discord.Guild{ID: 12, OwnerID: 99}, Roles: []discord.Role{{ID: 70, Permissions: discord.PermissionBanMembers | discord.PermissionManageGuild | discord.PermissionViewChannel | discord.PermissionReadMessageHistory}}}
	member := &discord.Member{User: discord.User{ID: 42}, RoleIDs: []snowflake.ID{70}, CommunicationDisabledUntil: &until}
	authority := memberAuthority(guild, member)
	if want := discord.PermissionViewChannel | discord.PermissionReadMessageHistory; authority.permissions != want {
		t.Fatalf("timed out permissions = %v, want %v", authority.permissions, want)
	}
	guild.Roles[0].Permissions |= discord.PermissionAdministrator
	if !memberAuthority(guild, member).admin() {
		t.Fatal("timeout masked Administrator")
	}
}

func TestDiscordChannelPermissions(t *testing.T) {
	const manage = discord.PermissionManageMessages
	for _, test := range []struct {
		name       string
		permission discord.Permissions
		overwrites discord.PermissionOverwrites
		thread     bool
		badChannel bool
		failLookup bool
		want       bool
	}{
		{name: "guild permission", permission: manage, want: true},
		{name: "everyone denial", permission: manage, overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 12, Deny: manage}}},
		{name: "channel grant", overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 70, Allow: manage}}, want: true},
		{name: "role grant overrides everyone denial", permission: manage, overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 12, Deny: manage}, discord.RolePermissionOverwrite{RoleID: 70, Allow: manage}}, want: true},
		{name: "role grants combine", overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 70, Deny: manage}, discord.RolePermissionOverwrite{RoleID: 72, Allow: manage}}, want: true},
		{name: "member denial overrides role grant", permission: manage, overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 70, Allow: manage}, discord.MemberPermissionOverwrite{UserID: 42, Deny: manage}}},
		{name: "member grant overrides role denial", overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 70, Deny: manage}, discord.MemberPermissionOverwrite{UserID: 42, Allow: manage}}, want: true},
		{name: "hidden channel", permission: manage, overwrites: discord.PermissionOverwrites{discord.MemberPermissionOverwrite{UserID: 42, Deny: discord.PermissionViewChannel}}},
		{name: "administrator bypass", permission: discord.PermissionAdministrator, overwrites: discord.PermissionOverwrites{discord.MemberPermissionOverwrite{UserID: 42, Deny: manage}}, want: true},
		{name: "thread inherits parent denial", permission: manage, thread: true, overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 70, Deny: manage}}},
		{name: "thread inherits parent grant", thread: true, overwrites: discord.PermissionOverwrites{discord.RolePermissionOverwrite{RoleID: 70, Allow: manage}}, want: true},
		{name: "different guild", permission: manage, badChannel: true},
		{name: "failed channel lookup", permission: manage, failLookup: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority := testModerationAuthority()
			authority.roles[0].permissions = discord.PermissionViewChannel | test.permission
			authority.members["42"] = []string{testBanRoleID, testPlainRoleID}
			session := mustTestClient(t)
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if response, ok := authority.response(r); ok {
					return response, nil
				}
				if test.failLookup {
					return nil, fmt.Errorf("channel lookup unavailable")
				}
				guildID := "12"
				if test.badChannel {
					guildID = "13"
				}
				body := fmt.Sprintf(`{"id":"5","type":0,"guild_id":%q,"permission_overwrites":%s}`, guildID, mustJSON(test.overwrites))
				if r.URL.Path == "/api/v10/channels/6" && test.thread {
					body = `{"id":"6","type":11,"guild_id":"12","parent_id":"5"}`
				} else if r.URL.Path != "/api/v10/channels/5" {
					return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			banner := &DiscordBanner{session: session, guildID: "12"}
			channelID := snowflakeID("5")
			if test.thread {
				channelID = snowflakeID("6")
			}
			for _, action := range []string{"purge", "scanreactions", "ctx-reset", "stay"} {
				if err := banner.HasChannelPermission("42", action, channelID); (err == nil) != test.want {
					t.Errorf("%s permission = %v, want allowed=%v", action, err, test.want)
				}
			}
		})
	}
}

func TestDiscordMemberLookupErrorDoesNotAuthorize(t *testing.T) {
	for _, code := range []int{10007, 10004, 0} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			authority := testModerationAuthority()
			session := mustTestClient(t)
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v10/guilds/12/members/56" {
					body := fmt.Sprintf(`{"code":%d,"message":"not found"}`, code)
					return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				}
				if response, ok := authority.response(r); ok {
					return response, nil
				}
				return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
			})})
			banner := &DiscordBanner{session: session, guildID: "12"}
			if err := banner.CanManageMember("42", "56", "ban"); (err == nil) != (code == 10007) {
				t.Fatalf("target lookup code %d: %v", code, err)
			}
		})
	}
}

func TestBulkRoleUsesCurrentPermissions(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprint(current), func(t *testing.T) {
			authority := testModerationAuthority()
			authority.roles[0].permissions = 0
			stale := discord.PermissionManageRoles
			want := "You need Manage Roles to use this command."
			if current {
				authority.roles[0].permissions = discord.PermissionManageRoles
				stale = 0
				want = "Provide at least one username, mention, or ID."
			}
			var content string
			acknowledged := false
			session := mustTestClient(t)
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if response, ok := authority.response(r); ok {
					if !acknowledged {
						t.Error("permission lookup ran before acknowledgement")
					}
					return response, nil
				}
				if r.Method == http.MethodPost && r.URL.Path == "/api/v10/interactions/17/test/callback" {
					acknowledged = true
					return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				if r.Method != http.MethodPatch || r.URL.Path != "/api/v10/webhooks/1/test/messages/@original" {
					return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var response discord.MessageCreate
				if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
					t.Error(err)
				}
				content = response.Content
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"18","channel_id":"5"}`)), Request: r}, nil
			})})
			b := &Bot{Config: testConfig(config.Config{DiscordGuildID: "12"}), Logger: zap.NewNop()}
			interaction := testCommandInteraction(t, session, fmt.Sprintf(`{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"12","channel":{"id":"5","type":0},"member":{"user":{"id":"42"},"roles":["70"],"permissions":%q},"data":{"name":"bulkrole","type":1,"options":[{"name":"role","type":8,"value":"20"},{"name":"users","type":3,"value":""}]}}`, fmt.Sprint(uint64(stale))))
			b.onInteractionCreate(session, interaction)
			if content != want {
				t.Fatalf("reply = %q, want %q", content, want)
			}
		})
	}
}
