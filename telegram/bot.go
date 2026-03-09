package telegram

import (
	"fmt"
	"strconv"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

type Bot struct {
	API        *tgbotapi.BotAPI
	Config     *config.Config
	DB         *db.DB
	Logger     *zap.Logger
	Notes      *cmd.NotesHandler
	Version    *cmd.VersionHandler
	Actions    *cmd.ActionsHandler
	Moderation *cmd.ModerationHandler
	Warn       *cmd.WarnHandler
	Admin      *cmd.AdminHandler
}

func New(cfg *config.Config, database *db.DB, logger *zap.Logger,
	notes *cmd.NotesHandler, version *cmd.VersionHandler, actions *cmd.ActionsHandler,
	moderation *cmd.ModerationHandler, warn *cmd.WarnHandler, admin *cmd.AdminHandler,
) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	bot := &Bot{
		API:        api,
		Config:     cfg,
		DB:         database,
		Logger:     logger.With(zap.String("platform", "telegram")),
		Notes:      notes,
		Version:    version,
		Actions:    actions,
		Moderation: moderation,
		Warn:       warn,
		Admin:      admin,
	}

	return bot, nil
}

func (b *Bot) Start() error {
	b.Logger.Info("telegram bot connected",
		zap.String("user", b.API.Self.UserName),
		zap.Int64("chat_id", b.Config.TelegramChatID),
	)

	b.registerCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.API.GetUpdatesChan(u)
	go func() {
		for update := range updates {
			if update.MyChatMember != nil {
				b.handleChatMemberUpdate(update.MyChatMember)
				continue
			}

			message := update.Message
			if message == nil {
				message = update.ChannelPost
			}

			if message == nil {
				continue
			}

			if message.Chat.ID != b.Config.TelegramChatID {
				continue
			}

			b.handleMessage(message)
		}
	}()

	return nil
}

func (b *Bot) Stop() {
	b.Logger.Info("shutting down telegram bot")
	b.API.StopReceivingUpdates()
}

func (b *Bot) registerCommands() {
	commands := []tgbotapi.BotCommand{
		{Command: "notes", Description: "List all available notes"},
		{Command: "note", Description: "Show a specific note"},
		{Command: "addnote", Description: "Add a new note (admin)"},
		{Command: "editnote", Description: "Edit a note (admin)"},
		{Command: "delnote", Description: "Delete a note (admin)"},
		{Command: "version", Description: "Show release info"},
		{Command: "latest", Description: "Show the latest release"},
		{Command: "actions", Description: "GitHub Actions build status"},
		{Command: "ban", Description: "Permanently ban a user (admin)"},
		{Command: "dban", Description: "Ban + delete messages (admin)"},
		{Command: "tban", Description: "Temporarily ban a user (admin)"},
		{Command: "sban", Description: "Softban a user (admin)"},
		{Command: "warn", Description: "Warn a user (admin)"},
		{Command: "warnings", Description: "Show warnings for a user"},
		{Command: "unwarn", Description: "Remove a warning (admin)"},
		{Command: "dehoist", Description: "Remove hoisting chars (admin)"},
		{Command: "addadmin", Description: "Add a bot admin (permaadmin)"},
		{Command: "removeadmin", Description: "Remove a bot admin (permaadmin)"},
	}

	cfg := tgbotapi.NewSetMyCommandsWithScope(
		tgbotapi.NewBotCommandScopeChat(b.Config.TelegramChatID),
		commands...,
	)
	if _, err := b.API.Request(cfg); err != nil {
		b.Logger.Error("failed to set telegram commands", zap.Error(err))
	}
}

func (b *Bot) handleChatMemberUpdate(update *tgbotapi.ChatMemberUpdated) {
	if update.Chat.ID != b.Config.TelegramChatID {
		leave := tgbotapi.LeaveChatConfig{ChatID: update.Chat.ID}
		b.API.Request(leave)
		b.Logger.Warn("left unauthorized chat", zap.Int64("chat_id", update.Chat.ID))
	}
}

type TelegramBanner struct {
	api    *tgbotapi.BotAPI
	chatID int64
	logger *zap.Logger
}

func (t *TelegramBanner) Platform() string { return "telegram" }
func (t *TelegramBanner) ChatID() string   { return strconv.FormatInt(t.chatID, 10) }

func (t *TelegramBanner) Ban(userID, reason string) error {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	cfg := tgbotapi.BanChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: t.chatID,
			UserID: uid,
		},
	}
	_, err := t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) Unban(userID string) error {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	cfg := tgbotapi.UnbanChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: t.chatID,
			UserID: uid,
		},
		OnlyIfBanned: true,
	}
	_, err := t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) DeleteMessages(userID string) error {
	// Telegram doesn't support bulk delete by user easily; best effort
	// The bot would need to track messages or iterate recent ones
	return nil
}

func (t *TelegramBanner) Restrict(userID string, untilDate int64) error {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	perms := tgbotapi.ChatPermissions{
		CanSendMessages:       false,
		CanSendMediaMessages:  false,
		CanSendPolls:          false,
		CanSendOtherMessages:  false,
		CanAddWebPagePreviews: false,
		CanChangeInfo:         false,
		CanInviteUsers:        false,
		CanPinMessages:        false,
	}
	cfg := tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: t.chatID,
			UserID: uid,
		},
		UntilDate:   untilDate,
		Permissions: &perms,
	}
	_, err := t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) Unrestrict(userID string) error {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	perms := tgbotapi.ChatPermissions{
		CanSendMessages:       true,
		CanSendMediaMessages:  true,
		CanSendPolls:          true,
		CanSendOtherMessages:  true,
		CanAddWebPagePreviews: true,
		CanChangeInfo:         true,
		CanInviteUsers:        true,
		CanPinMessages:        true,
	}
	cfg := tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: t.chatID,
			UserID: uid,
		},
		Permissions: &perms,
	}
	_, err := t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) SetNickname(userID, nickname string) error {
	return fmt.Errorf("telegram bots cannot rename users")
}

func (t *TelegramBanner) DMUser(userID, message string) error {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	return dmUser(t.api, uid, message)
}

func (t *TelegramBanner) GetDisplayName(userID string) (string, error) {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	member, err := t.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: t.chatID,
			UserID: uid,
		},
	})
	if err != nil {
		return "", err
	}
	name := member.User.FirstName
	if member.User.LastName != "" {
		name += " " + member.User.LastName
	}
	return name, nil
}

func (t *TelegramBanner) GetAllMembers() ([]cmd.MemberInfo, error) {
	return nil, fmt.Errorf("telegram does not support listing all members via bot API")
}

func (b *Bot) newBanner() *TelegramBanner {
	return &TelegramBanner{
		api:    b.API,
		chatID: b.Config.TelegramChatID,
		logger: b.Logger,
	}
}
