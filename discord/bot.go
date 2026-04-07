package discord

import (
	"fmt"
	"sync"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

type Bot struct {
	Session    *discordgo.Session
	Config     *config.Config
	DB         *db.DB
	Logger     *zap.Logger
	Notes      *cmd.NotesHandler
	Version    *cmd.VersionHandler
	Actions    *cmd.ActionsHandler
	Moderation *cmd.ModerationHandler
	Warn       *cmd.WarnHandler
	Admin      *cmd.AdminHandler
	Ping       *cmd.PingHandler
	Case       *cmd.CaseHandler

	garminProcessor  *cmd.GarminProcessor
	TimedBanRestorer func()
	confirmations    *confirmationStore
}

func New(cfg *config.Config, database *db.DB, logger *zap.Logger,
	notes *cmd.NotesHandler, version *cmd.VersionHandler, actions *cmd.ActionsHandler,
	moderation *cmd.ModerationHandler, warn *cmd.WarnHandler, admin *cmd.AdminHandler, ping *cmd.PingHandler,
	cases *cmd.CaseHandler,
) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuildMembers | discordgo.IntentsGuildBans

	bot := &Bot{
		Session:         session,
		Config:          cfg,
		DB:              database,
		Logger:          logger.With(zap.String("platform", "discord")),
		Notes:           notes,
		Version:         version,
		Actions:         actions,
		Moderation:      moderation,
		Warn:            warn,
		Admin:           admin,
		Ping:            ping,
		Case:            cases,
		garminProcessor: cmd.NewGarminProcessor(),
	}

	// Set up Discord case logger if log channel is configured
	if cfg.DiscordLogChannel != "" {
		caseLogger := NewDiscordCaseLogger(session, cfg.DiscordLogChannel, bot.Logger)
		cases.CaseLogger = caseLogger
	}

	session.AddHandler(bot.onInteractionCreate)
	session.AddHandler(bot.onMessageCreate)
	session.AddHandler(bot.onGuildMemberAdd)
	session.AddHandler(bot.onGuildMemberUpdate)
	session.AddHandler(bot.handleReactionAdd)
	session.AddHandler(bot.handleReactionRemove)
	session.AddHandler(bot.handleMessageDelete)

	bot.confirmations = newConfirmationStore()

	return bot, nil
}

func (b *Bot) Start() error {
	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("opening discord connection: %w", err)
	}

	b.Logger.Info("discord bot connected",
		zap.String("user", b.Session.State.User.Username),
		zap.String("guild", b.Config.DiscordGuildID),
	)

	if err := b.registerCommands(); err != nil {
		return fmt.Errorf("registering slash commands: %w", err)
	}

	return nil
}

func (b *Bot) Stop() {
	b.Logger.Info("shutting down discord bot")
	b.Session.Close()
}

func (b *Bot) registerCommands() error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "help",
			Description: "Show available commands",
		},
		{
			Name:        "notes",
			Description: "List all available notes",
		},
		{
			Name:        "note",
			Description: "Show a specific note",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Note name",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "stay",
					Description: "Keep message permanently (admin only)",
					Required:    false,
				},
			},
		},
		{
			Name:        "addnote",
			Description: "Add a new note (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Note name",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "content",
					Description: "Note content",
					Required:    true,
				},
			},
		},
		{
			Name:        "editnote",
			Description: "Edit an existing note (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Note name",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "content",
					Description: "New content",
					Required:    true,
				},
			},
		},
		{
			Name:        "delnote",
			Description: "Delete a note (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Note name",
					Required:    true,
				},
			},
		},
		{
			Name:        "version",
			Description: "Show release info",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "version",
					Description: "Version tag (default: latest)",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "stay",
					Description: "Keep message permanently (admin only)",
					Required:    false,
				},
			},
		},
		{
			Name:        "latest",
			Description: "Show the latest release",
		},
		{
			Name:        "actions",
			Description: "Show GitHub Actions build status",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "stay",
					Description: "Keep message permanently (admin only)",
					Required:    false,
				},
			},
		},
		{
			Name:        "ban",
			Description: "Permanently ban a user (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to ban",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Ban reason",
					Required:    true,
				},
			},
		},
		{
			Name:        "dban",
			Description: "Ban a user and delete their messages (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to ban",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Ban reason",
					Required:    true,
				},
			},
		},
		{
			Name:        "tban",
			Description: "Temporarily ban a user (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to ban",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "duration",
					Description: "Ban duration (e.g. 1h, 2d, 1mon)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Ban reason",
					Required:    true,
				},
			},
		},
		{
			Name:        "sban",
			Description: "Softban a user (ban + unban to clear messages) (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to softban",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Softban reason",
					Required:    true,
				},
			},
		},
		{
			Name:        "mute",
			Description: "Temporarily mute a user (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to mute",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "duration",
					Description: "Mute duration (e.g. 10m, 1h, 7d)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Mute reason",
					Required:    true,
				},
			},
		},
		{
			Name:        "warn",
			Description: "Warn a user (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to warn",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Warning reason",
					Required:    true,
				},
			},
		},
		{
			Name:        "warnings",
			Description: "Show warnings for a user",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to check",
					Required:    true,
				},
			},
		},
		{
			Name:        "unwarn",
			Description: "Remove a warning from a user (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to unwarn",
					Required:    true,
				},
				{
					Type:         discordgo.ApplicationCommandOptionInteger,
					Name:         "id",
					Description:  "Warning ID (starts at 1)",
					Required:     true,
					Autocomplete: true,
				},
			},
		},
		{
			Name:        "dehoist",
			Description: "Remove hoisting characters from a user's name (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to dehoist (omit for dry run of all)",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "dry",
					Description: "Dry run - show what would change without applying",
					Required:    false,
				},
			},
		},
		{
			Name:        "addadmin",
			Description: "Add a bot admin (permaadmin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to make admin",
					Required:    true,
				},
			},
		},
		{
			Name:        "removeadmin",
			Description: "Remove a bot admin (permaadmin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to remove as admin",
					Required:    true,
				},
			},
		},
		{
			Name:        "ping",
			Description: "Check latency to various services",
		},
		{
			Name:        "purge",
			Description: "Delete messages from current message until the one being replied to (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "count",
					Description: "Number of messages to delete (alternative to replying)",
					Required:    false,
					MinValue:    func() *float64 { v := float64(1); return &v }(),
					MaxValue:    100,
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, b.Config.DiscordGuildID, cmd)
		if err != nil {
			return fmt.Errorf("registering command %q: %w", cmd.Name, err)
		}
	}

	b.Logger.Info("registered slash commands", zap.Int("count", len(commands)))
	return nil
}

type DiscordBanner struct {
	session *discordgo.Session
	guildID string
	logger  *zap.Logger
}

func (d *DiscordBanner) Platform() string { return "discord" }
func (d *DiscordBanner) ChatID() string   { return d.guildID }

func (d *DiscordBanner) Ban(userID, reason string) error {
	return d.session.GuildBanCreateWithReason(d.guildID, userID, reason, 0)
}

func (d *DiscordBanner) Unban(userID string) error {
	return d.session.GuildBanDelete(d.guildID, userID)
}

func (d *DiscordBanner) DeleteMessages(userID string) error {
	channels, err := d.session.GuildChannels(d.guildID)
	if err != nil {
		return err
	}

	// Use a wait group to process channels concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstError error

	// Process only text channels, up to a reasonable limit concurrently
	semaphore := make(chan struct{}, 5) // Limit to 5 concurrent channels

	for _, ch := range channels {
		if ch.Type != discordgo.ChannelTypeGuildText {
			continue
		}

		wg.Add(1)
		semaphore <- struct{}{} // Acquire

		go func(channelID string) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release

			if err := d.deleteUserMessagesInChannel(channelID, userID); err != nil {
				mu.Lock()
				if firstError == nil {
					firstError = err
				}
				mu.Unlock()
			}
		}(ch.ID)
	}

	wg.Wait()
	return firstError
}

func (d *DiscordBanner) deleteUserMessagesInChannel(channelID, userID string) error {
	// Calculate the message ID for 7 days ago
	// Discord snowflakes encode timestamp, so we can calculate a cutoff
	// 7 days = 7 * 24 * 60 * 60 * 1000 milliseconds = 604800000 ms
	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour)
	// Discord epoch is 1420070400000 (Jan 1, 2015)
	discordEpoch := int64(1420070400000)
	// Convert to snowflake: (timestamp - discordEpoch) << 22
	cutoffSnowflake := ((sevenDaysAgo.UnixMilli() - discordEpoch) << 22)
	cutoffID := fmt.Sprintf("%d", cutoffSnowflake)

	var beforeID string
	messageCount := 0
	maxMessages := 1000 // Limit to prevent excessive API calls

	for messageCount < maxMessages {
		msgs, err := d.session.ChannelMessages(channelID, 100, beforeID, "", "")
		if err != nil || len(msgs) == 0 {
			break
		}

		var toDelete []string
		for _, msg := range msgs {
			// Stop if we've reached messages older than 7 days
			if msg.ID < cutoffID {
				return nil
			}

			if msg.Author.ID == userID {
				toDelete = append(toDelete, msg.ID)
			}
			messageCount++
		}

		if len(toDelete) > 1 {
			// Bulk delete for messages less than 14 days old
			if err := d.session.ChannelMessagesBulkDelete(channelID, toDelete); err != nil {
				// Fall back to individual deletion if bulk fails
				for _, id := range toDelete {
					d.session.ChannelMessageDelete(channelID, id)
				}
			}
		} else if len(toDelete) == 1 {
			d.session.ChannelMessageDelete(channelID, toDelete[0])
		}

		beforeID = msgs[len(msgs)-1].ID
		if len(msgs) < 100 {
			break
		}
	}

	return nil
}

func (d *DiscordBanner) Restrict(userID string, untilDate int64) error {
	timeoutUntil := time.Unix(untilDate, 0)
	return d.session.GuildMemberTimeout(d.guildID, userID, &timeoutUntil)
}

func (d *DiscordBanner) Unrestrict(userID string) error {
	return d.session.GuildMemberTimeout(d.guildID, userID, nil)
}

func (d *DiscordBanner) SetNickname(userID, nickname string) error {
	return d.session.GuildMemberNickname(d.guildID, userID, nickname)
}

func (d *DiscordBanner) DMUser(userID, message string) error {
	return dmUser(d.session, userID, message)
}

func (d *DiscordBanner) GetDisplayName(userID string) (string, error) {
	member, err := d.session.GuildMember(d.guildID, userID)
	if err != nil {
		// If the user is not found (left the guild, etc.), treat it as
		// "no display name" instead of a hard error so moderation flows
		// like dehoist can continue gracefully.
		if restErr, ok := err.(*discordgo.RESTError); ok && restErr.Response != nil && restErr.Response.StatusCode == 404 {
			return "", nil
		}
		return "", err
	}

	// 1) Server-specific display name (nickname)
	if member.Nick != "" {
		return member.Nick, nil
	}

	// 2) Global display name
	if member.User.GlobalName != "" {
		return member.User.GlobalName, nil
	}

	// 3) Fallback: username (when no display names are set)
	return member.User.Username, nil
}

func (d *DiscordBanner) GetUsername(userID string) (string, error) {
	member, err := d.session.GuildMember(d.guildID, userID)
	if err != nil {
		if restErr, ok := err.(*discordgo.RESTError); ok && restErr.Response != nil && restErr.Response.StatusCode == 404 {
			return "", nil
		}
		return "", err
	}

	return member.User.Username, nil
}

func (d *DiscordBanner) GetAllMembers() ([]cmd.MemberInfo, error) {
	var all []cmd.MemberInfo
	var afterID string

	for {
		members, err := d.session.GuildMembers(d.guildID, afterID, 1000)
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			break
		}

		for _, m := range members {
			// Prefer server display name, then global display name, then username
			displayName := ""
			if m.Nick != "" {
				displayName = m.Nick
			} else if m.User.GlobalName != "" {
				displayName = m.User.GlobalName
			} else {
				displayName = m.User.Username
			}

			all = append(all, cmd.MemberInfo{
				UserID:      m.User.ID,
				Username:    m.User.Username,
				DisplayName: displayName,
				IsBot:       m.User.Bot,
			})
		}

		afterID = members[len(members)-1].User.ID
		if len(members) < 1000 {
			break
		}
	}

	return all, nil
}

func (b *Bot) newBanner() *DiscordBanner {
	return &DiscordBanner{
		session: b.Session,
		guildID: b.Config.DiscordGuildID,
		logger:  b.Logger,
	}
}

// NewBanner creates a new DiscordBanner for external use
func (b *Bot) NewBanner() *DiscordBanner {
	return b.newBanner()
}
