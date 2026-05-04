package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/util"
)

type WarnHandler struct {
	DB          *db.DB
	CaseHandler *CaseHandler
}

// SetCaseHandler sets the case handler for warn actions
func (h *WarnHandler) SetCaseHandler(ch *CaseHandler) {
	h.CaseHandler = ch
}

func (h *WarnHandler) Warn(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, []string, *db.Case, error) {
	platform := banner.Platform()

	if h.DB.IsAdmin(platform, targetID, cfg) {
		return "I will not warn an admin.", nil, nil, nil
	}

	_, err := h.DB.AddWarning(platform, targetID, reason, callerID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("adding warning: %w", err)
	}

	h.DB.LogModAction(platform, callerID, targetID, "warn", reason)

	count, err := h.DB.GetWarningCount(platform, targetID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("getting warning count: %w", err)
	}

	reasonText := reason
	if reasonText == "" {
		reasonText = "no reason provided"
	}

	banner.DMUser(targetID, fmt.Sprintf(
		"You have been warned in Metrolist for: %s. This is warning %d.",
		reasonText, count,
	))

	// Create case
	var c *db.Case
	if h.CaseHandler != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(banner.Platform(), "warn", targetID, callerID, reason, targetName, moderatorName)
	}

	// Calculate escalating timeout: 1h + (count-1) * 20m
	timeoutDuration := time.Hour + time.Duration(count-1)*20*time.Minute
	timeoutExpiresAt := time.Now().Add(timeoutDuration).Unix()

	if err := banner.Restrict(targetID, timeoutExpiresAt); err != nil {
		h.DB.LogModAction(platform, "system", targetID, "timeout_failed", fmt.Sprintf("Failed to timeout user after warn #%d: %s", count, err))
	}

	h.DB.LogModAction(platform, "system", targetID, "timeout", fmt.Sprintf("Timeout for %s after warn #%d", util.FormatDuration(timeoutDuration), count))

	response := fmt.Sprintf("⚠️ %s has been warned. Reason: %s (warning #%d). Auto-action: timed out for %s.", formatUserRef(banner, targetID), reasonText, count, util.FormatDuration(timeoutDuration))

	return response, nil, c, nil
}

func (h *WarnHandler) Warnings(banner PlatformBanner, targetID string) (string, error) {
	platform := banner.Platform()
	warnings, err := h.DB.GetWarnings(platform, targetID)
	if err != nil {
		return "", fmt.Errorf("getting warnings: %w", err)
	}

	threshold, err := h.DB.GetWarnThreshold(platform, targetID)
	if err != nil {
		return "", fmt.Errorf("getting warn threshold: %w", err)
	}

	if len(warnings) == 0 {
		return fmt.Sprintf("%s has no warnings. (0/%d)", formatUserRef(banner, targetID), threshold), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Warnings for %s:**\n", formatUserRef(banner, targetID)))

	for i, w := range warnings {
		reason := w.Reason
		if reason == "" {
			reason = "no reason"
		}
		ts := time.Unix(w.Timestamp, 0).Format("2006-01-02")
		sb.WriteString(fmt.Sprintf("[%d] %s - by %s - %s\n", i+1, reason, formatUserRef(banner, w.WarnedBy), ts))
	}

	sb.WriteString(fmt.Sprintf("\nWarnings: %d/%d", len(warnings), threshold))

	return sb.String(), nil
}

func (h *WarnHandler) Unwarn(platform, callerID, targetID string, index int, banner PlatformBanner) (string, *db.Case, error) {
	if index < 1 {
		return "", nil, fmt.Errorf("warning IDs start at 1")
	}

	if err := h.DB.DeleteWarningByIndex(platform, targetID, index-1); err != nil {
		return "", nil, fmt.Errorf("deleting warning: %w", err)
	}

	h.DB.LogModAction(platform, callerID, targetID, "unwarn", fmt.Sprintf("removed warning #%d", index))

	// Create case
	var c *db.Case
	if h.CaseHandler != nil && banner != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(platform, "unwarn", targetID, callerID, fmt.Sprintf("removed warning #%d", index), targetName, moderatorName)
	}

	var targetRef string
	if banner != nil {
		targetRef = formatUserRef(banner, targetID)
	} else {
		targetRef = "`@" + targetID + "`"
	}

	return fmt.Sprintf("Warning #%d removed from %s.", index, targetRef), c, nil
}
