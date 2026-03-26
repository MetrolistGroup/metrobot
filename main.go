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
	timers map[string]*time.Timer
	mutex  sync.RWMutex
}

// NewTimerManager creates a new TimerManager
func NewTimerManager() *TimerManager {
	return &TimerManager{
		timers: make(map[string]*time.Timer),
	}
}

// AddTimer adds a timer with automatic cleanup when it completes
func (tm *TimerManager) AddTimer(id string, timer *time.Timer, cleanupCallback func()) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// Store the timer
	tm.timers[id] = timer

	// Wrap the original timer function to auto-remove from manager
	go func() {
		select {
		case <-timer.C:
			// Timer completed naturally
			tm.RemoveTimer(id)
			if cleanupCallback != nil {
				cleanupCallback()
			}
		}
	}()
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

// StopAll stops and removes all managed timers
func (tm *TimerManager) StopAll() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	for id, timer := range tm.timers {
		timer.Stop()
		delete(tm.timers, id)
	}
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

	logger.Info("starting metrolist bot")

	database, err := db.Open("bot.db")
	if err != nil {
		logger.Fatal("failed to open database", zap.Error(err))
	}
	defer database.Close()

	logger.Info("database initialized")

	actionsClient := gh.NewActionsClient(cfg.GitHubToken, cfg.GitHubOwner, cfg.GitHubRepo, cfg.ActionsWorkflowFile)
	releasesClient := gh.NewReleasesClient(cfg.GitHubToken, cfg.GitHubOwner, cfg.GitHubRepo)

	notesHandler := &cmd.NotesHandler{DB: database}
	versionHandler := &cmd.VersionHandler{Releases: releasesClient}
	actionsHandler := &cmd.ActionsHandler{Actions: actionsClient, Config: cfg}
	moderationHandler := &cmd.ModerationHandler{DB: database}
	warnHandler := &cmd.WarnHandler{DB: database}
	adminHandler := &cmd.AdminHandler{DB: database}
	pingHandler := &cmd.PingHandler{}
	caseHandler := &cmd.CaseHandler{DB: database}

	// Wire up case handler with moderation handlers
	moderationHandler.SetCaseHandler(caseHandler)
	warnHandler.SetCaseHandler(caseHandler)

	// Create timer manager for proper cleanup
	timerManager := NewTimerManager()

	restoreTimedBans(database, logger, timerManager)

	discordBot, err := discord.New(cfg, database, logger,
		notesHandler, versionHandler, actionsHandler,
		moderationHandler, warnHandler, adminHandler, pingHandler,
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
		moderationHandler, warnHandler, adminHandler, pingHandler,
		caseHandler,
	)
	if err != nil {
		logger.Fatal("failed to create telegram bot", zap.Error(err))
	}

	if err := telegramBot.Start(); err != nil {
		logger.Fatal("failed to start telegram bot", zap.Error(err))
	}

	logger.Info("both bots are running. Press Ctrl+C to stop.")

	// Restore timed mutes after both bots are started
	restoreTimedMutes(database, discordBot, telegramBot, logger, timerManager)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("shutdown signal received")

	// Stop all timers before shutting down bots
	timerManager.StopAll()
	logger.Info("all timers stopped")

	discordBot.Stop()
	telegramBot.Stop()
	logger.Info("graceful shutdown complete")
}

func restoreTimedBans(database *db.DB, logger *zap.Logger, timerManager *TimerManager) {
	bans, err := database.GetPendingTimedBans()
	if err != nil {
		logger.Error("failed to load pending timed bans", zap.Error(err))
		return
	}

	now := time.Now().Unix()
	for _, ban := range bans {
		ban := ban
		if ban.ExpiresAt <= now {
			logger.Info("expired timed ban found, removing",
				zap.Int64("ban_id", ban.ID),
				zap.String("user_id", ban.UserID),
			)
			if err := database.DeleteTimedBan(ban.ID); err != nil {
				logger.Error("failed to delete expired timed ban", zap.Error(err))
			}
			if err := database.LogModAction(ban.Platform, "system", ban.UserID, "unban", "timed ban expired (bot was offline)"); err != nil {
				logger.Error("failed to log mod action for expired ban", zap.Error(err))
			}
			continue
		}

		remaining := time.Duration(ban.ExpiresAt-now) * time.Second
		logger.Info("restoring timed ban",
			zap.Int64("ban_id", ban.ID),
			zap.String("user_id", ban.UserID),
			zap.Duration("remaining", remaining),
		)

		timer := time.AfterFunc(remaining, func() {
			// Timer callback moved to AddTimer
		})

		// Create a unique ID for this ban timer
		banTimerID := fmt.Sprintf("ban_%d", ban.ID)

		// Add timer with cleanup callback
		timerManager.AddTimer(banTimerID, timer, func() {
			logger.Info("timed ban expired, unbanning",
				zap.Int64("ban_id", ban.ID),
				zap.String("user_id", ban.UserID),
			)
			if err := database.DeleteTimedBan(ban.ID); err != nil {
				logger.Error("failed to delete expired timed ban", zap.Error(err), zap.Int64("ban_id", ban.ID))
			}
			if err := database.LogModAction(ban.Platform, "system", ban.UserID, "unban", "timed ban expired"); err != nil {
				logger.Error("failed to log mod action for ban expiry", zap.Error(err), zap.String("user_id", ban.UserID))
			}
		})
	}

	logger.Info("timed bans restored", zap.Int("count", len(bans)))
}

func restoreTimedMutes(database *db.DB, discordBot *discord.Bot, telegramBot *telegram.Bot, logger *zap.Logger, timerManager *TimerManager) {
	mutes, err := database.GetPendingMutes()
	if err != nil {
		logger.Error("failed to load pending timed mutes", zap.Error(err))
		return
	}

	now := time.Now().Unix()
	for _, mute := range mutes {
		mute := mute
		if mute.ExpiresAt <= now {
			logger.Info("expired timed mute found, removing",
				zap.Int64("mute_id", mute.ID),
				zap.String("user_id", mute.UserID),
			)
			if err := database.DeleteMute(mute.ID); err != nil {
				logger.Error("failed to delete expired timed mute", zap.Error(err))
			}
			if err := database.LogModAction(mute.Platform, "system", mute.UserID, "unmute", "timed mute expired (bot was offline)"); err != nil {
				logger.Error("failed to log mod action for expired mute", zap.Error(err))
			}
			continue
		}

		remaining := time.Duration(mute.ExpiresAt-now) * time.Second
		logger.Info("restoring timed mute",
			zap.Int64("mute_id", mute.ID),
			zap.String("user_id", mute.UserID),
			zap.Duration("remaining", remaining),
		)

		timer := time.AfterFunc(remaining, func() {
			// Timer callback moved to AddTimer
		})

		// Create a unique ID for this mute timer
		muteTimerID := fmt.Sprintf("mute_%d", mute.ID)

		// Add timer with cleanup callback
		timerManager.AddTimer(muteTimerID, timer, func() {
			logger.Info("timed mute expired, unmuting",
				zap.Int64("mute_id", mute.ID),
				zap.String("user_id", mute.UserID),
			)

			// Unrestrict the user based on platform
			if mute.Platform == "discord" {
				banner := discordBot.NewBanner()
				if err := banner.Unrestrict(mute.UserID); err != nil {
					logger.Error("failed to unrestrict Discord user", zap.Error(err), zap.String("user_id", mute.UserID))
				}
			} else if mute.Platform == "telegram" {
				banner := telegramBot.NewBanner()
				if err := banner.Unrestrict(mute.UserID); err != nil {
					logger.Error("failed to unrestrict Telegram user", zap.Error(err), zap.String("user_id", mute.UserID))
				}
			}

			if err := database.DeleteMute(mute.ID); err != nil {
				logger.Error("failed to delete expired timed mute", zap.Error(err), zap.Int64("mute_id", mute.ID))
			}
			if err := database.LogModAction(mute.Platform, "system", mute.UserID, "unmute", "timed mute expired"); err != nil {
				logger.Error("failed to log mod action for mute expiry", zap.Error(err), zap.String("user_id", mute.UserID))
			}
		})
	}

	logger.Info("timed mutes restored", zap.Int("count", len(mutes)))
}
