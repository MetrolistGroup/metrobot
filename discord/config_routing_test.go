package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestUnconfiguredDiscordIDsMatchNothing(t *testing.T) {
	for name, b := range map[string]*Bot{"nil config": {}, "empty config": {Config: &config.Config{DiscordGuildID: "12"}}} {
		t.Run(name, func(t *testing.T) {
			for _, id := range []string{"", "0", garminAppSupportID, serverLogChannelID} {
				if b.isSupportChannel(id) || b.isGeneralChannel(id) || b.isChatThread(id) || b.isEventLogChannel(id) || b.garminChannelDescription(id) != "" {
					t.Fatalf("unconfigured bot matched channel %q", id)
				}
			}
			if channels := b.garminReadableChannels(); len(channels) != 0 {
				t.Fatalf("readable channels = %v", channels)
			}
			if _, ok := b.garminCommunityChannelTool(); ok {
				t.Fatal("community tool offered without readable channels")
			}
			if _, err := b.readGarminCommunityChannel(nil, "minky", "", 1); err == nil {
				t.Fatal("unconfigured alias was readable")
			}
			if _, prohibited := b.prohibitedReactionReason(prohibitedFlagReaction); prohibited {
				t.Fatal("unconfigured reaction was prohibited")
			}
			if got := b.garminReadableChannelForRequest([]cmd.GarminAIMessage{{Role: "user", Content: "what did elissa post in minky?"}}); got != "" {
				t.Fatalf("unconfigured routed alias = %q", got)
			}
		})
	}
}

func TestCustomDiscordRoutingStaysPerBot(t *testing.T) {
	custom := testConfig(config.Config{DiscordGuildID: "12"})
	custom.Discord.AI = config.DiscordAIConfig{
		GeneralChannelID: "201", BotsChannelID: "202", SupportChannelID: "203", ChatThreadID: "204",
		ReadableChannels:    map[string]string{"art": "205", "Dev_Log": "206", "empty": ""},
		ChannelDescriptions: map[string]string{"205": "member artwork", "201": "custom lobby"},
	}
	custom.Discord.Logging = config.DiscordLoggingConfig{MessageChannelID: "207"}
	other := testBot()
	a := &Bot{Config: custom}

	if !a.isSupportChannel("203") || a.isSupportChannel(garminAppSupportID) || other.isSupportChannel("203") || !other.isSupportChannel(garminAppSupportID) {
		t.Fatal("support channel leaked between bots")
	}
	if a.garminChannelDescription("201") != "custom lobby" || !strings.Contains(a.garminChannelDescription("202"), "preferred channel") || a.garminChannelDescription(garminMinkyID) != "" {
		t.Fatal("custom channel descriptions were not applied per bot")
	}
	if !a.isEventLogChannel("207") || a.isEventLogChannel(serverLogChannelID) || a.isEventLogChannel("") {
		t.Fatal("custom log channels were not applied")
	}
	if got := a.garminReadableChannels(); len(got) != 2 || got["art"] != "205" || got["dev-log"] != "206" {
		t.Fatalf("readable channels = %v", got)
	}
	tool, ok := a.garminCommunityChannelTool()
	if !ok {
		t.Fatal("configured community tool missing")
	}
	var schema struct {
		Properties struct {
			Channel struct{ Enum []string } `json:"channel"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(schema.Properties.Channel.Enum) != "[art dev-log]" || !strings.Contains(tool.Function.Description, "art (member artwork)") || strings.Contains(tool.Function.Description, "minky") {
		t.Fatalf("community tool = %s %v", tool.Function.Description, schema.Properties.Channel.Enum)
	}

	session := mustTestClient(t)
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: 12}}})
	for _, channel := range []map[string]any{
		{"id": "201", "guild_id": "12", "type": discord.ChannelTypeGuildText},
		{"id": "204", "guild_id": "12", "parent_id": "201", "type": discord.ChannelTypeGuildPublicThread},
		{"id": "208", "guild_id": "12", "parent_id": "201", "type": discord.ChannelTypeGuildPublicThread},
	} {
		session.Caches.AddChannel(testChannel(t, channel))
	}
	if a.garminRedirectChannelID(session, "208") != "201" || a.garminAIAmbientEnabled(session, "208") {
		t.Fatal("thread under the custom general channel did not inherit its restrictions")
	}
	if a.garminRedirectChannelID(session, "204") != "204" || !a.garminAIAmbientEnabled(session, "204") {
		t.Fatal("custom chat thread inherited general restrictions")
	}
	if other.garminRedirectChannelID(session, "208") != "208" || !other.garminAIAmbientEnabled(session, "208") {
		t.Fatal("another bot used the custom general channel")
	}
}

func TestOptionalWelcomeAndBlockedTagDisabledWithoutConfig(t *testing.T) {
	session := mustTestClient(t)
	requests := 0
	session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})})
	cfg := testConfig(config.Config{DiscordGuildID: "12"})
	cfg.Discord.Welcome, cfg.Discord.Moderation.BlockedGuildID = config.DiscordWelcomeConfig{}, ""
	b := &Bot{Config: cfg, Logger: zap.NewNop()}

	var member discord.Member
	if err := json.Unmarshal([]byte(`{"guild_id":"12","user":{"id":"56","primary_guild":{"identity_guild_id":"`+blockedGuildTagID+`","identity_enabled":true}}}`), &member); err != nil {
		t.Fatal(err)
	}
	b.onGuildTagEvent(session, &member)
	member.User.PrimaryGuild = &discord.PrimaryGuild{IdentityEnabled: ptr(true)}
	b.onGuildTagEvent(session, &member)
	b.onGuildMemberUpdate(session, &events.GuildMemberUpdate{
		GenericGuildMember: &events.GenericGuildMember{GuildID: 12, Member: discord.Member{GuildID: 12, Nick: ptr("same"), User: discord.User{ID: 56}, RoleIDs: []snowflake.ID{snowflakeID(donorRoleID)}}},
		OldMember:          discord.Member{Nick: ptr("same"), User: discord.User{ID: 56}},
	})
	if requests != 0 {
		t.Fatalf("disabled features made %d requests", requests)
	}
}

func TestDiscordBannerNativeAuthority(t *testing.T) {
	authority := testModerationAuthority()
	authority.members["57"] = []string{testAdminRoleID}
	session := mustTestClient(t)
	session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if response, ok := authority.response(r); ok {
			return response, nil
		}
		return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})})
	banner := (&Bot{Config: testConfig(config.Config{DiscordGuildID: "12"})}).bannerFor(session)

	for userID, want := range map[string]bool{"99": true, "57": true, "42": false, "56": false, "404": false} {
		if got, err := banner.IsAdministrator(userID); err != nil || got != want {
			t.Errorf("IsAdministrator(%s) = %v, %v; want %v", userID, got, err, want)
		}
	}
	// Ban Members alone does not cover other actions, and an empty target checks only the caller.
	if err := banner.CanManageMember("42", "", "dehoist"); err == nil {
		t.Fatal("Ban Members granted Manage Nicknames")
	}
	if err := banner.CanManageMember("57", "", "dehoist"); err != nil {
		t.Fatalf("administrator dehoist = %v", err)
	}
	for _, test := range []struct{ caller, target string }{{"42", "42"}, {"99", testBotUserID}, {"42", "57"}, {"57", "99"}} {
		if err := banner.CanManageMember(test.caller, test.target, "kick"); permissionDenial(err) == "" || err == nil {
			t.Errorf("CanManageMember(%s, %s) = %v", test.caller, test.target, err)
		}
	}
	// The bot as caller can warn ordinary members but never native admins.
	if err := banner.CanManageMember(testBotUserID, "56", "warn"); err != nil {
		t.Fatalf("automatic warn = %v", err)
	}
	if err := banner.CanManageMember(testBotUserID, "57", "warn"); err == nil {
		t.Fatal("automatic warn reached an administrator")
	}
	authority.failGuild = true
	if _, err := banner.IsAdministrator("99"); err == nil {
		t.Fatal("failed lookup returned no error")
	}
	if err := banner.HasPermission("99", "ban"); err == nil || permissionDenial(err) != "Couldn't confirm your Discord permissions. Try again later." {
		t.Fatalf("failed lookup = %v", err)
	}
}
