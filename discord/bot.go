package discord

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/firecrawl"
	gh "github.com/MetrolistGroup/metrobot/github"
	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/MetrolistGroup/metrobot/nanoreview"
	"github.com/MetrolistGroup/metrobot/technicalcity"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

type Bot struct {
	Client     *bot.Client
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

	garminProcessor      *cmd.GarminProcessor
	garminAI             cmd.GarminAI
	garminMemory         *cmd.GarminMemory
	garminGitHub         *gh.AssistantClient
	garminFirecrawl      *firecrawl.Client
	gsmarena             *gsmarena.Client
	nanoreview           *nanoreview.Client
	technicalCity        *technicalcity.Client
	garminAIMu           sync.Mutex
	garminAILastUsed     map[string]time.Time
	garminAIContexts     map[string]garminAIContext
	garminAIUserContexts map[string]garminAIContext
	garminContextCutoffs map[string]string
	garminAIRequests     map[string]map[string]context.CancelFunc
	garminAIParents      map[string]map[string]garminAIContext
	garminAIAmbientBusy  map[string]uint64
	garminAIAmbientSeq   uint64
	garminAISlots        chan struct{}
	messageLogMu         sync.Mutex
	messageLogCache      map[string][]cachedLogMessage
	youtubeWebhookMu     sync.Mutex
	TimedBanRestorer     func()
	ready                chan struct{}
	readyOnce            sync.Once
	stopOnce             sync.Once
	lifecycle            context.Context
	cancelLifecycle      context.CancelFunc

	garminDecisions       cmd.Decisions
	supportMu             sync.Mutex
	supportNotesMu        sync.Mutex
	supportSlots          chan struct{}
	supportSeen           map[string]supportRequestState
	garminDeletedMessages map[string]time.Time
}

func New(cfg *config.Config, database *db.DB, logger *zap.Logger,
	notes *cmd.NotesHandler, version *cmd.VersionHandler, actions *cmd.ActionsHandler,
	moderation *cmd.ModerationHandler, warn *cmd.WarnHandler, admin *cmd.AdminHandler, ping *cmd.PingHandler,
	cases *cmd.CaseHandler,
) (*Bot, error) {
	if snowflakeID(cfg.DiscordGuildID) == 0 {
		return nil, fmt.Errorf("invalid Discord guild ID")
	}
	lifecycle, cancelLifecycle := context.WithCancel(context.Background())
	b := &Bot{
		Config:               cfg,
		DB:                   database,
		Logger:               logger.With(zap.String("platform", "discord")),
		Notes:                notes,
		Version:              version,
		Actions:              actions,
		Moderation:           moderation,
		Warn:                 warn,
		Admin:                admin,
		Ping:                 ping,
		Case:                 cases,
		gsmarena:             gsmarena.New(database),
		nanoreview:           nanoreview.New(database),
		technicalCity:        technicalcity.New(database),
		garminProcessor:      cmd.NewGarminProcessor(),
		garminAILastUsed:     make(map[string]time.Time),
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
		garminContextCutoffs: make(map[string]string),
		garminAIRequests:     make(map[string]map[string]context.CancelFunc),
		garminAIAmbientBusy:  make(map[string]uint64),
		messageLogCache:      make(map[string][]cachedLogMessage),
		ready:                make(chan struct{}),
		lifecycle:            lifecycle,
		cancelLifecycle:      cancelLifecycle,
	}
	session, err := disgo.New(cfg.DiscordToken,
		bot.WithEventManagerConfigOpts(bot.WithGatewayHandlers(b.gatewayHandlers())),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentGuilds|gateway.IntentGuildMessages|gateway.IntentGuildMembers|gateway.IntentGuildModeration|gateway.IntentGuildMessageReactions|gateway.IntentMessageContent),
			gateway.WithEnableRawEvents(true),
		),
		// Message content is kept only in the bounded, filtered audit cache.
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds|cache.FlagChannels|cache.FlagRoles|cache.FlagMembers|cache.FlagEmojis)),
		bot.WithMemberChunkingFilter(bot.MemberChunkingFilterAll),
	)
	if err != nil {
		cancelLifecycle()
		return nil, fmt.Errorf("creating Discord client: %w", err)
	}
	b.Client = session
	if len(cfg.OpenRouterAPIKeys) > 0 {
		b.garminAI = cmd.NewOpenRouterClient(cfg.OpenRouterAPIKeys, cfg.OpenRouterModel)
		if cfg.OpenRouterDecisionModel != "" {
			b.garminDecisions = cmd.NewDecisionsClient(cfg.OpenRouterAPIKeys, cfg.OpenRouterDecisionModel)
			b.supportSlots = make(chan struct{}, 2)
		}
	}
	if b.garminAI != nil {
		memoryPath := cfg.GarminMemoryFile
		if memoryPath == "" {
			memoryPath = cmd.GarminMemoryFile
		}
		memory, err := cmd.NewGarminMemory(memoryPath)
		if err != nil && cfg.GarminMemoryFile == "" {
			memory, err = cmd.NewGarminMemory("/data/" + cmd.GarminMemoryFile)
		}
		if err != nil {
			cancelLifecycle()
			session.Close(context.Background())
			return nil, fmt.Errorf("initializing Metrobot memory: %w", err)
		}
		b.garminMemory = memory
		b.garminGitHub = gh.NewAssistantClient(cfg.GitHubToken, cfg.GitHubOwner, cfg.GitHubRepo)
		if len(cfg.FirecrawlAPIKeys) > 0 {
			b.garminFirecrawl = firecrawl.NewClient(cfg.FirecrawlAPIKeys)
		}
		b.garminAISlots = make(chan struct{}, 3)
	}

	// Set up Discord case logger if log channel is configured
	if cfg.DiscordLogChannel != "" {
		caseLogger := NewDiscordCaseLogger(session, cfg.DiscordLogChannel, b.Logger)
		cases.CaseLogger = caseLogger
	}

	b.installListeners()

	return b, nil
}

func (b *Bot) Start() error {
	ctx, cancel := context.WithTimeout(b.lifecycle, 30*time.Second)
	defer cancel()
	if err := b.Client.OpenGateway(ctx); err != nil {
		b.Stop()
		return fmt.Errorf("opening Discord connection: %w", err)
	}
	select {
	case <-b.ready:
	case <-ctx.Done():
		b.Stop()
		return fmt.Errorf("waiting for Discord READY: %w", ctx.Err())
	}
	user, _ := b.Client.Caches.SelfUser()
	b.Logger.Info("discord bot connected", zap.String("user", user.Username), zap.String("guild", b.Config.DiscordGuildID))
	if err := b.registerCommands(); err != nil {
		b.Stop()
		return fmt.Errorf("registering slash commands: %w", err)
	}
	return nil
}

func (b *Bot) Stop() {
	b.stopOnce.Do(func() {
		b.Logger.Info("shutting down discord bot")
		b.cancelLifecycle()
		b.garminAIMu.Lock()
		for _, requests := range b.garminAIRequests {
			for _, cancel := range requests {
				cancel()
			}
		}
		b.garminAIMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		b.Client.Close(ctx)
	})
}

func (b *Bot) registerCommands() error {
	manageRolesPermission := discord.PermissionManageRoles
	commands := []discord.ApplicationCommandCreate{discord.SlashCommandCreate{
		Name:        "help",
		Description: "Show available commands",
	}, discord.SlashCommandCreate{
		Name:        "gsm",
		Description: "Look up phone specifications on GSMArena",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:         "search",
			Description:  "Phone name",
			Required:     true,
			Autocomplete: true,
			MaxLength:    ptr(100),
		}},
	}, discord.SlashCommandCreate{
		Name:        "nanoreview",
		Description: "Look up phones, CPUs, GPUs, laptops and SoCs on NanoReview",
		Options:     []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{Name: "search", Description: "Product name", Required: true, Autocomplete: true, MaxLength: ptr(100)}},
	}, discord.SlashCommandCreate{
		Name:        "technicalcity",
		Description: "Look up CPU and GPU specs on Technical City",
		Options:     []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{Name: "search", Description: "CPU or GPU name", Required: true, Autocomplete: true, MaxLength: ptr(100)}},
	}, discord.SlashCommandCreate{
		Name:        "gsmcompare",
		Description: "Compare two phones on GSMArena",
		Options:     compareOptions("Phone name"),
	}, discord.SlashCommandCreate{
		Name:        "nanoreviewcompare",
		Description: "Compare two phones, CPUs, GPUs or SoCs on NanoReview",
		Options:     compareOptions("Product name"),
	}, discord.SlashCommandCreate{
		Name:        "technicalcitycompare",
		Description: "Compare two CPUs or GPUs on Technical City",
		Options:     compareOptions("CPU or GPU name"),
	}, discord.SlashCommandCreate{
		Name:        "ctx-reset",
		Description: "Forget Garmin context before this point in this channel (admin only)",
	}, discord.SlashCommandCreate{
		Name:        "quote",
		Description: "Turn the previous message into a quote image",
	}, discord.MessageCommandCreate{
		Name: "Clip",
	}, discord.SlashCommandCreate{
		Name:        "notes",
		Description: "List all available notes",
	}, discord.SlashCommandCreate{
		Name:        "note",
		Description: "Show a note, or add one with a description and content",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:        "name",
			Description: "Note name without spaces",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "short_desc",
			Description: "Short description (required when adding)",
			Required:    false,
			MaxLength:   ptr(160),
		}, discord.ApplicationCommandOptionString{
			Name:        "content",
			Description: "Multi-line content (required when adding)",
			Required:    false,
		}, discord.ApplicationCommandOptionBool{
			Name:        "stay",
			Description: "Keep message permanently (admin only)",
			Required:    false,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "addnote",
		Description: "Add a new note (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:        "name",
			Description: "Note name",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "short_desc",
			Description: "Short description",
			Required:    true,
			MaxLength:   ptr(160),
		}, discord.ApplicationCommandOptionString{
			Name:        "content",
			Description: "Multi-line note content",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "editnote",
		Description: "Edit an existing note (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:        "name",
			Description: "Note name",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "short_desc",
			Description: "New short description",
			Required:    true,
			MaxLength:   ptr(160),
		}, discord.ApplicationCommandOptionString{
			Name:        "content",
			Description: "New multi-line content",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "renamenote",
		Description: "Rename an existing note (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:        "name",
			Description: "Current note name",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "new_name",
			Description: "New note name without spaces",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "delnote",
		Description: "Delete a note (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:        "name",
			Description: "Note name",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "version",
		Description: "Show release info",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
			Name:        "version",
			Description: "Version tag (default: latest)",
			Required:    false,
		}, discord.ApplicationCommandOptionBool{
			Name:        "stay",
			Description: "Keep message permanently (admin only)",
			Required:    false,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "latest",
		Description: "Show the latest release",
	}, discord.SlashCommandCreate{
		Name:        "actions",
		Description: "Show GitHub Actions build status",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionBool{
			Name:        "stay",
			Description: "Keep message permanently (admin only)",
			Required:    false,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "ban",
		Description: "Permanently ban a user (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to ban",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Ban reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "dban",
		Description: "Ban a user and delete their messages (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to ban",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Ban reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "tban",
		Description: "Temporarily ban a user (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to ban",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "duration",
			Description: "Ban duration (e.g. 1h, 2d, 1mon)",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Ban reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "sban",
		Description: "Softban a user (admin/moderator)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to softban",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Softban reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "kick",
		Description: "Kick a user (admin/moderator)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to kick",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Kick reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "mute",
		Description: "Temporarily mute a user (admin/moderator)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to mute",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "duration",
			Description: "Mute duration (e.g. 10m, 1h, 7d)",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Mute reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "timeout",
		Description: "Timeout a user (admin/moderator)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to timeout",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "duration",
			Description: "Timeout duration (e.g. 10m, 1h, 7d)",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Timeout reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "warn",
		Description: "Warn a user (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to warn",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "reason",
			Description: "Warning reason",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "warnings",
		Description: "Show warnings for a user",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to check",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "unwarn",
		Description: "Remove a warning from a user (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to unwarn",
			Required:    true,
		}, discord.ApplicationCommandOptionInt{
			Name:         "id",
			Description:  "Warning ID (starts at 1)",
			Required:     true,
			Autocomplete: true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "dehoist",
		Description: "Dehoist a user or rerun dehoisting across the server (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to dehoist (omit to rerun all untouched server names)",
			Required:    false,
		}, discord.ApplicationCommandOptionBool{
			Name:        "dry",
			Description: "Preview changes without applying them",
			Required:    false,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "approvenick",
		Description: "Allow a user's next nickname change without dehoisting (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User whose next nickname change should be allowed",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:                     "bulkrole",
		Description:              "Add one role to multiple users",
		DefaultMemberPermissions: omit.New(&manageRolesPermission),
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionRole{
			Name:        "role",
			Description: "Role to add",
			Required:    true,
		}, discord.ApplicationCommandOptionString{
			Name:        "users",
			Description: "Space- or comma-separated usernames, mentions, or IDs",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "addadmin",
		Description: "Add a bot admin (permaadmin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to make admin",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "removeadmin",
		Description: "Remove a bot admin (permaadmin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionUser{
			Name:        "user",
			Description: "User to remove as admin",
			Required:    true,
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "memory",
		Description: "Manage Metrobot AI memory",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionSubCommand{
			Name:        "view",
			Description: "View Metrobot's memory",
		}, discord.ApplicationCommandOptionSubCommand{
			Name:        "append",
			Description: "Append Markdown to Metrobot's memory",
			Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
				Name:        "content",
				Description: "Markdown to remember",
				Required:    true,
			},
			},
		}, discord.ApplicationCommandOptionSubCommand{
			Name:        "replace",
			Description: "Replace Metrobot's memory",
			Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
				Name:        "content",
				Description: "Complete replacement Markdown",
				Required:    true,
			},
			},
		}, discord.ApplicationCommandOptionSubCommand{
			Name:        "clear",
			Description: "Clear Metrobot's memory",
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "ping",
		Description: "Check latency to various services",
	}, discord.SlashCommandCreate{
		Name:        "purge",
		Description: "Delete recent messages from the current channel (admin only)",
		Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionInt{
			Name:        "count",
			Description: "Number of messages to delete (alternative to replying)",
			Required:    false,
			MinValue:    ptr(1),
			MaxValue:    ptr(100),
		},
		},
	}, discord.SlashCommandCreate{
		Name:        "scanreactions",
		Description: "Scan the last 100 channel messages for prohibited reactions (admin only)",
	}, discord.SlashCommandCreate{
		Name:        "refreshstarboard",
		Description: "Refresh all starboard entries by rechecking star counts (admin only)",
	},
	}

	if _, err := b.Client.Rest.SetGuildCommands(b.Client.ApplicationID, snowflakeID(b.Config.DiscordGuildID), commands); err != nil {
		return fmt.Errorf("registering slash commands: %w", err)
	}

	b.Logger.Info("registered slash commands", zap.Int("count", len(commands)))
	return nil
}

type DiscordBanner struct {
	session *bot.Client
	guildID string
	logger  *zap.Logger
}

func (d *DiscordBanner) Platform() string { return "discord" }
func (d *DiscordBanner) ChatID() string   { return d.guildID }

func (d *DiscordBanner) Ban(userID, reason string) error {
	return d.session.Rest.AddBan(snowflakeID(d.guildID), snowflakeID(userID), 0, rest.WithReason(reason))
}

func (d *DiscordBanner) BanAndDeleteMessages(userID, reason string) error {
	return d.session.Rest.AddBan(snowflakeID(d.guildID), snowflakeID(userID), 7*24*time.Hour, rest.WithReason(reason))
}

func (d *DiscordBanner) Unban(userID string) error {
	return d.session.Rest.DeleteBan(snowflakeID(d.guildID), snowflakeID(userID))
}

func (d *DiscordBanner) Kick(userID, reason string) error {
	return d.session.Rest.RemoveMember(snowflakeID(d.guildID), snowflakeID(userID), rest.WithReason(reason))
}

func (d *DiscordBanner) DeleteMessages(userID string) error {
	channels, err := d.session.Rest.GetGuildChannels(snowflakeID(d.guildID))
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
		if ch.Type() != discord.ChannelTypeGuildText {
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
		}(ch.ID().String())
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
		msgs, err := d.session.Rest.GetMessages(snowflakeID(channelID), 0, snowflakeID(beforeID), 0, 100)
		if err != nil || len(msgs) == 0 {
			break
		}

		var toDelete []snowflake.ID
		for _, msg := range msgs {
			// Stop if we've reached messages older than 7 days
			if msg.ID.String() < cutoffID {
				return nil
			}

			if msg.Author.ID.String() == userID {
				toDelete = append(toDelete, msg.ID)
			}
			messageCount++
		}

		if len(toDelete) > 1 {
			// Bulk delete for messages less than 14 days old
			if err := d.session.Rest.BulkDeleteMessages(snowflakeID(channelID), toDelete); err != nil {
				// Fall back to individual deletion if bulk fails
				for _, id := range toDelete {
					d.session.Rest.DeleteMessage(snowflakeID(channelID), id)
				}
			}
		} else if len(toDelete) == 1 {
			d.session.Rest.DeleteMessage(snowflakeID(channelID), toDelete[0])
		}

		beforeID = msgs[len(msgs)-1].ID.String()
		if len(msgs) < 100 {
			break
		}
	}

	return nil
}

func (d *DiscordBanner) Restrict(userID string, untilDate int64) error {
	timeoutUntil := time.Unix(untilDate, 0)
	return updateTimeout(d.session, snowflakeID(d.guildID), snowflakeID(userID), &timeoutUntil)
}

func (d *DiscordBanner) Unrestrict(userID string) error {
	return updateTimeout(d.session, snowflakeID(d.guildID), snowflakeID(userID), nil)
}

func (d *DiscordBanner) SetNickname(userID, nickname string) error {
	return updateNickname(d.session, snowflakeID(d.guildID), snowflakeID(userID), nickname)
}

func (d *DiscordBanner) DMUser(userID, message string) error {
	return dmUser(d.session, userID, message)
}

func (d *DiscordBanner) GetDisplayName(userID string) (string, error) {
	member, err := d.session.Rest.GetMember(snowflakeID(d.guildID), snowflakeID(userID))
	if err != nil {
		// If the user is not found (left the guild, etc.), treat it as
		// "no display name" instead of a hard error so moderation flows
		// like dehoist can continue gracefully.
		if restErr, ok := err.(*rest.Error); ok && restErr.Response != nil && restErr.Response.StatusCode == 404 {
			return "", nil
		}
		return "", err
	}

	// 1) Server-specific display name (nickname)
	if value(member.Nick) != "" {
		return value(member.Nick), nil
	}

	// 2) Global display name
	if value(member.User.GlobalName) != "" {
		return value(member.User.GlobalName), nil
	}

	// 3) Fallback: username (when no display names are set)
	return member.User.Username, nil
}

func (d *DiscordBanner) GetUsername(userID string) (string, error) {
	member, err := d.session.Rest.GetMember(snowflakeID(d.guildID), snowflakeID(userID))
	if err != nil {
		if restErr, ok := err.(*rest.Error); ok && restErr.Response != nil && restErr.Response.StatusCode == 404 {
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
		members, err := d.session.Rest.GetMembers(snowflakeID(d.guildID), 1000, snowflakeID(afterID))
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			break
		}

		for _, m := range members {
			originalName := value(m.User.GlobalName)
			if originalName == "" {
				originalName = m.User.Username
			}
			displayName := originalName
			if value(m.Nick) != "" {
				displayName = value(m.Nick)
			}

			all = append(all, cmd.MemberInfo{
				UserID:       m.User.ID.String(),
				Username:     m.User.Username,
				DisplayName:  displayName,
				OriginalName: originalName,
				Nickname:     value(m.Nick),
				IsBot:        m.User.Bot,
			})
		}

		afterID = members[len(members)-1].User.ID.String()
		if len(members) < 1000 {
			break
		}
	}

	return all, nil
}

func (b *Bot) newBanner() *DiscordBanner {
	return &DiscordBanner{
		session: b.Client,
		guildID: b.Config.DiscordGuildID,
		logger:  b.Logger,
	}
}

// NewBanner creates a new DiscordBanner for external use
func (b *Bot) NewBanner() *DiscordBanner {
	return b.newBanner()
}
