package discord

import (
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestGarminPromptBudget(t *testing.T) {
	chat, err := (&Bot{}).garminSystemPromptWithMemory("", false)
	if err != nil {
		t.Fatal(err)
	}
	support, err := loadGarminSupportAnswerSkill()
	if err != nil {
		t.Fatal(err)
	}
	supportBytes := len(garminAppSupportSystemPrompt) + len(cmd.GarminReplyStyle) + len(support)
	// Baseline measured before this change with this same empty-memory prompt fixture.
	const beforeChat = 16566
	const beforeSupport = 14078
	if len(chat) >= beforeChat*2/3 || supportBytes >= beforeSupport*2/3 {
		t.Fatalf("prompt budget regression: chat=%d support=%d", len(chat), supportBytes)
	}
	// A representative text exchange has 160 bytes/message and uses no research tools.
	// These count prompt/history text, not tool schemas, provider wrappers, images or routing/checker calls.
	estimate := func(bytes int) int { return (bytes + 3) / 4 }
	t.Logf("prompt bytes: base=%d chat=%d -> %d support=%d -> %d evidence=%d; estimated prompt tokens: chat=%d -> %d support=%d -> %d", len(cmd.GarminSystemPrompt()), beforeChat, len(chat), beforeSupport, supportBytes, len(supportAnswerEvidenceInstructions), estimate(beforeChat), estimate(len(chat)), estimate(beforeSupport), estimate(supportBytes))
	t.Logf("representative generation input including retained 160-byte messages: chat=%d -> %d support=%d -> %d estimated tokens", estimate(beforeChat+30*160), estimate(len(chat)+garminAIConversationWindow*160), estimate(beforeSupport+30*160), estimate(supportBytes+garminAIConversationWindow*160))
	if garminAIConversationWindow != 12 || garminAIImageMessageWindow != 4 || garminSupportMaxToolRounds != 4 || supportMaxRetries != 1 || garminAIMaxToolRounds > 4 {
		t.Fatal("call budget regression")
	}
}

func TestGarminToolResultCompactionPreservesOriginalEvidence(t *testing.T) {
	body := strings.Repeat("source ", 1024)
	static := strings.Repeat("static ", 1024)
	messages := []cmd.GarminAIMessage{{Role: "tool", ToolCallID: "first", Content: body}, {Role: "tool", ToolCallID: "second", Content: body}, {Role: "tool", ToolCallID: "static", Content: static}}
	compact := compactGarminToolResults(messages, static)
	if compact[0].Content != body || !strings.Contains(compact[1].Content, "first") || !strings.Contains(compact[2].Content, "system prompt") {
		t.Fatal("missing reference", compact)
	}
	if messages[1].Content != body || messages[2].Content != static {
		t.Fatal("original evidence changed")
	}
	if len(compact[0].Content)+len(compact[1].Content)+len(compact[2].Content) >= len(body)*2 {
		t.Fatal("repeated evidence did not shrink")
	}
}
