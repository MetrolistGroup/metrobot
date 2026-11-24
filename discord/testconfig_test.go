package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
)

// Test-only deployment IDs. Production has no built-in IDs, so every test bot
// that relies on a routed channel, role or log channel gets them explicitly.
const (
	garminTestManagerRoleID = "81"
	garminNyxID             = "1242567443742986373"
	garminLampID            = "650805815623680030"
	garminGeneralID         = "1398083750616891465"
	garminBotsID            = "1423657766622593104"
	garminAppSupportID      = "1400780694979870732"
	garminChatThreadID      = "1523126462306320415"
	garminCoolchannelID     = "1468369310215831552"
	garminSneakPeeksID      = "1533978905344610385"
	garminPollsID           = "1462860353204654111"
	garminMinkyID           = "1529998926445285428"

	serverLogChannelID   = "1417290212278538310"
	messageLogChannelID  = "1417289373124132864"
	reactionLogChannelID = "1547010470332989571"

	discordCoolPeopleRoleID = "1530657656526737548"
	discordModeratorRoleID  = "1495442011749220563"
	blockedGuildTagID       = "1469982893056200819"
	donorRoleID             = "1528525769570127872"
	donorWelcomeChannelID   = "1528531338196418683"
	donorWelcomeNote        = "donorwelcome"

	prohibitedFlagReaction              = "🇮🇱"
	prohibitedStarOfDavidReaction       = "✡️"
	prohibitedStarOfDavidTextReaction   = "✡"
	prohibitedFlagReactionReason        = "using prohibited :flag_il: reaction"
	prohibitedStarOfDavidReactionReason = "using prohibited :star_of_david: reaction"
)

func testDiscordSettings() config.DiscordConfig {
	return config.DiscordConfig{
		Logging: config.DiscordLoggingConfig{ServerChannelID: serverLogChannelID, MessageChannelID: messageLogChannelID, ReactionChannelID: reactionLogChannelID},
		Moderation: config.DiscordModerationConfig{BlockedGuildID: blockedGuildTagID, ProhibitedReactions: map[string]string{
			prohibitedFlagReaction:        prohibitedFlagReactionReason,
			prohibitedStarOfDavidReaction: prohibitedStarOfDavidReactionReason,
		}},
		Welcome: config.DiscordWelcomeConfig{RoleID: donorRoleID, ChannelID: donorWelcomeChannelID, Note: donorWelcomeNote},
		AI: config.DiscordAIConfig{
			GeneralChannelID: garminGeneralID,
			BotsChannelID:    garminBotsID,
			SupportChannelID: garminAppSupportID,
			ChatThreadID:     garminChatThreadID,
			ReadableChannels: map[string]string{"coolchannel": garminCoolchannelID, "sneak-peeks": garminSneakPeeksID, "polls": garminPollsID, "minky": garminMinkyID},
			ChannelDescriptions: map[string]string{
				garminCoolchannelID: "staff random posts and shitposts; regular users cannot post here",
				garminSneakPeeksID:  "staff previews of Metrolist KMP and related projects",
				garminMinkyID:       "Elissa posts pictures of a cat named Minky here",
				garminPollsID:       "staff polls users about app designs and possible features",
			},
		},
		Starboard: config.DiscordStarboardConfig{SobEmoji: "😭", IgnoredCategoryIDs: []string{"1441100425595195442", "1417289207944183939", "1471523726947254272", "1462269835756175462"}},
	}
}

// testConfig fills the test deployment settings into cfg.
func testConfig(cfg config.Config) *config.Config {
	cfg.Discord = testDiscordSettings()
	cfg.Discord.GuildID, cfg.Discord.Token = cfg.DiscordGuildID, cfg.DiscordToken
	return &cfg
}

func testBot() *Bot {
	return &Bot{Config: testConfig(config.Config{})}
}

// testGuildAuthority serves the guild and member lookups behind native
// permission checks. Members map user IDs to role IDs; absent users are 404.
type testGuildAuthority struct {
	guildID, ownerID string
	roles            []testRole
	members          map[string][]string
	failGuild        bool
}

type testRole struct {
	id          string
	permissions discord.Permissions
	position    int
}

func (a testGuildAuthority) response(r *http.Request) (*http.Response, bool) {
	if r.Method != http.MethodGet {
		return nil, false
	}
	prefix := "/api/v10/guilds/" + a.guildID
	reply := func(status int, body string) (*http.Response, bool) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, true
	}
	switch {
	case r.URL.Path == prefix:
		if a.failGuild {
			return reply(http.StatusInternalServerError, `{"code":0,"message":"unavailable"}`)
		}
		roles := []map[string]any{{"id": a.guildID, "name": "@everyone", "permissions": "0", "position": 0}}
		for _, role := range a.roles {
			roles = append(roles, map[string]any{"id": role.id, "name": "role " + role.id, "permissions": strconv.FormatUint(uint64(role.permissions), 10), "position": role.position})
		}
		body, _ := json.Marshal(map[string]any{"id": a.guildID, "name": "guild", "owner_id": a.ownerID, "roles": roles})
		return reply(http.StatusOK, string(body))
	case strings.HasPrefix(r.URL.Path, prefix+"/members/"):
		userID := strings.TrimPrefix(r.URL.Path, prefix+"/members/")
		roleIDs, ok := a.members[userID]
		if !ok {
			return reply(http.StatusNotFound, `{"code":10007,"message":"Unknown Member"}`)
		}
		body, _ := json.Marshal(map[string]any{"user": map[string]any{"id": userID, "username": "member" + userID}, "roles": append([]string{}, roleIDs...)})
		return reply(http.StatusOK, string(body))
	}
	return nil, false
}
