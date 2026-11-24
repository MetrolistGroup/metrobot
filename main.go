package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/discord"
	gh "github.com/MetrolistGroup/metrobot/github"
	"github.com/MetrolistGroup/metrobot/log"
	"github.com/MetrolistGroup/metrobot/telegram"
	"go.uber.org/zap"
)

// TimerManager manages active timers for cleanup on shutdown and automatic memory management
type TimerManager struct {
	timers  map[string]*time.Timer
	mutex   sync.RWMutex
	stopped bool
	running sync.WaitGroup
}

// NewTimerManager creates a new TimerManager
func NewTimerManager() *TimerManager {
	return &TimerManager{
		timers: make(map[string]*time.Timer),
	}
}

// AddTimer adds a timer with automatic cleanup when it completes. After
// StopAll it does nothing, so in-flight callbacks cannot schedule retries.
func (tm *TimerManager) AddTimer(id string, duration time.Duration, cleanupCallback func()) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	if tm.stopped {
		return
	}
	if old, exists := tm.timers[id]; exists {
		old.Stop()
	}
	var timer *time.Timer
	// The callback blocks on the mutex until the timer is registered below.
	timer = time.AfterFunc(duration, func() {
		tm.mutex.Lock()
		if tm.stopped || tm.timers[id] != timer {
			tm.mutex.Unlock()
			return
		}
		delete(tm.timers, id)
		tm.running.Add(1)
		tm.mutex.Unlock()
		defer tm.running.Done()

		if cleanupCallback != nil {
			cleanupCallback()
		}
	})
	tm.timers[id] = timer
}

// RemoveTimer removes a specific timer from management
func (tm *TimerManager) RemoveTimer(id string) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if timer, exists := tm.timers[id]; exists {
		timer.Stop()
		delete(tm.timers, id)
	}
}

// StopAll rejects new timers, cancels pending ones and waits for running callbacks.
func (tm *TimerManager) StopAll() {
	tm.mutex.Lock()

	tm.stopped = true
	for id, timer := range tm.timers {
		timer.Stop()
		delete(tm.timers, id)
	}
	tm.mutex.Unlock()
	tm.running.Wait()
}

// Count returns the number of active timers (useful for monitoring)
func (tm *TimerManager) Count() int {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()
	return len(tm.timers)
}

func main() {
	cfg, err := config.Load("config.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %s\n", err)
		os.Exit(1)
	}

	logger, err := log.New(cfg.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to init logger: %s\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	zap.ReplaceGlobals(logger) // cmd logs per-call usage through zap.L()

	logger.Info("starting metrolist bot")

	database, err := db.Open("bot.db")
	if err != nil {
		logger.Fatal("failed to open database", zap.Error(err))
	}
	defer database.Close()

	logger.Info("database initialized")

	if cfg.ActionsWorkflowFile != "" || cfg.ActionsArtifactNames.Foss != "" || cfg.ActionsArtifactNames.GMS != "" {
		logger.Info("ignoring legacy actions_workflow_file and actions_artifact_names; nightly uses GitHub release tag assets")
	}
	releasesClient, err := gh.NewReleasesClient(cfg.GitHubToken, cfg.GitHubOwner, cfg.GitHubRepo)
	if err != nil {
		logger.Fatal("failed to initialize GitHub releases client", zap.Error(err))
	}

	notesHandler := &cmd.NotesHandler{DB: database}
	versionHandler := &cmd.VersionHandler{Releases: releasesClient}
	actionsHandler := &cmd.ActionsHandler{Releases: releasesClient}
	// Create timer manager for proper cleanup
	timerManager := NewTimerManager()

	moderationHandler := &cmd.ModerationHandler{DB: database, Scheduler: timerManager}
	warnHandler := &cmd.WarnHandler{DB: database, Moderation: moderationHandler}
	pingHandler := &cmd.PingHandler{}
	caseHandler := &cmd.CaseHandler{DB: database}

	// Wire up case handler with moderation handlers
	moderationHandler.SetCaseHandler(caseHandler)
	warnHandler.SetCaseHandler(caseHandler)

	discordBot, err := discord.New(cfg, database, logger,
		notesHandler, versionHandler, actionsHandler,
		moderationHandler, warnHandler, pingHandler,
		caseHandler,
	)
	if err != nil {
		logger.Fatal("failed to create discord bot", zap.Error(err))
	}

	if err := discordBot.Start(); err != nil {
		logger.Fatal("failed to start discord bot", zap.Error(err))
	}

	telegramBot, err := telegram.New(cfg, database, logger,
		notesHandler, versionHandler, actionsHandler,
		moderationHandler, warnHandler, pingHandler,
		caseHandler,
	)
	if err != nil {
		logger.Fatal("failed to create telegram bot", zap.Error(err))
	}

	if err := telegramBot.Start(); err != nil {
		logger.Fatal("failed to start telegram bot", zap.Error(err))
	}

	logger.Info("both bots are running. Press Ctrl+C to stop.")

	// Restore timed moderation after both bots are started so expiry can call the platform APIs.
	restoreTimedBans(database, moderationHandler, discordBot, telegramBot, logger, timerManager)
	restoreTimedMutes(database, moderationHandler, discordBot, telegramBot, logger, timerManager)
	if err := discordBot.RestoreReminders(); err != nil {
		logger.Error("failed to restore reminders", zap.Error(err))
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	signal.Stop(sigCh)
	logger.Info("shutdown signal received")

	// Stop all timers before shutting down bots
	timerManager.StopAll()
	logger.Info("all timers stopped")

	discordBot.Stop()
	telegramBot.Stop()
	logger.Info("graceful shutdown complete")
}

func restoreTimedBans(database *db.DB, moderation *cmd.ModerationHandler, discordBot *discord.Bot, telegramBot *telegram.Bot, logger *zap.Logger, timerManager *TimerManager) {
	bans, err := database.GetPendingTimedBans()
	if err != nil {
		logger.Error("failed to load pending timed bans", zap.Error(err))
		return
	}

	now := time.Now().Unix()
	for _, ban := range bans {
		banner, err := platformBanner(ban.Platform, ban.ChatID, discordBot, telegramBot)
		if err != nil {
			logger.Error("cannot restore timed ban", zap.Int64("ban_id", ban.ID), zap.Error(err))
			continue
		}
		remaining := max(time.Duration(ban.ExpiresAt-now)*time.Second, 0)
		logger.Info("restoring timed ban",
			zap.Int64("ban_id", ban.ID),
			zap.String("user_id", ban.UserID),
			zap.Duration("remaining", remaining),
		)
		timerManager.AddTimer(fmt.Sprintf("ban_%d", ban.ID), remaining, func() {
			moderation.ExpireTimedBan(banner, ban.ID, ban.UserID)
		})
	}

	logger.Info("timed bans restored", zap.Int("count", len(bans)))
}

func restoreTimedMutes(database *db.DB, moderation *cmd.ModerationHandler, discordBot *discord.Bot, telegramBot *telegram.Bot, logger *zap.Logger, timerManager *TimerManager) {
	mutes, err := database.GetPendingMutes()
	if err != nil {
		logger.Error("failed to load pending timed mutes", zap.Error(err))
		return
	}

	now := time.Now().Unix()
	for _, mute := range mutes {
		banner, err := platformBanner(mute.Platform, mute.ChatID, discordBot, telegramBot)
		if err != nil {
			logger.Error("cannot restore timed mute", zap.Int64("mute_id", mute.ID), zap.Error(err))
			continue
		}
		remaining := max(time.Duration(mute.ExpiresAt-now)*time.Second, 0)
		logger.Info("restoring timed mute",
			zap.Int64("mute_id", mute.ID),
			zap.String("user_id", mute.UserID),
			zap.Duration("remaining", remaining),
		)
		timerManager.AddTimer(fmt.Sprintf("mute_%d", mute.ID), remaining, func() {
			moderation.ExpireMute(banner, mute.ID, mute.UserID)
		})
	}

	logger.Info("timed mutes restored", zap.Int("count", len(mutes)))
}

// platformBanner returns the banner for a stored punishment's platform.
func platformBanner(platform, chatID string, discordBot *discord.Bot, telegramBot *telegram.Bot) (cmd.PlatformBanner, error) {
	var banner cmd.PlatformBanner
	switch platform {
	case cmd.PlatformDiscord:
		banner = discordBot.NewBanner()
	case cmd.PlatformTelegram:
		banner = telegramBot.NewBanner()
	default:
		return nil, fmt.Errorf("unsupported platform %q", platform)
	}
	if banner.ChatID() != chatID {
		return nil, fmt.Errorf("stored %s chat %q differs from configured chat %q", platform, chatID, banner.ChatID())
	}
	return banner, nil
}
