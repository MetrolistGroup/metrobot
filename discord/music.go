package discord

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/internal/music"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func musicCommands() []discord.ApplicationCommandCreate {
	actions := []discord.ApplicationCommandOptionChoiceString{}
	for _, action := range []string{"show", "skip", "stop", "remove", "clear", "shuffle", "loop", "seek", "volume"} {
		actions = append(actions, discord.ApplicationCommandOptionChoiceString{Name: action, Value: action})
	}
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "play", Description: "Queue a YouTube video/search, or resume without a query", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "query", Description: "YouTube video URL or search (no playlists/live streams)", MaxLength: ptr(music.MaxQuery)},
		}},
		discord.SlashCommandCreate{Name: "pause", Description: "Pause voice playback (use /play to resume)"},
		discord.SlashCommandCreate{Name: "queue", Description: "Show or control the voice queue", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "action", Description: "Default: show; stop also disconnects", Choices: actions},
			discord.ApplicationCommandOptionInt{Name: "page", Description: "Pending queue page (5 tracks per page)", MinValue: ptr(1), MaxValue: ptr(20)},
			discord.ApplicationCommandOptionInt{Name: "position", Description: "Absolute pending queue position for remove", MinValue: ptr(1), MaxValue: ptr(music.MaxQueue)},
			discord.ApplicationCommandOptionInt{Name: "seconds", Description: "Required for seek: absolute seconds before the current track's end", MinValue: ptr(0), MaxValue: ptr(int(music.MaxDuration / time.Second))},
			discord.ApplicationCommandOptionInt{Name: "percent", Description: "Required for volume: 0–100 (default volume 100; 0 mutes)", MinValue: ptr(0), MaxValue: ptr(100)},
			discord.ApplicationCommandOptionString{Name: "mode", Description: "Required for loop", Choices: []discord.ApplicationCommandOptionChoiceString{{Name: "off", Value: "off"}, {Name: "track", Value: "track"}, {Name: "queue", Value: "queue"}}},
		}},
	}
}

func (b *Bot) musicChannel(s *bot.Client, userID snowflake.ID, joining bool) (snowflake.ID, error) {
	guild := snowflakeID(b.Config.DiscordGuildID)
	state, ok := s.Caches.VoiceState(guild, userID)
	if !ok || state.ChannelID == nil {
		return 0, fmt.Errorf("Join a normal server voice channel first.")
	}
	channel, ok := s.Caches.GuildVoiceChannel(*state.ChannelID)
	if !ok || channel.Type() != discord.ChannelTypeGuildVoice || channel.GuildID() != guild {
		return 0, fmt.Errorf("Music is only available in normal server voice channels.")
	}
	if joining {
		member, ok := s.Caches.Member(guild, s.ID())
		if !ok {
			return 0, fmt.Errorf("Bot permissions are not cached yet; try again shortly.")
		}
		if !s.Caches.MemberPermissionsInChannel(channel, member).Has(discord.PermissionConnect | discord.PermissionSpeak) {
			return 0, fmt.Errorf("I need Connect and Speak permissions in that voice channel.")
		}
	}
	return channel.ID(), nil
}

func (b *Bot) handleMusic(s *bot.Client, i *events.ApplicationCommandInteractionCreate, command string, opts map[string]discord.SlashCommandOption) {
	// Always acknowledge before any tooling, connection or cleanup work.
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Warn("deferring music command", zap.Error(err))
		return
	}
	respond := func(text string) {
		_, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Content: &text, AllowedMentions: &discord.AllowedMentions{}})
		if err != nil {
			b.Logger.Warn("responding to music command", zap.Error(err))
		}
	}
	if b.music == nil {
		respond("Music is unavailable in this build. It requires the music build tag, CGO and libdave; see docs/music.md.")
		return
	}
	action := getOptString(opts, "action")
	if command == "queue" && (action == "" || action == "show") {
		page := 1
		if opt, ok := opts["page"]; ok {
			page = opt.Int()
		}
		respond(formatMusicQueue(b.music.Snapshot(), page))
		return
	}
	channel, err := b.musicChannel(s, i.User().ID, command == "play" && strings.TrimSpace(getOptString(opts, "query")) != "")
	if err != nil {
		respond(err.Error())
		return
	}
	switch command {
	case "play":
		query := getOptString(opts, "query")
		if strings.TrimSpace(query) == "" {
			err = b.music.Control(uint64(channel), "resume", 0, "")
			if err == nil {
				respond("Resumed playback.")
				return
			}
		} else {
			for _, tool := range []string{"yt-dlp", "ffmpeg", "deno"} {
				if _, toolErr := exec.LookPath(tool); toolErr != nil {
					respond("Music requires yt-dlp, ffmpeg (libopus) and deno on PATH.")
					return
				}
			}
			var normalized string
			normalized, err = music.NormalizeQuery(query)
			if err == nil {
				err = b.music.Enqueue(uint64(channel), normalized)
			}
			if err == nil {
				respond("Queued: " + musicText(query) + ". Videos resolve as they reach the front; /queue shows status and any playback error.")
				return
			}
		}
	case "pause":
		err = b.music.Control(uint64(channel), "pause", 0, "")
		if err == nil {
			respond("Paused. Use /play without a query to resume.")
			return
		}
	case "queue":
		value := 0
		option := "position"
		switch action {
		case "seek":
			option = "seconds"
		case "volume":
			option = "percent"
		}
		if opt, ok := opts[option]; ok {
			value = opt.Int()
		} else if action == "seek" || action == "volume" {
			respond("Specify " + option + " for " + action + ".")
			return
		}
		err = b.music.Control(uint64(channel), action, value, getOptString(opts, "mode"))
		if err == nil {
			respond("Queue action applied: " + action + ".\n" + formatMusicQueue(b.music.Snapshot(), 1))
			return
		}
	}
	if err != nil {
		respond(err.Error())
	}
}

func musicText(text string) string {
	text = strings.TrimPrefix(text, "ytsearch1:")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("\\`*_[]<>@", r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 55 {
		text = string(runes[:55]) + "…"
	}
	return text
}

func formatMusicQueue(s music.Snapshot, page int) string {
	pages := max(1, (len(s.Pending)+4)/5)
	page = max(1, min(page, pages))
	var out strings.Builder
	fmt.Fprintf(&out, "**Voice queue** • %s • loop: %s • volume: %d%%", s.Status, s.Loop, s.Volume)
	if s.Paused {
		out.WriteString(" • paused")
	}
	if s.Channel != 0 {
		fmt.Fprintf(&out, "\nChannel: <#%d>", s.Channel)
	}
	if s.Current != nil {
		fmt.Fprintf(&out, "\nNow: %s", musicText(s.Current.Label()))
		if s.Current.Duration > 0 {
			fmt.Fprintf(&out, " (%s / %s)", s.Elapsed.Truncate(time.Second), s.Current.Duration.Round(time.Second))
		}
	} else {
		out.WriteString("\nNow: nothing")
	}
	fmt.Fprintf(&out, "\nPending: %d • page %d/%d", len(s.Pending), page, pages)
	for idx := (page - 1) * 5; idx < min(page*5, len(s.Pending)); idx++ {
		fmt.Fprintf(&out, "\n%d. %s", idx+1, musicText(s.Pending[idx].Label()))
	}
	if s.LastError != "" {
		fmt.Fprintf(&out, "\nLast playback error: %s", musicText(s.LastError))
	}
	out.WriteString("\n/queue action: skip | stop | remove position:N | clear | shuffle | loop mode:off/track/queue | seek seconds:N | volume percent:N")
	return out.String()
}

func (b *Bot) onMusicVoiceState(s *bot.Client, e *events.GuildVoiceStateUpdate) {
	if b.music == nil || e.VoiceState.GuildID.String() != b.Config.DiscordGuildID {
		return
	}
	snapshot := b.music.Snapshot()
	if snapshot.Channel == 0 {
		return
	}
	if e.VoiceState.UserID == s.ID() {
		// Event listeners may run after a newer join has already updated the cache.
		latest, ok := s.Caches.VoiceState(e.VoiceState.GuildID, s.ID())
		if ok && value(latest.ChannelID) != value(e.VoiceState.ChannelID) {
			return
		}
		if e.VoiceState.ChannelID == nil || uint64(*e.VoiceState.ChannelID) != snapshot.Channel || e.VoiceState.GuildMute {
			b.music.Disconnect(snapshot.Channel)
		}
		return
	}
	if e.OldVoiceState.ChannelID == nil || uint64(*e.OldVoiceState.ChannelID) != snapshot.Channel {
		return
	}
	for state := range s.Caches.VoiceStates(e.VoiceState.GuildID) {
		if state.UserID == s.ID() || state.ChannelID == nil || uint64(*state.ChannelID) != snapshot.Channel {
			continue
		}
		// Unknown cached members count as listeners; fail safe instead of ejecting real users.
		member, ok := s.Caches.Member(e.VoiceState.GuildID, state.UserID)
		if !ok || !member.User.Bot {
			return
		}
	}
	b.music.Disconnect(snapshot.Channel)
}
