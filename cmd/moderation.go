package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

type PlatformBanner interface {
	Ban(userID, reason string) error
	Unban(userID string) error
	DeleteMessages(userID string) error
	Restrict(userID string, untilDate int64) error
	Unrestrict(userID string) error
	SetNickname(userID, nickname string) error
	DMUser(userID, message string) error
	GetDisplayName(userID string) (string, error)
	GetAllMembers() ([]MemberInfo, error)
	Platform() string
	ChatID() string
}

type MemberInfo struct {
	UserID      string
	Username    string
	DisplayName string
}

type ModerationHandler struct {
	DB *db.DB
}

func (h *ModerationHandler) Ban(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil
	}

	if err := banner.Ban(targetID, reason); err != nil {
		return "", fmt.Errorf("banning user: %w", err)
	}

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "ban", reason)

	reasonText := ""
	if reason != "" {
		reasonText = " Reason: " + reason
	}
	return fmt.Sprintf("🔨 <@%s> has been permanently banned.%s", targetID, reasonText), nil
}

func (h *ModerationHandler) DBan(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil
	}

	if err := banner.DeleteMessages(targetID); err != nil {
		return "", fmt.Errorf("deleting messages: %w", err)
	}

	if err := banner.Ban(targetID, reason); err != nil {
		return "", fmt.Errorf("banning user: %w", err)
	}

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "dban", reason)

	reasonText := ""
	if reason != "" {
		reasonText = " Reason: " + reason
	}
	return fmt.Sprintf("🔨 <@%s> has been banned and their messages deleted.%s", targetID, reasonText), nil
}

func (h *ModerationHandler) TBan(banner PlatformBanner, callerID, targetID string, duration time.Duration, reason string, cfg db.PermaAdminProvider) (string, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil
	}

	expiresAt := time.Now().Add(duration).Unix()

	if err := banner.Ban(targetID, reason); err != nil {
		return "", fmt.Errorf("banning user: %w", err)
	}

	banID, err := h.DB.AddTimedBan(banner.Platform(), banner.ChatID(), targetID, expiresAt, reason)
	if err != nil {
		return "", fmt.Errorf("storing timed ban: %w", err)
	}
	_ = banID

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "tban", reason)

	reasonText := ""
	if reason != "" {
		reasonText = " Reason: " + reason
	}
	return fmt.Sprintf("⏱️ <@%s> has been banned for %s.%s", targetID, formatDuration(duration), reasonText), nil
}

func (h *ModerationHandler) SBan(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil
	}

	banner.DMUser(targetID, fmt.Sprintf("You have been softbanned from Metrolist. Reason: %s", reason))

	if banner.Platform() == "discord" {
		if err := banner.Ban(targetID, reason); err != nil {
			return "", fmt.Errorf("banning user: %w", err)
		}
		if err := banner.Unban(targetID); err != nil {
			return "", fmt.Errorf("unbanning user: %w", err)
		}
	} else {
		if err := banner.Restrict(targetID, time.Now().Add(35*time.Second).Unix()); err != nil {
			return "", fmt.Errorf("restricting user: %w", err)
		}
		time.AfterFunc(35*time.Second, func() {
			banner.Unrestrict(targetID)
		})
	}

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "sban", reason)

	reasonText := ""
	if reason != "" {
		reasonText = " Reason: " + reason
	}
	return fmt.Sprintf("🧹 <@%s> has been softbanned.%s", targetID, reasonText), nil
}

func (h *ModerationHandler) Dehoist(banner PlatformBanner, targetID string, dry bool) (string, error) {
	if dry {
		return h.dehoistDryRun(banner, targetID)
	}

	displayName, err := banner.GetDisplayName(targetID)
	if err != nil {
		return "", fmt.Errorf("getting display name: %w", err)
	}

	// If there is no display name (server or global), there is nothing to dehoist.
	if displayName == "" {
		return "✅ No members need dehoisting.", nil
	}

	newName := stripHoistChars(displayName)
	if newName == displayName {
		return fmt.Sprintf("@%s does not need dehoisting.", targetID), nil
	}

	if banner.Platform() == "telegram" {
		return fmt.Sprintf("⚠️ Cannot rename users on Telegram. Please manually change your username: @%s", targetID), nil
	}

	if err := banner.SetNickname(targetID, newName); err != nil {
		return "", fmt.Errorf("setting nickname: %w", err)
	}

	return fmt.Sprintf("@%s - \"%s\" → \"%s\"", targetID, displayName, newName), nil
}

func (h *ModerationHandler) dehoistDryRun(banner PlatformBanner, targetID string) (string, error) {
	if targetID != "" {
		displayName, err := banner.GetDisplayName(targetID)
		if err != nil {
			return "", fmt.Errorf("getting display name: %w", err)
		}
		if displayName == "" {
			return "✅ No members need dehoisting.", nil
		}
		newName := stripHoistChars(displayName)
		if newName == displayName {
			return "✅ No members need dehoisting.", nil
		}
		return fmt.Sprintf("@%s - \"%s\" → \"%s\"", targetID, displayName, newName), nil
	}

	members, err := banner.GetAllMembers()
	if err != nil {
		return "", fmt.Errorf("getting members: %w", err)
	}

	var results []string
	for _, m := range members {
		name := m.DisplayName
		if name == "" {
			continue
		}
		newName := stripHoistChars(name)
		if newName != name {
			results = append(results, fmt.Sprintf("%s → %s", name, newName))
		}
	}

	if len(results) == 0 {
		return "✅ No members need dehoisting.", nil
	}

	list := strings.Join(results, "\n")
	result := fmt.Sprintf("```\n%s\n```", list)
	if banner.Platform() == "telegram" {
		result += "\n\n⚠️ Note: Telegram bots cannot rename users. This is informational only."
	}

	return result, nil
}

func stripHoistChars(s string) string {
	var b strings.Builder
	seenNormal := false

	for _, r := range s {
		// Normal (allowed) base characters
		if (r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') {
			b.WriteRune(r)
			seenNormal = true
			continue
		}

		// Secondary allowed chars: only AFTER we've seen at least one normal char
		if seenNormal && (r == ' ' || r == '.' || r == '-' || r == '_') {
			b.WriteRune(r)
		}
	}

	result := b.String()

	if result == "" {
		return "change your username"
	}
	return result
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		mins := int(d.Minutes()) % 60
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	}
	days := int(d.Hours()) / 24
	return fmt.Sprintf("%dd", days)
}
