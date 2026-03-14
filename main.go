package main

import (
	"fmt"
	"os"
	"os/signal"
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

	restoreTimedBans(database, logger)

	discordBot, err := discord.New(cfg, database, logger,
		notesHandler, versionHandler, actionsHandler,
		moderationHandler, warnHandler, adminHandler, pingHandler,
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
	)
	if err != nil {
		logger.Fatal("failed to create telegram bot", zap.Error(err))
	}

	if err := telegramBot.Start(); err != nil {
		logger.Fatal("failed to start telegram bot", zap.Error(err))
	}

	logger.Info("both bots are running. Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("shutdown signal received")
	discordBot.Stop()
	telegramBot.Stop()
	logger.Info("graceful shutdown complete")
}

func restoreTimedBans(database *db.DB, logger *zap.Logger) {
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
			database.DeleteTimedBan(ban.ID)
			database.LogModAction(ban.Platform, "system", ban.UserID, "unban", "timed ban expired (bot was offline)")
			continue
		}

		remaining := time.Duration(ban.ExpiresAt-now) * time.Second
		logger.Info("restoring timed ban",
			zap.Int64("ban_id", ban.ID),
			zap.String("user_id", ban.UserID),
			zap.Duration("remaining", remaining),
		)

		time.AfterFunc(remaining, func() {
			logger.Info("timed ban expired, unbanning",
				zap.Int64("ban_id", ban.ID),
				zap.String("user_id", ban.UserID),
			)
			database.DeleteTimedBan(ban.ID)
			database.LogModAction(ban.Platform, "system", ban.UserID, "unban", "timed ban expired")
		})
	}

	logger.Info("timed bans restored", zap.Int("count", len(bans)))
}
