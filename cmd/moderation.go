package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/util"
)

// Platform constants to avoid hardcoded strings and improve maintainability
const (
	PlatformDiscord  = "discord"
	PlatformTelegram = "telegram"
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
	GetUsername(userID string) (string, error)
	GetAllMembers() ([]MemberInfo, error)
	Platform() string
	ChatID() string
}

type MemberInfo struct {
	UserID      string
	Username    string
	DisplayName string
	IsBot       bool
}

type ModerationHandler struct {
	DB          *db.DB
	CaseHandler *CaseHandler
}

// SetCaseHandler sets the case handler for moderation actions
func (h *ModerationHandler) SetCaseHandler(ch *CaseHandler) {
	h.CaseHandler = ch
}

func (h *ModerationHandler) Ban(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, *db.Case, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil, nil
	}

	if err := banner.Ban(targetID, reason); err != nil {
		return "", nil, fmt.Errorf("banning user: %w", err)
	}

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "ban", reason)

	// Create case
	var c *db.Case
	if h.CaseHandler != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(banner.Platform(), "ban", targetID, callerID, reason, targetName, moderatorName)
	}

	reasonText := " Reason: " + reason
	return fmt.Sprintf("🔨 %s has been permanently banned.%s", formatUserRef(banner, targetID), reasonText), c, nil
}

func (h *ModerationHandler) DBan(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, *db.Case, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil, nil
	}

	if err := banner.DeleteMessages(targetID); err != nil {
		return "", nil, fmt.Errorf("deleting messages: %w", err)
	}

	if err := banner.Ban(targetID, reason); err != nil {
		return "", nil, fmt.Errorf("banning user: %w", err)
	}

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "dban", reason)

	// Create case
	var c *db.Case
	if h.CaseHandler != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(banner.Platform(), "dban", targetID, callerID, reason, targetName, moderatorName)
	}

	reasonText := " Reason: " + reason
	return fmt.Sprintf("🔨 %s has been banned and their messages deleted.%s", formatUserRef(banner, targetID), reasonText), c, nil
}

func (h *ModerationHandler) TBan(banner PlatformBanner, callerID, targetID string, duration time.Duration, reason string, cfg db.PermaAdminProvider) (string, *db.Case, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil, nil
	}

	expiresAt := time.Now().Add(duration).Unix()

	if err := banner.Ban(targetID, reason); err != nil {
		return "", nil, fmt.Errorf("banning user: %w", err)
	}

	banID, err := h.DB.AddTimedBan(banner.Platform(), banner.ChatID(), targetID, expiresAt, reason)
	if err != nil {
		return "", nil, fmt.Errorf("storing timed ban: %w", err)
	}
	_ = banID

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "tban", reason)

	// Create case
	var c *db.Case
	if h.CaseHandler != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(banner.Platform(), "tban", targetID, callerID, reason, targetName, moderatorName)
	}

	reasonText := " Reason: " + reason
	return fmt.Sprintf("⏱️ %s has been banned for %s.%s", formatUserRef(banner, targetID), util.FormatDuration(duration), reasonText), c, nil
}

func (h *ModerationHandler) SBan(banner PlatformBanner, callerID, targetID, reason string, cfg db.PermaAdminProvider) (string, *db.Case, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not ban an admin.", nil, nil
	}

	banner.DMUser(targetID, fmt.Sprintf("You have been softbanned from Metrolist. Reason: %s", reason))

	if banner.Platform() == PlatformDiscord {
		if err := banner.Ban(targetID, reason); err != nil {
			return "", nil, fmt.Errorf("banning user: %w", err)
		}
		if err := banner.Unban(targetID); err != nil {
			return "", nil, fmt.Errorf("unbanning user: %w", err)
		}
	} else {
		if err := banner.Restrict(targetID, time.Now().Add(35*time.Second).Unix()); err != nil {
			return "", nil, fmt.Errorf("restricting user: %w", err)
		}
		time.AfterFunc(35*time.Second, func() {
			banner.Unrestrict(targetID)
		})
	}

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "sban", reason)

	// Create case
	var c *db.Case
	if h.CaseHandler != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(banner.Platform(), "sban", targetID, callerID, reason, targetName, moderatorName)
	}

	reasonText := " Reason: " + reason
	return fmt.Sprintf("🧹 %s has been softbanned.%s", formatUserRef(banner, targetID), reasonText), c, nil
}

func (h *ModerationHandler) Mute(banner PlatformBanner, callerID, targetID string, duration time.Duration, reason string, cfg db.PermaAdminProvider) (string, *db.Case, error) {
	if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not mute an admin.", nil, nil
	}

	expiresAt := time.Now().Add(duration).Unix()

	if err := banner.Restrict(targetID, expiresAt); err != nil {
		return "", nil, fmt.Errorf("muting user: %w", err)
	}

	// Store timed mute for restoration after restart
	muteID, err := h.DB.AddMute(banner.Platform(), banner.ChatID(), targetID, expiresAt, reason)
	if err != nil {
		return "", nil, fmt.Errorf("storing timed mute: %w", err)
	}
	_ = muteID

	h.DB.LogModAction(banner.Platform(), callerID, targetID, "mute", reason)

	// Create case
	var c *db.Case
	if h.CaseHandler != nil {
		targetName, _ := banner.GetDisplayName(targetID)
		moderatorName, _ := banner.GetDisplayName(callerID)
		c, _ = h.CaseHandler.CreateCaseAndLog(banner.Platform(), "mute", targetID, callerID, reason, targetName, moderatorName)
	}

	// Schedule unmute
	time.AfterFunc(duration, func() {
		h.DB.DeleteMute(muteID)
		h.DB.LogModAction(banner.Platform(), "system", targetID, "unmute", "timed mute expired")
	})

	reasonText := " Reason: " + reason
	return fmt.Sprintf("🔇 %s has been muted for %s.%s", formatUserRef(banner, targetID), util.FormatDuration(duration), reasonText), c, nil
}

func (h *ModerationHandler) Dehoist(banner PlatformBanner, targetID string, dry bool, cfg db.PermaAdminProvider) (string, error) {
	// Never touch admins (permaadmins + DB-defined admins)
	if targetID != "" && h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
		return "I will not dehoist an admin.", nil
	}

	if dry {
		return h.dehoistDryRun(banner, targetID, cfg)
	}

	// Bulk dehoist: targetID empty, non-dry run
	if targetID == "" {
		// Telegram cannot rename users; fall back to dry-run output.
		if banner.Platform() == PlatformTelegram {
			return h.dehoistDryRun(banner, targetID, cfg)
		}

		members, err := banner.GetAllMembers()
		if err != nil {
			return "", fmt.Errorf("getting members: %w", err)
		}

		totalMembers := len(members)
		successCount := 0

		for _, m := range members {
			// Skip admins and bots entirely
			if h.DB.IsAdmin(banner.Platform(), m.UserID, cfg) || m.IsBot {
				continue
			}

			name := m.DisplayName
			if name == "" {
				continue
			}

			newName := stripHoistChars(name)
			if newName == name {
				continue
			}

			if err := banner.SetNickname(m.UserID, newName); err != nil {
				// Best-effort: continue with other members when one rename fails.
				continue
			}

			successCount++
		}

		return fmt.Sprintf("Successfully dehoisted %d members out of %d server members.", successCount, totalMembers), nil
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

	if banner.Platform() == PlatformTelegram {
		return fmt.Sprintf("⚠️ Cannot rename users on Telegram. Please manually change your username: @%s", targetID), nil
	}

	if err := banner.SetNickname(targetID, newName); err != nil {
		return "", fmt.Errorf("setting nickname: %w", err)
	}

	return fmt.Sprintf("@%s - \"%s\" → \"%s\"", targetID, displayName, newName), nil
}

func (h *ModerationHandler) dehoistDryRun(banner PlatformBanner, targetID string, cfg db.PermaAdminProvider) (string, error) {
	if targetID != "" {
		if h.DB.IsAdmin(banner.Platform(), targetID, cfg) {
			return "I will not dehoist an admin.", nil
		}

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
		if h.DB.IsAdmin(banner.Platform(), m.UserID, cfg) || m.IsBot {
			continue
		}

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
	if banner.Platform() == PlatformTelegram {
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
		if seenNormal && (r == ' ' || r == '.' || r == '-' || r == '_' || r == ',' || r == ':' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' || r == '|' || r == '\'' || r == '#') {
			b.WriteRune(r)
		}
	}

	result := b.String()

	if result == "" {
		return "change your display name"
	}
	return result
}

func formatUserRef(banner PlatformBanner, userID string) string {
	// Get username to format nicely without pinging
	username, err := banner.GetUsername(userID)
	if err != nil || username == "" {
		// Fallback to userID if we can't get username
		return "`@" + userID + "`"
	}

	return "`@" + username + "`"
}
