package cmd

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/decancer"
	"github.com/MetrolistGroup/metrobot/util"
	"go.uber.org/zap"
)

// Platform constants to avoid hardcoded strings and improve maintainability
const (
	PlatformDiscord  = "discord"
	PlatformTelegram = "telegram"
)

// discordMaxTimeout is the longest timeout Discord accepts.
const discordMaxTimeout = 28 * 24 * time.Hour

// expiryMaxAttempts bounds in-process retries of a failed unban or unmute.
// The row stays stored afterwards, so the next restart tries again.
const expiryMaxAttempts = 5

type PlatformBanner interface {
	IsAdministrator(userID string) (bool, error)
	CanManageMember(callerID, targetID, action string) error
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
	UserID       string
	Username     string
	DisplayName  string
	OriginalName string
	Nickname     string
	IsBot        bool
}

type ModerationHandler struct {
	DB          *db.DB
	CaseHandler *CaseHandler
	Scheduler   TimerScheduler

	// targetLocks serializes punishment changes and expiry per target.
	targetLocks sync.Map
}

type TimerScheduler interface {
	AddTimer(id string, duration time.Duration, cleanupCallback func())
}

type messageDeletingBanner interface {
	BanAndDeleteMessages(userID, reason string) error
}

type kickingBanner interface {
	Kick(userID, reason string) error
}

// SetCaseHandler sets the case handler for moderation actions
func (h *ModerationHandler) SetCaseHandler(ch *CaseHandler) {
	h.CaseHandler = ch
}

func (h *ModerationHandler) lockTarget(banner PlatformBanner, userID string) func() {
	value, _ := h.targetLocks.LoadOrStore(banner.Platform()+"/"+banner.ChatID()+"/"+userID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// recordModeration writes the audit entry and case for an action that already
// happened on the platform. Failures come back as a note for the response so
// the caller never reports the action itself as failed or repeats it.
func recordModeration(database *db.DB, cases *CaseHandler, banner PlatformBanner, callerID, targetID, action, reason string) (*db.Case, string) {
	var note string
	if err := database.LogModAction(banner.Platform(), callerID, targetID, action, reason); err != nil {
		zap.L().Error("saving moderation log entry", zap.String("action", action), zap.String("target", targetID), zap.Error(err))
		note = " The moderation log entry could not be saved."
	}
	if cases == nil {
		return nil, note
	}
	c, err := cases.CreateCase(banner.Platform(), action, targetID, callerID, reason)
	if err != nil {
		zap.L().Error("saving moderation case", zap.String("action", action), zap.String("target", targetID), zap.Error(err))
		return nil, note + " The case record could not be saved."
	}
	if cases.CaseLogger != nil {
		// The log post needs two name lookups and a send; the moderator's reply should not wait for them.
		go func() {
			targetName, _ := banner.GetDisplayName(targetID)
			moderatorName, _ := banner.GetDisplayName(callerID)
			_ = cases.CaseLogger.LogCaseToDiscord(c, targetName, moderatorName)
		}()
	}
	return c, note
}

// clearTimedBans drops stored timed bans after a permanent ban or an unban so
// their timers cannot lift the target's current state.
func (h *ModerationHandler) clearTimedBans(banner PlatformBanner, targetID string) {
	if err := h.DB.DeleteOtherTimedBans(banner.Platform(), banner.ChatID(), targetID, 0); err != nil {
		zap.L().Error("clearing superseded timed bans", zap.String("target", targetID), zap.Error(err))
	}
}

func (h *ModerationHandler) Ban(banner PlatformBanner, callerID, targetID, reason string) (string, *db.Case, error) {
	if err := banner.CanManageMember(callerID, targetID, "ban"); err != nil {
		return "", nil, err
	}

	unlock := h.lockTarget(banner, targetID)
	if err := banner.Ban(targetID, reason); err != nil {
		unlock()
		return "", nil, fmt.Errorf("banning user: %w", err)
	}
	h.clearTimedBans(banner, targetID)
	unlock()

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "ban", reason)

	reasonText := " Reason: " + formatUserText(banner, reason)
	return fmt.Sprintf("%s has been permanently banned.%s%s", formatUserRef(banner, targetID), reasonText, note), c, nil
}

func (h *ModerationHandler) DBan(banner PlatformBanner, callerID, targetID, reason string) (string, *db.Case, error) {
	if err := banner.CanManageMember(callerID, targetID, "dban"); err != nil {
		return "", nil, err
	}

	unlock := h.lockTarget(banner, targetID)
	if deletingBanner, ok := banner.(messageDeletingBanner); ok {
		if err := deletingBanner.BanAndDeleteMessages(targetID, reason); err != nil {
			unlock()
			return "", nil, fmt.Errorf("banning user and deleting messages: %w", err)
		}
	} else {
		if err := banner.DeleteMessages(targetID); err != nil {
			unlock()
			return "", nil, fmt.Errorf("deleting messages: %w", err)
		}

		if err := banner.Ban(targetID, reason); err != nil {
			unlock()
			return "", nil, fmt.Errorf("banning user: %w", err)
		}
	}
	h.clearTimedBans(banner, targetID)
	unlock()

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "dban", reason)

	reasonText := " Reason: " + formatUserText(banner, reason)
	return fmt.Sprintf("%s has been banned and their messages deleted.%s%s", formatUserRef(banner, targetID), reasonText, note), c, nil
}

func (h *ModerationHandler) TBan(banner PlatformBanner, callerID, targetID string, duration time.Duration, reason string) (string, *db.Case, error) {
	if err := banner.CanManageMember(callerID, targetID, "tban"); err != nil {
		return "", nil, err
	}

	expiresAt := time.Now().Add(duration).Unix()

	unlock := h.lockTarget(banner, targetID)
	banID, err := h.DB.AddTimedBan(banner.Platform(), banner.ChatID(), targetID, expiresAt, reason)
	if err != nil {
		unlock()
		return "", nil, fmt.Errorf("storing timed ban: %w", err)
	}

	if err := banner.Ban(targetID, reason); err != nil {
		_ = h.DB.DeleteTimedBan(banID)
		unlock()
		return "", nil, fmt.Errorf("banning user: %w", err)
	}
	if err := h.DB.DeleteOtherTimedBans(banner.Platform(), banner.ChatID(), targetID, banID); err != nil {
		zap.L().Error("clearing superseded timed bans", zap.String("target", targetID), zap.Error(err))
	}
	unlock()

	h.scheduleTimer(fmt.Sprintf("ban_%d", banID), duration, func() {
		h.ExpireTimedBan(banner, banID, targetID)
	})

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "tban", reason)

	reasonText := " Reason: " + formatUserText(banner, reason)
	return fmt.Sprintf("%s has been banned for %s.%s%s", formatUserRef(banner, targetID), util.FormatDuration(duration), reasonText, note), c, nil
}

func (h *ModerationHandler) Kick(banner PlatformBanner, callerID, targetID, reason string) (string, *db.Case, error) {
	if err := banner.CanManageMember(callerID, targetID, "kick"); err != nil {
		return "", nil, err
	}

	kicker, ok := banner.(kickingBanner)
	if !ok {
		return "", nil, fmt.Errorf("kicking is not supported on %s", banner.Platform())
	}
	if err := kicker.Kick(targetID, reason); err != nil {
		return "", nil, fmt.Errorf("kicking user: %w", err)
	}

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "kick", reason)

	return fmt.Sprintf("%s has been kicked. Reason: %s%s", formatUserRef(banner, targetID), formatUserText(banner, reason), note), c, nil
}

func (h *ModerationHandler) SBan(banner PlatformBanner, callerID, targetID, reason string) (string, *db.Case, error) {
	if err := banner.CanManageMember(callerID, targetID, "sban"); err != nil {
		return "", nil, err
	}

	banner.DMUser(targetID, fmt.Sprintf("You have been softbanned from Metrolist. Reason: %s", reason))

	unlock := h.lockTarget(banner, targetID)
	if deletingBanner, ok := banner.(messageDeletingBanner); ok {
		if err := deletingBanner.BanAndDeleteMessages(targetID, reason); err != nil {
			unlock()
			return "", nil, fmt.Errorf("banning user and deleting messages: %w", err)
		}
	} else if err := banner.Ban(targetID, reason); err != nil {
		unlock()
		return "", nil, fmt.Errorf("banning user: %w", err)
	}
	if err := banner.Unban(targetID); err != nil {
		unlock()
		return "", nil, fmt.Errorf("user was banned, but unbanning failed: %w", err)
	}
	// The unban also lifted any earlier timed ban.
	h.clearTimedBans(banner, targetID)
	unlock()

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "sban", reason)

	reasonText := " Reason: " + formatUserText(banner, reason)
	return fmt.Sprintf("%s has been softbanned.%s%s", formatUserRef(banner, targetID), reasonText, note), c, nil
}

func (h *ModerationHandler) Mute(banner PlatformBanner, callerID, targetID string, duration time.Duration, reason string) (string, *db.Case, error) {
	if err := banner.CanManageMember(callerID, targetID, "timeout"); err != nil {
		return "", nil, err
	}
	if banner.Platform() == PlatformDiscord && duration > discordMaxTimeout {
		return "Discord timeouts can last at most 28 days.", nil, nil
	}

	if err := h.restrictFor(banner, targetID, duration, reason, false); err != nil {
		return "", nil, err
	}

	c, note := recordModeration(h.DB, h.CaseHandler, banner, callerID, targetID, "mute", reason)

	reasonText := " Reason: " + formatUserText(banner, reason)
	return fmt.Sprintf("%s has been muted for %s.%s%s", formatUserRef(banner, targetID), util.FormatDuration(duration), reasonText, note), c, nil
}

// TimeoutAtLeast restricts the target for duration as a stored, expiring mute
// unless a longer stored mute already covers it. Automatic timeouts use it so
// they never shorten a manual mute and an older mute timer cannot end them.
func (h *ModerationHandler) TimeoutAtLeast(banner PlatformBanner, targetID string, duration time.Duration, reason string) error {
	return h.restrictFor(banner, targetID, duration, reason, true)
}

// restrictFor stores a mute row, applies it, drops the target's older rows and
// schedules expiry. The newest row is the only one whose timer may unmute.
func (h *ModerationHandler) restrictFor(banner PlatformBanner, targetID string, duration time.Duration, reason string, keepLonger bool) error {
	unlock := h.lockTarget(banner, targetID)
	expiresAt := time.Now().Add(duration).Unix()
	if keepLonger {
		pending, err := h.DB.PendingMuteExpiry(banner.Platform(), banner.ChatID(), targetID)
		if err != nil {
			unlock()
			return fmt.Errorf("checking stored mute: %w", err)
		}
		if pending > expiresAt {
			expiresAt = pending
			duration = time.Until(time.Unix(pending, 0))
		}
	}
	muteID, err := h.DB.AddMute(banner.Platform(), banner.ChatID(), targetID, expiresAt, reason)
	if err != nil {
		unlock()
		return fmt.Errorf("storing timed mute: %w", err)
	}
	if err := banner.Restrict(targetID, expiresAt); err != nil {
		_ = h.DB.DeleteMute(muteID)
		unlock()
		return fmt.Errorf("muting user: %w", err)
	}
	if err := h.DB.DeleteOtherMutes(banner.Platform(), banner.ChatID(), targetID, muteID); err != nil {
		zap.L().Error("clearing superseded mutes", zap.String("target", targetID), zap.Error(err))
	}
	unlock()

	h.scheduleTimer(fmt.Sprintf("mute_%d", muteID), duration, func() {
		h.ExpireMute(banner, muteID, targetID)
	})
	return nil
}

// ExpireTimedBan lifts a timed ban if it still owns the target's ban. Live
// timers and restart restoration both use it.
func (h *ModerationHandler) ExpireTimedBan(banner PlatformBanner, banID int64, targetID string) {
	h.expirePunishment(banner, targetID, punishmentExpiry{
		timerID:   fmt.Sprintf("ban_%d", banID),
		id:        banID,
		action:    "unban",
		reason:    "timed ban expired",
		isCurrent: h.DB.IsCurrentTimedBan,
		remove:    h.DB.DeleteTimedBan,
		lift:      banner.Unban,
	}, 1)
}

// ExpireMute lifts a timed mute if it still owns the target's restriction.
// Live timers and restart restoration both use it.
func (h *ModerationHandler) ExpireMute(banner PlatformBanner, muteID int64, targetID string) {
	h.expirePunishment(banner, targetID, punishmentExpiry{
		timerID:   fmt.Sprintf("mute_%d", muteID),
		id:        muteID,
		action:    "unmute",
		reason:    "timed mute expired",
		isCurrent: h.DB.IsCurrentMute,
		remove:    h.DB.DeleteMute,
		lift:      banner.Unrestrict,
	}, 1)
}

type punishmentExpiry struct {
	timerID, action, reason string
	id                      int64
	isCurrent               func(int64) (bool, error)
	remove                  func(int64) error
	lift                    func(string) error
}

func (h *ModerationHandler) expirePunishment(banner PlatformBanner, targetID string, e punishmentExpiry, attempt int) {
	unlock := h.lockTarget(banner, targetID)
	defer unlock()

	current, err := e.isCurrent(e.id)
	if err == nil && !current {
		// A newer punishment replaced this one; leave the target alone.
		if err := e.remove(e.id); err != nil {
			zap.L().Warn("deleting superseded punishment", zap.String("timer", e.timerID), zap.Error(err))
		}
		return
	}
	if err == nil {
		err = e.lift(targetID)
	}
	if err != nil {
		if attempt >= expiryMaxAttempts {
			zap.L().Error("punishment expiry failed; keeping it for the next restart", zap.String("timer", e.timerID), zap.Int("attempt", attempt), zap.Error(err))
			return
		}
		delay := time.Duration(1<<attempt) * time.Minute
		zap.L().Warn("punishment expiry failed; retrying", zap.String("timer", e.timerID), zap.Int("attempt", attempt), zap.Duration("retry_in", delay), zap.Error(err))
		h.scheduleTimer(e.timerID, delay, func() {
			h.expirePunishment(banner, targetID, e, attempt+1)
		})
		return
	}
	if err := e.remove(e.id); err != nil {
		zap.L().Error("deleting expired punishment", zap.String("timer", e.timerID), zap.Error(err))
	}
	if err := h.DB.LogModAction(banner.Platform(), "system", targetID, e.action, e.reason); err != nil {
		zap.L().Error("saving moderation log entry", zap.String("action", e.action), zap.String("target", targetID), zap.Error(err))
	}
	zap.L().Info("punishment expired", zap.String("timer", e.timerID), zap.String("target", targetID))
}

func (h *ModerationHandler) scheduleTimer(id string, duration time.Duration, cleanupCallback func()) {
	if h.Scheduler != nil {
		h.Scheduler.AddTimer(id, duration, cleanupCallback)
		return
	}
	time.AfterFunc(duration, cleanupCallback)
}

func (h *ModerationHandler) Dehoist(banner PlatformBanner, callerID, targetID string, dry bool) (string, error) {
	if err := banner.CanManageMember(callerID, targetID, "dehoist"); err != nil {
		return "", err
	}
	if targetID != "" {
		admin, err := banner.IsAdministrator(targetID)
		if err != nil {
			return "", fmt.Errorf("checking target permissions: %w", err)
		}
		if admin {
			return "I will not dehoist an admin.", nil
		}
	}

	if dry {
		return h.dehoistDryRun(banner, callerID, targetID)
	}

	// Bulk dehoist: targetID empty, non-dry run
	if targetID == "" {
		// Telegram cannot rename users; fall back to dry-run output.
		if banner.Platform() == PlatformTelegram {
			return h.dehoistDryRun(banner, callerID, targetID)
		}

		members, err := banner.GetAllMembers()
		if err != nil {
			return "", fmt.Errorf("getting members: %w", err)
		}

		totalMembers := len(members)
		successCount := 0

		for _, m := range members {
			// Skip admins and bots entirely
			admin, err := banner.IsAdministrator(m.UserID)
			if err != nil || admin || m.IsBot || banner.CanManageMember(callerID, m.UserID, "dehoist") != nil {
				continue
			}

			_, newName, ok := originalNameDehoistChange(m)
			if !ok {
				continue
			}

			if err := banner.SetNickname(m.UserID, newName); err != nil {
				// Best-effort: continue with other members when one rename fails.
				continue
			}

			successCount++
		}

		return fmt.Sprintf("Dehoisted %d members out of %d server members.", successCount, totalMembers), nil
	}

	displayName, err := banner.GetDisplayName(targetID)
	if err != nil {
		return "", fmt.Errorf("getting display name: %w", err)
	}

	// If there is no display name (server or global), there is nothing to dehoist.
	if displayName == "" {
		return "No members need dehoisting.", nil
	}

	newName := stripHoistChars(displayName)
	if newName == displayName {
		return fmt.Sprintf("@%s does not need dehoisting.", targetID), nil
	}

	if banner.Platform() == PlatformTelegram {
		return fmt.Sprintf("I can't rename users on Telegram. Change your username manually: @%s", targetID), nil
	}

	if err := banner.SetNickname(targetID, newName); err != nil {
		return "", fmt.Errorf("setting nickname: %w", err)
	}

	return fmt.Sprintf("@%s - \"%s\" → \"%s\"", targetID, displayName, newName), nil
}

func (h *ModerationHandler) dehoistDryRun(banner PlatformBanner, callerID, targetID string) (string, error) {
	if targetID != "" {
		displayName, err := banner.GetDisplayName(targetID)
		if err != nil {
			return "", fmt.Errorf("getting display name: %w", err)
		}
		if displayName == "" {
			return "No members need dehoisting.", nil
		}
		newName := stripHoistChars(displayName)
		if newName == displayName {
			return "No members need dehoisting.", nil
		}
		return fmt.Sprintf("@%s - \"%s\" → \"%s\"", targetID, displayName, newName), nil
	}

	members, err := banner.GetAllMembers()
	if err != nil {
		return "", fmt.Errorf("getting members: %w", err)
	}

	var results []string
	for _, m := range members {
		admin, err := banner.IsAdministrator(m.UserID)
		if err != nil || admin || m.IsBot || banner.CanManageMember(callerID, m.UserID, "dehoist") != nil {
			continue
		}

		currentName, newName, ok := originalNameDehoistChange(m)
		if ok {
			results = append(results, fmt.Sprintf("%s → %s", currentName, newName))
		}
	}

	if len(results) == 0 {
		return "No members need dehoisting.", nil
	}

	list := strings.Join(results, "\n")
	result := fmt.Sprintf("```\n%s\n```", list)
	if banner.Platform() == PlatformTelegram {
		result += "\n\nTelegram bots cannot rename users. This is a preview only."
	}

	return result, nil
}

// NeedsDehoisting reports whether the name would change after dehoisting.
func NeedsDehoisting(name string) bool {
	return name != "" && stripHoistChars(name) != name
}

// originalNameDehoistChange returns a bulk migration only when the member has
// no nickname or their nickname matches output produced by the old dehoister.
func originalNameDehoistChange(member MemberInfo) (string, string, bool) {
	if member.OriginalName == "" {
		return "", "", false
	}

	currentName := member.OriginalName
	if member.Nickname != "" {
		if member.Nickname != stripHoistCharsLegacy(member.OriginalName) {
			return "", "", false
		}
		currentName = member.Nickname
	}

	newName := stripHoistChars(member.OriginalName)
	return currentName, newName, currentName != newName
}

// stripHoistCharsLegacy identifies nicknames generated by the previously
// shipped dehoister so a server-wide rerun does not overwrite manual names.
func stripHoistCharsLegacy(s string) string {
	return stripAllowedHoistChars(s)
}

func stripHoistChars(s string) string {
	return stripAllowedHoistChars(decancer.Cure(s))
}

func stripAllowedHoistChars(s string) string {
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
	if banner.Platform() == PlatformDiscord {
		if _, err := strconv.ParseUint(userID, 10, 64); err == nil {
			return "<@" + userID + ">"
		}
	}

	username, err := banner.GetUsername(userID)
	if err != nil || username == "" {
		username = userID
	}
	if banner.Platform() == PlatformTelegram {
		username = html.EscapeString(username)
		if _, err := strconv.ParseInt(userID, 10, 64); err == nil {
			return fmt.Sprintf(`<a href="tg://user?id=%s">@%s</a>`, userID, username)
		}
	}

	return "@" + username
}

func formatUserText(banner PlatformBanner, text string) string {
	if banner.Platform() == PlatformTelegram {
		return html.EscapeString(text)
	}
	return text
}
