package main

import (
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/discord"
	"github.com/MetrolistGroup/metrobot/telegram"
)

func TestTimerManagerRegistrationAndShutdown(t *testing.T) {
	tm := NewTimerManager()

	// A zero-delay timer must not leave a stale entry after it fires.
	fired := make(chan struct{})
	tm.AddTimer("zero", 0, func() { close(fired) })
	<-fired
	if tm.Count() != 0 {
		t.Fatalf("fired zero-delay timer is still registered: %d", tm.Count())
	}

	tm.StopAll()
	ran := make(chan struct{}, 1)
	tm.AddTimer("retry", 0, func() { ran <- struct{}{} })
	if tm.Count() != 0 {
		t.Fatal("AddTimer registered a timer after StopAll")
	}
	select {
	case <-ran:
		t.Fatal("timer scheduled after StopAll ran")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestTimerManagerSkipsInvalidatedCallbacks(t *testing.T) {
	for _, change := range []string{"replace", "remove", "stop"} {
		t.Run(change, func(t *testing.T) {
			tm := NewTimerManager()
			defer tm.StopAll()
			ran := make(chan struct{}, 1)
			tm.AddTimer("id", time.Hour, func() { ran <- struct{}{} })
			tm.mutex.Lock()
			tm.timers["id"].Reset(0)
			time.Sleep(10 * time.Millisecond)
			delete(tm.timers, "id")
			var replacement *time.Timer
			if change == "replace" {
				replacement = time.AfterFunc(time.Hour, func() {})
				tm.timers["id"] = replacement
			} else if change == "stop" {
				tm.stopped = true
			}
			tm.mutex.Unlock()
			select {
			case <-ran:
				t.Fatal("invalidated callback ran")
			case <-time.After(20 * time.Millisecond):
			}
			tm.mutex.RLock()
			current := tm.timers["id"]
			tm.mutex.RUnlock()
			if current != replacement {
				t.Fatal("old callback removed the replacement")
			}
		})
	}
}

func TestTimerManagerShutdownWaitsForRunningCallback(t *testing.T) {
	tm := NewTimerManager()
	started, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tm.AddTimer("running", 0, func() {
		close(started)
		<-release
		tm.AddTimer("retry", 0, func() { t.Error("retry ran during shutdown") })
	})
	<-started
	go func() { tm.StopAll(); close(stopped) }()
	select {
	case <-stopped:
		t.Error("shutdown returned while a callback was running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the callback returned")
	}
	if tm.Count() != 0 {
		t.Fatal("callback scheduled a retry during shutdown")
	}
}

func TestPlatformBannerRejectsPunishmentsFromOtherChats(t *testing.T) {
	cfg := &config.Config{DiscordGuildID: "300", TelegramChatID: -100}
	cfg.ApplyDefaults()
	discordBot := &discord.Bot{Config: cfg}
	telegramBot := &telegram.Bot{Config: cfg}
	for _, test := range []struct {
		platform, chatID string
		allowed          bool
	}{
		{"discord", "300", true},
		{"telegram", "-100", true},
		{"discord", "400", false},
		{"telegram", "-200", false},
		{"discord", "", false},
		{"unknown", "300", false},
	} {
		banner, err := platformBanner(test.platform, test.chatID, discordBot, telegramBot)
		if (err == nil) != test.allowed || test.allowed && banner.ChatID() != test.chatID {
			t.Errorf("platformBanner(%q, %q) = %v, %v", test.platform, test.chatID, banner, err)
		}
	}
}
