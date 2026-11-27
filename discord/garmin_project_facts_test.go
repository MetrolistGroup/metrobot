package discord

import (
	"context"
	"os"
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
					if !garminToolAvailable(request.Tools, "load_support_guide") || !garminToolAvailable(request.Tools, "load_skill") {
						t.Fatal("casual follow-up cannot discover skills or verified instructions")
					}
					for _, name := range []string{"login", "playlists"} {
						guide, _ := loadGarminSupportGuide(name)
						if strings.Contains(request.SystemPrompt, guide) {
							t.Fatalf("setup wording preloaded %s guide", name)
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

func TestV14ProjectDetailsLoadOnDemand(t *testing.T) {
	reference, err := loadGarminSkill("metrolist")
	if err != nil {
		t.Fatal(err)
	}
	core, err := loadGarminProjectReference()
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"v14.0.0", "14 November 2026", "Android 7+", "iOS 17+", "Windows 10+", "ARM64 portable", "macOS 15+", "unsigned", "metrolist-desktop-bin", "com.metrolist.music", "Metrolist.apk", "No donation, tester role or source-access gate remains", "metrolist-installer-windows-x64-VERSION.exe", "F-Cast", "ListenBrainz", "metrolist-altstore.json", "SHA256SUMS"} {
		if !strings.Contains(reference, fact) {
			t.Errorf("missing v14 fact %q", fact)
		}
	}
	for _, old := range []string{"in maintenance mode", "com.metrolist.music.kmp", "MetrolistGroup/Metrolist-KMP", "requires the GMS", "restricted source", "No native ARM64"} {
		if strings.Contains(reference, old) {
			t.Errorf("old product split %q", old)
		}
	}
	if strings.Contains(core, "SHA256SUMS") || strings.Contains(core, "F-Cast") || !strings.Contains(core, "public") {
		t.Fatal("static prefix includes rare details or omits public status")
	}
}

func TestGarminKnowsBuiltInKudGamesOnFollowup(t *testing.T) {
	content, err := os.ReadFile("../garmin-memory.md")
	if err != nil {
		t.Fatal(err)
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Replace(string(content)); err != nil {
		t.Fatal(err)
	}
	b := &Bot{garminMemory: memory}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		for _, fact := range []string{"playable Kud games built into this Discord server", ">>plinko 500", "Every 50 Kud buys one independent ball", ">>kud", ">>mines", ">>highlow", ">>fight", ">>crash [bet]", ">>coins-crash [bet]", "15-second lobby", ">>loan @lender", "20%"} {
			if !strings.Contains(request.Context, fact) {
				t.Errorf("AI follow-up is missing economy fact %q", fact)
			}
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "yes, send `>>plinko` for one ball or `>>plinko 500` for ten."}}, nil
	})
	m := supportTestMessage(40)
	m.ChannelID = snowflakeID(garminBotsID)
	m.Content = "garmin, can i try?"
	messages := []cmd.GarminAIMessage{
		{Role: "user", Content: "Plinko?"},
		{Role: "assistant", Content: "no plinko board built in"},
		{Role: "user", Content: "can i try?"},
	}
	result, err := b.runGarminAI(context.Background(), nil, m, messages)
	if err != nil || result == nil || result.Silent || !strings.Contains(result.Answer, ">>plinko") {
		t.Fatalf("game follow-up failed: %+v, %v", result, err)
	}
}
