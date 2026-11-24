package cmd

import (
	"errors"
	"testing"
	"time"
)

func TestNativePermissionFailurePreventsModerationMutation(t *testing.T) {
	for _, action := range []string{"ban", "dban", "tban", "sban", "kick", "mute", "warn", "unwarn"} {
		t.Run(action, func(t *testing.T) {
			database := openTimerTestDB(t)
			if err := database.AddAdmin("discord", "legacy-admin", "old-owner"); err != nil {
				t.Fatal(err)
			}
			scheduler := &recordingScheduler{}
			h := &ModerationHandler{DB: database, Scheduler: scheduler}
			w := &WarnHandler{DB: database, Moderation: h}
			denied := errors.New("native permission lookup failed")
			banner := &fakeModerationBanner{platform: "discord", chatID: "300", manageErr: denied}
			wantWarnings := 0
			if action == "unwarn" {
				if _, err := database.AddWarning("discord", "target", "existing", "mod"); err != nil {
					t.Fatal(err)
				}
				wantWarnings = 1
			}
			var err error
			switch action {
			case "ban":
				_, _, err = h.Ban(banner, "legacy-admin", "target", "reason")
			case "dban":
				_, _, err = h.DBan(banner, "legacy-admin", "target", "reason")
			case "tban":
				_, _, err = h.TBan(banner, "legacy-admin", "target", time.Hour, "reason")
			case "sban":
				_, _, err = h.SBan(banner, "legacy-admin", "target", "reason")
			case "kick":
				_, _, err = h.Kick(banner, "legacy-admin", "target", "reason")
			case "mute":
				_, _, err = h.Mute(banner, "legacy-admin", "target", time.Hour, "reason")
			case "warn":
				_, _, _, err = w.Warn(banner, "legacy-admin", "target", "reason")
			case "unwarn":
				_, _, err = w.Unwarn("discord", "legacy-admin", "target", 1, banner)
			}
			if !errors.Is(err, denied) {
				t.Fatalf("native denial was bypassed: %v", err)
			}
			bans, banErr := database.GetPendingTimedBans()
			mutes, muteErr := database.GetPendingMutes()
			warnings, warnErr := database.GetWarningCount("discord", "target")
			if banErr != nil || muteErr != nil || warnErr != nil || len(bans) != 0 || len(mutes) != 0 || warnings != wantWarnings || len(scheduler.callbacks) != 0 || banner.kickCalls != 0 {
				t.Fatalf("denied action mutated state: bans=%v mutes=%v warnings=%d timers=%d", bans, mutes, warnings, len(scheduler.callbacks))
			}
		})
	}
}
