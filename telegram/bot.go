package telegram

import (
	"errors"
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
	Ping       *cmd.PingHandler
	Case       *cmd.CaseHandler

	garminProcessor *cmd.GarminProcessor
}

func New(cfg *config.Config, database *db.DB, logger *zap.Logger,
	notes *cmd.NotesHandler, version *cmd.VersionHandler, actions *cmd.ActionsHandler,
	moderation *cmd.ModerationHandler, warn *cmd.WarnHandler, ping *cmd.PingHandler,
	cases *cmd.CaseHandler,
) (*Bot, error) {
	cfg.ApplyDefaults()
	api, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	bot := &Bot{
		API:             api,
		Config:          cfg,
		DB:              database,
		Logger:          logger.With(zap.String("platform", "telegram")),
		Notes:           notes,
		Version:         version,
		Actions:         actions,
		Moderation:      moderation,
		Warn:            warn,
		Ping:            ping,
		Case:            cases,
		garminProcessor: cmd.NewGarminProcessor(),
	}

	return bot, nil
}

func (b *Bot) Start() error {
	b.Logger.Info("telegram bot connected",
		zap.String("user", b.API.Self.UserName),
		zap.Int64("chat_id", b.Config.Telegram.ChatID),
	)

	b.registerCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.API.GetUpdatesChan(u)
	go func() {
		for update := range updates {
			if update.CallbackQuery != nil {
				b.handleNoteCallback(update.CallbackQuery)
				continue
			}

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

			if message.Chat.ID != b.Config.Telegram.ChatID {
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
		{Command: "help", Description: "Show available commands"},
		{Command: "notes", Description: "List all available notes"},
		{Command: "note", Description: "Show a specific note"},
		{Command: "addnote", Description: "Add a new note (Change group info)"},
		{Command: "editnote", Description: "Edit a note (Change group info)"},
		{Command: "renamenote", Description: "Rename a note (Change group info)"},
		{Command: "delnote", Description: "Delete a note (Change group info)"},
		{Command: "version", Description: "Show release info"},
		{Command: "latest", Description: "Show the latest release"},
		{Command: "actions", Description: "Metrolist nightly downloads"},
		{Command: "ban", Description: "Permanently ban a user (Ban users)"},
		{Command: "dban", Description: "Ban a user and delete their messages (Ban users)"},
		{Command: "tban", Description: "Temporarily ban a user (Ban users)"},
		{Command: "sban", Description: "Softban a user (Ban users)"},
		{Command: "mute", Description: "Mute a user (Ban users)"},
		{Command: "warn", Description: "Warn a user (Ban users)"},
		{Command: "warnings", Description: "Show warnings for a user"},
		{Command: "warns", Description: "Show warnings for a user (alias)"},
		{Command: "unwarn", Description: "Remove a warning (Ban users)"},
		{Command: "dehoist", Description: "Remove hoisting characters (Ban users)"},
		{Command: "ping", Description: "Check latency to services"},
	}

	cfg := tgbotapi.NewSetMyCommandsWithScope(
		tgbotapi.NewBotCommandScopeChat(b.Config.Telegram.ChatID),
		commands...,
	)
	if _, err := b.API.Request(cfg); err != nil {
		b.Logger.Error("failed to set telegram commands", zap.Error(err))
	}
}

func (b *Bot) handleChatMemberUpdate(update *tgbotapi.ChatMemberUpdated) {
	if update.Chat.ID != b.Config.Telegram.ChatID {
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

// parseUserID converts a string user ID to int64 with consistent error handling
func parseUserID(userID string) (int64, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid user ID %q: %w", userID, err)
	}
	return uid, nil
}

// member fetches userID's current status in the configured group. Every
// permission check calls it, so revoked rights apply on the next command.
func (t *TelegramBanner) member(userID string) (tgbotapi.ChatMember, error) {
	uid, err := parseUserID(userID)
	if err != nil {
		return tgbotapi.ChatMember{}, err
	}
	if uid <= 0 {
		return tgbotapi.ChatMember{}, fmt.Errorf("invalid user ID %q", userID)
	}
	return t.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{ChatID: t.chatID, UserID: uid},
	})
}

// hasRight reports whether member is the group creator or an administrator
// whose native rights include right. Every other status has no rights.
func hasRight(member tgbotapi.ChatMember, right func(tgbotapi.ChatMember) bool) bool {
	return member.IsCreator() || member.IsAdministrator() && right(member)
}

func canRestrictMembers(m tgbotapi.ChatMember) bool { return m.CanRestrictMembers }
func canChangeInfo(m tgbotapi.ChatMember) bool      { return m.CanChangeInfo }

// IsAdministrator reports whether userID is the creator or an administrator of the group.
func (t *TelegramBanner) IsAdministrator(userID string) (bool, error) {
	member, err := t.member(userID)
	if err != nil {
		return false, err
	}
	return member.IsCreator() || member.IsAdministrator(), nil
}

// CanManageMember allows action only when the caller currently holds the
// native Ban users right and the target is not a group creator or administrator.
// Telegram ties every moderation action, including warnings, to that right.
func (t *TelegramBanner) CanManageMember(callerID, targetID, action string) error {
	caller, err := t.member(callerID)
	if err != nil {
		t.logger.Warn("telegram permission check failed", zap.String("user", callerID), zap.String("action", action), zap.Error(err))
		return errors.New("I could not verify your Telegram permissions.")
	}
	if !hasRight(caller, canRestrictMembers) {
		return fmt.Errorf("You need the Ban users permission in this group to use %s.", action)
	}
	if targetID == "" {
		return nil
	}
	target, err := t.member(targetID)
	if err != nil {
		t.logger.Warn("telegram target check failed", zap.String("user", targetID), zap.String("action", action), zap.Error(err))
		return errors.New("I could not verify that user's Telegram status.")
	}
	if target.IsCreator() || target.IsAdministrator() {
		return errors.New("I will not take action against a group admin.")
	}
	return nil
}

func (t *TelegramBanner) Ban(userID, reason string) error {
	return t.ban(userID, false)
}

func (t *TelegramBanner) BanAndDeleteMessages(userID, reason string) error {
	return t.ban(userID, true)
}

func (t *TelegramBanner) ban(userID string, revokeMessages bool) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
	cfg := tgbotapi.BanChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: t.chatID,
			UserID: uid,
		},
		RevokeMessages: revokeMessages,
	}
	_, err = t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) Unban(userID string) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
	cfg := tgbotapi.UnbanChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: t.chatID,
			UserID: uid,
		},
		OnlyIfBanned: true,
	}
	_, err = t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) DeleteMessages(userID string) error {
	// Telegram doesn't support bulk delete by user easily; best effort
	// The bot would need to track messages or iterate recent ones
	return nil
}

func (t *TelegramBanner) Restrict(userID string, untilDate int64) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
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
	_, err = t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) Unrestrict(userID string) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
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
	_, err = t.api.Request(cfg)
	return err
}

func (t *TelegramBanner) SetNickname(userID, nickname string) error {
	return fmt.Errorf("telegram bots cannot rename users")
}

func (t *TelegramBanner) DMUser(userID, message string) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
	return dmUser(t.api, uid, message)
}

func (t *TelegramBanner) GetDisplayName(userID string) (string, error) {
	uid, err := parseUserID(userID)
	if err != nil {
		return "", err
	}
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

func (t *TelegramBanner) GetUsername(userID string) (string, error) {
	uid, err := parseUserID(userID)
	if err != nil {
		return "", err
	}
	member, err := t.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: t.chatID,
			UserID: uid,
		},
	})
	if err != nil {
		return "", err
	}
	if member.User.UserName != "" {
		return member.User.UserName, nil
	}
	return member.User.FirstName, nil
}

func (t *TelegramBanner) GetAllMembers() ([]cmd.MemberInfo, error) {
	return nil, fmt.Errorf("telegram does not support listing all members via bot API")
}

func (b *Bot) newBanner() *TelegramBanner {
	return &TelegramBanner{
		api:    b.API,
		chatID: b.Config.Telegram.ChatID,
		logger: b.Logger,
	}
}

// NewBanner creates a new TelegramBanner for external use
func (b *Bot) NewBanner() *TelegramBanner {
	return b.newBanner()
}
