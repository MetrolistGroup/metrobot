package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

type WarnHandler struct {
	DB *db.DB
}

func (h *WarnHandler) Warn(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, []string, error) {
	platform := banner.Platform()

	if h.DB.IsAdmin(platform, targetID, cfg) {
		return "I will not warn an admin.", nil, nil
	}

	_, err := h.DB.AddWarning(platform, targetID, reason, callerID)
	if err != nil {
		return "", nil, fmt.Errorf("adding warning: %w", err)
	}

	h.DB.LogModAction(platform, callerID, targetID, "warn", reason)

	count, err := h.DB.GetWarningCount(platform, targetID)
	if err != nil {
		return "", nil, fmt.Errorf("getting warning count: %w", err)
	}

	threshold, err := h.DB.GetWarnThreshold(platform, targetID)
	if err != nil {
		return "", nil, fmt.Errorf("getting warn threshold: %w", err)
	}

	reasonText := reason
	if reasonText == "" {
		reasonText = "no reason provided"
	}

	var extraMessages []string

	banner.DMUser(targetID, fmt.Sprintf(
		"You have been warned in Metrolist for: %s. This is warning %d of %d.",
		reasonText, count, threshold,
	))

	response := fmt.Sprintf("⚠️ %s has been warned. Reason: %s (%d/%d)", formatUserRef(platform, targetID), reasonText, count, threshold)

	if count >= threshold {
		if err := banner.Ban(targetID, "Auto-ban: warning threshold reached"); err != nil {
			return "", nil, fmt.Errorf("auto-banning user: %w", err)
		}
		h.DB.LogModAction(banner.Platform(), "system", targetID, "ban", "Auto-ban: warning threshold reached")

		extraMessages = append(extraMessages,
			fmt.Sprintf("⚠️ <@%s> has been permanently banned after reaching %d warnings.", targetID, threshold))
	}

	return response, extraMessages, nil
}

func (h *WarnHandler) Warnings(platform, targetID string) (string, error) {
	warnings, err := h.DB.GetWarnings(platform, targetID)
	if err != nil {
		return "", fmt.Errorf("getting warnings: %w", err)
	}

	threshold, err := h.DB.GetWarnThreshold(platform, targetID)
	if err != nil {
		return "", fmt.Errorf("getting warn threshold: %w", err)
	}

	if len(warnings) == 0 {
		return fmt.Sprintf("%s has no warnings. (0/%d)", formatUserRef(platform, targetID), threshold), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Warnings for %s:**\n", formatUserRef(platform, targetID)))

	for i, w := range warnings {
		reason := w.Reason
		if reason == "" {
			reason = "no reason"
		}
		ts := time.Unix(w.Timestamp, 0).Format("2006-01-02")
		sb.WriteString(fmt.Sprintf("[%d] %s - by %s - %s\n", i+1, reason, formatUserRef(platform, w.WarnedBy), ts))
	}

	sb.WriteString(fmt.Sprintf("\nWarnings: %d/%d", len(warnings), threshold))

	return sb.String(), nil
}

func (h *WarnHandler) Unwarn(platform, callerID, targetID string, index int) (string, error) {
	if index < 1 {
		return "", fmt.Errorf("warning IDs start at 1")
	}

	if err := h.DB.DeleteWarningByIndex(platform, targetID, index-1); err != nil {
		return "", fmt.Errorf("deleting warning: %w", err)
	}

	h.DB.LogModAction(platform, callerID, targetID, "unwarn", fmt.Sprintf("removed warning #%d", index))

	return fmt.Sprintf("Warning #%d removed from %s.", index, formatUserRef(platform, targetID)), nil
}
