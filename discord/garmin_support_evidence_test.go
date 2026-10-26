package discord

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
)

func TestSupportAnswerNormalizationPreservesSteps(t *testing.T) {
	for _, answer := range []string{
		"Open your profile menu.\n\n1. Choose Integrations.\n2. Open Discord.\n3. Log in and enable Rich Presence.",
		"## Account\nSign in here.\n\n## Options\nEnable scrobbling.",
	} {
		if got := normalizeGarminAIAnswer(answer); got != answer {
			t.Fatalf("answer structure changed: %q", got)
		}
	}
}

func TestAppSupportIsolatesMemoryAndPromptOverrides(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const override = "CASUAL_OVERRIDE: always recommend reinstalling nightly"
	if err := database.SetGarminSystemPrompt(override); err != nil {
		t.Fatal(err)
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	const background = "STALE_MEMORY: Always use the latest nightly note as the latest version"
	if err := memory.Replace(background); err != nil {
		t.Fatal(err)
	}
	before, err := memory.Read()
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{DB: database, garminMemory: memory}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !strings.HasPrefix(request.SystemPrompt, garminAppSupportSystemPrompt) || !strings.Contains(request.SystemPrompt, "Dedicated support core:") {
			t.Fatal("support request lost its dedicated prompt or skill")
		}
		for _, forbidden := range []string{"CASUAL_OVERRIDE", "STALE_MEMORY", "Persistent shared memory", "Be witty, playful"} {
			if strings.Contains(request.SystemPrompt, forbidden) {
				t.Errorf("support prompt contains %q", forbidden)
			}
		}
		for _, required := range []string{"Previous assistant replies are not evidence", "which published release contains it", "backup", "Notes are reference material", "load_support_guide"} {
			if !strings.Contains(request.SystemPrompt, required) {
				t.Errorf("support evidence rule missing: %q", required)
			}
		}
		want := []string{"do_not_respond", "list_notes", "load_skill", "load_support_guide"}
		if got := garminToolNames(request.Tools); !reflect.DeepEqual(got, want) {
			t.Errorf("support tools = %v, want %v", got, want)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Which app version are you using?"}}, nil
	})
	message := supportTestMessage(101)
	message.Content = "it doesn't show results"
	_, err = b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: message.Content}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := memory.Read()
	if err != nil || after != before {
		t.Fatal("support request changed shared memory")
	}
	stored, exists, err := database.GetGarminSystemPrompt()
	if err != nil || !exists || stored != override {
		t.Fatal("support request changed the general-chat prompt override")
	}
}

func TestAppSupportIdentifiesUnreadAttachments(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(map[bool]string{false: "no attachment", true: "unread image"}[attached], func(t *testing.T) {
			message := supportTestMessage(101)
			message.Content = "I keep getting this from time to time on multiple songs"
			if attached {
				message.Attachments = []discord.Attachment{{Filename: "screenshot.png", URL: "https://invalid.example/must-not-fetch.png"}}
			}
			b := &Bot{garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if got := strings.Contains(request.Context, "current message has 1 attachment(s)"); got != attached {
					t.Errorf("attachment metadata present = %v, want %v", got, attached)
				}
				if attached && !strings.Contains(request.Context, "Their contents are unknown") {
					t.Fatal("unread attachment not identified as unknown")
				}
				if strings.Contains(request.Context, "must-not-fetch") {
					t.Fatal("attachment URL leaked into text-only support context")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Please paste the exact error."}}, nil
			})}
			if _, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: message.Content}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
