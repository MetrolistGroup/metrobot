package discord

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestCasualChatCanCorrectLoginClaimAndRetrieveGuides(t *testing.T) {
	for _, prompt := range []string{
		"explain login to him and how to get yt music playlists onto metrolist",
		"yes it does",
	} {
		t.Run(prompt, func(t *testing.T) {
			memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
			if err != nil {
				t.Fatal(err)
			}
			const stale = "Metrolist doesn't do login. Metrolist is strictly Android only."
			if err := memory.Replace(stale); err != nil {
				t.Fatal(err)
			}
			before, err := memory.Read()
			if err != nil {
				t.Fatal(err)
			}
			b := &Bot{garminMemory: memory}
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if !strings.Contains(request.SystemPrompt, "Login is not a KMP-only feature") || !strings.Contains(request.SystemPrompt, "contradictory memory or earlier assistant claims") {
					t.Fatal("casual chat lost verified app facts or precedence over stale claims")
				}
				if calls == 1 {
					if !garminToolAvailable(request.Tools, "load_support_guide") {
						t.Fatal("casual follow-up cannot retrieve verified instructions")
					}
					if strings.Contains(prompt, "playlists") {
						for _, name := range []string{"login", "playlists"} {
							guide, _ := loadGarminSupportGuide(name)
							if !strings.Contains(request.SystemPrompt, guide) {
								t.Fatalf("reported setup request lacks %s guide", name)
							}
						}
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{
						{ID: "login", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "load_support_guide", Arguments: `{"name":"login"}`}},
						{ID: "playlists", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "load_support_guide", Arguments: `{"name":"playlists"}`}},
					}}}, nil
				}
				for i, name := range []string{"login", "playlists"} {
					guide, _ := loadGarminSupportGuide(name)
					message := request.Messages[len(request.Messages)-2+i]
					if message.Role != "tool" || message.Content != guide {
						t.Fatalf("casual %s retrieval lost full guide or safety boundaries", name)
					}
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "You're right, login is supported. Which app variant and platform are you using?"}}, nil
			})
			m := supportTestMessage(40)
			m.ChannelID = snowflakeID(garminBotsID)
			m.Content = "garmin, " + prompt
			messages := []cmd.GarminAIMessage{
				{Role: "user", Content: "how do i sync playlists with youtube music in Metrolist?"},
				{Role: "assistant", Content: "Metrolist doesn't do login."},
				{Role: "user", Content: prompt},
			}
			result, err := b.runGarminAI(context.Background(), nil, m, messages)
			if err != nil || result == nil || result.Silent || calls != 2 {
				t.Fatalf("casual guide lookup failed: %+v, %v", result, err)
			}
			if after, err := memory.Read(); err != nil || after != before {
				t.Fatal("answer generation rewrote shared memory")
			}
		})
	}
}
