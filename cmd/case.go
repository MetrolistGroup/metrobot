package cmd

import (
	"fmt"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

// CaseLogger interface for logging cases to Discord
type CaseLogger interface {
	LogCaseToDiscord(c *db.Case, targetName, moderatorName string) error
}

type CaseHandler struct {
	DB         *db.DB
	CaseLogger CaseLogger
}

// CreateCase creates a new case and optionally logs it to Discord
func (h *CaseHandler) CreateCase(platform, actionType, targetID, moderatorID, reason string) (*db.Case, error) {
	c, err := h.DB.CreateCase(platform, actionType, targetID, moderatorID, reason)
	if err != nil {
		return nil, fmt.Errorf("creating case: %w", err)
	}
	return c, nil
}

// GetCase retrieves a case by its case number
func (h *CaseHandler) GetCase(caseNumber int64) (*db.Case, error) {
	return h.DB.GetCase(caseNumber)
}

// GetCasesForUser retrieves all cases for a specific user
func (h *CaseHandler) GetCasesForUser(platform, userID string) ([]db.Case, error) {
	return h.DB.GetCasesForUser(platform, userID)
}

// FormatCase formats a case for display
func (h *CaseHandler) FormatCase(c *db.Case, targetName, moderatorName string) string {
	reason := c.Reason
	if reason == "" {
		reason = "no reason provided"
	}
	ts := time.Unix(c.Timestamp, 0).Format("2006-01-02 15:04:05")

	platformEmoji := "💬"
	if c.Platform == "discord" {
		platformEmoji = "🎮"
	} else if c.Platform == "telegram" {
		platformEmoji = "✈️"
	}

	return fmt.Sprintf(
		"**Case #%d** %s\n"+
			"**Action:** %s\n"+
			"**Target:** %s (%s)\n"+
			"**Moderator:** %s\n"+
			"**Reason:** %s\n"+
			"**Time:** %s",
		c.CaseNumber, platformEmoji,
		c.ActionType,
		targetName, c.Platform,
		moderatorName,
		reason,
		ts,
	)
}

// CreateCaseAndLog creates a case and logs it to Discord if a logger is configured
func (h *CaseHandler) CreateCaseAndLog(platform, actionType, targetID, moderatorID, reason, targetName, moderatorName string) (*db.Case, error) {
	c, err := h.CreateCase(platform, actionType, targetID, moderatorID, reason)
	if err != nil {
		return nil, err
	}

	// Log to Discord if logger is available
	if h.CaseLogger != nil {
		if err := h.CaseLogger.LogCaseToDiscord(c, targetName, moderatorName); err != nil {
			// Don't fail if logging fails, just continue
			return c, nil
		}
	}

	return c, nil
}
