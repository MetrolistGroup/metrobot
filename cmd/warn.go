package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/util"
	"go.uber.org/zap"
)

type WarnHandler struct {
	DB          *db.DB
	CaseHandler *CaseHandler
	// Moderation owns stored mutes so warning timeouts share its target locks
	// and expiry timers. Without it, Warn uses an unshared handler.
	Moderation *ModerationHandler
}

// SetCaseHandler sets the case handler for warn actions
func (h *WarnHandler) SetCaseHandler(ch *CaseHandler) {
	h.CaseHandler = ch
}

func (h *WarnHandler) Warn(banner PlatformBanner, callerID, targetID, reason string) (string, []string, *db.Case, error) {
	platform := banner.Platform()

	if err := banner.CanManageMember(callerID, targetID, "warn"); err != nil {
		return "", nil, nil, err
	}

	_, err := h.DB.AddWarning(platform, targetID, reason, callerID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("adding warning: %w", err)
	}

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

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "warn", reason)

	// Calculate escalating timeout: 1h + (count-1) * 20m
	timeoutDuration := time.Hour + time.Duration(count-1)*20*time.Minute
	moderation := h.Moderation
	if moderation == nil {
		moderation = &ModerationHandler{DB: h.DB}
	}

	autoAction := "timed out for " + util.FormatDuration(timeoutDuration)
	if err := moderation.TimeoutAtLeast(banner, targetID, timeoutDuration, fmt.Sprintf("timeout after warn #%d", count)); err != nil {
		autoAction = "the timeout failed"
		if logErr := h.DB.LogModAction(platform, "system", targetID, "timeout_failed", fmt.Sprintf("Failed to timeout user after warn #%d: %s", count, err)); logErr != nil {
			zap.L().Error("saving moderation log entry", zap.String("action", "timeout_failed"), zap.Error(logErr))
		}
	} else if logErr := h.DB.LogModAction(platform, "system", targetID, "timeout", fmt.Sprintf("Timeout for %s after warn #%d", util.FormatDuration(timeoutDuration), count)); logErr != nil {
		zap.L().Error("saving moderation log entry", zap.String("action", "timeout"), zap.Error(logErr))
	}

	response := fmt.Sprintf("%s has been warned. Reason: %s (warning #%d). Auto-action: %s.%s", formatUserRef(banner, targetID), formatUserText(banner, reasonText), count, autoAction, note)

	return response, nil, c, nil
}

func (h *WarnHandler) Warnings(banner PlatformBanner, targetID string) (string, error) {
	platform := banner.Platform()
	warnings, err := h.DB.GetWarnings(platform, targetID)
	if err != nil {
		return "", fmt.Errorf("getting warnings: %w", err)
	}

	if len(warnings) == 0 {
		return fmt.Sprintf("%s has no warnings.", formatUserRef(banner, targetID)), nil
	}

	var sb strings.Builder
	if banner.Platform() == PlatformTelegram {
		sb.WriteString(fmt.Sprintf("<b>Warnings for %s:</b>\n", formatUserRef(banner, targetID)))
	} else {
		sb.WriteString(fmt.Sprintf("### Warnings for %s\n", formatUserRef(banner, targetID)))
	}

	for i, w := range warnings {
		reason := w.Reason
		if reason == "" {
			reason = "no reason"
		}
		ts := time.Unix(w.Timestamp, 0).Format("2006-01-02")
		sb.WriteString(fmt.Sprintf("[%d] %s - by %s - %s\n", i+1, formatUserText(banner, reason), formatUserRef(banner, w.WarnedBy), ts))
	}

	// Mirrors Warn's escalation: the next warn is number len+1.
	next := time.Hour + time.Duration(len(warnings))*20*time.Minute
	sb.WriteString(fmt.Sprintf("\nTotal: %d. The next warning times out for %s.", len(warnings), util.FormatDuration(next)))

	return sb.String(), nil
}

func (h *WarnHandler) Unwarn(platform, callerID, targetID string, index int, banner PlatformBanner) (string, *db.Case, error) {
	if index < 1 {
		return "", nil, fmt.Errorf("warning IDs start at 1")
	}
	if banner == nil {
		return "", nil, fmt.Errorf("platform permissions are unavailable")
	}
	if err := banner.CanManageMember(callerID, targetID, "unwarn"); err != nil {
		return "", nil, err
	}

	if err := h.DB.DeleteWarningByIndex(platform, targetID, index-1); err != nil {
		return "", nil, fmt.Errorf("deleting warning: %w", err)
	}

	reason := fmt.Sprintf("removed warning #%d", index)
	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "unwarn", reason)
	return fmt.Sprintf("Warning #%d removed from %s.%s", index, formatUserRef(banner, targetID), note), c, nil
}
