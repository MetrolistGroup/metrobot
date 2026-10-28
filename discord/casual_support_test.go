package discord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestNonSupportChannelsUseOnlyAnsweringModel(t *testing.T) {
	for _, channel := range []string{garminGeneralID, garminBotsID, garminChatThreadID, "5"} {
		for _, prompt := range []string{"hello", "How do I sign into Metrolist?", "yes it does"} {
			t.Run(channel+"/"+prompt, func(t *testing.T) {
				calls := 0
				b := lifecycleBot(garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					calls++
					if strings.HasPrefix(request.SystemPrompt, garminAppSupportSystemPrompt) || !strings.Contains(request.SystemPrompt, "# Metrolist project reference") {
						t.Fatal("ordinary chat lost its normal prompt or project facts")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Qwen answer."}}, nil
				}))
				b.garminDecisions = supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
					t.Error("non-support chat called Decider")
					return nil, errors.New("Decider unavailable")
				})
				m := lifecycleMessage("100")
				m.ChannelID = snowflakeID(channel)
				m.Content = "garmin, " + prompt
				messages := []cmd.GarminAIMessage{{Role: "user", Content: "Does Metrolist support login?"}, {Role: "assistant", Content: "Login is unsupported."}, {Role: "user", Content: prompt}}
				result, err := b.runGarminAI(context.Background(), nil, m, messages)
				if err != nil || result == nil || result.Silent || result.Answer != "Qwen answer." || calls != 1 {
					t.Fatalf("result=%+v err=%v model calls=%d", result, err, calls)
				}
			})
		}
	}
}

func TestNonSupportCompactionSkipsDecider(t *testing.T) {
	sends, summaries, answers := 0, 0, 0
	client := lifecycleClient(t, func() (string, error) {
		sends++
		return `{"id":"300","channel_id":"5"}`, nil
	})
	b := lifecycleBot(garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if request.SystemPrompt == garminAIConversationSummaryPrompt {
			summaries++
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: `{"summary":"The user asked about Metrolist login."}`}}, nil
		}
		answers++
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Login is supported."}}, nil
	}))
	b.garminDecisions = supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		t.Fatal("non-support compaction or generation called Decider")
		return nil, nil
	})
	messages := make([]cmd.GarminAIMessage, garminAICompactionMessages+1)
	for i := range messages {
		messages[i] = cmd.GarminAIMessage{Role: "user", Content: "Metrolist login"}
	}
	m := lifecycleMessage("100")
	m.Content = "garmin, explain login"
	b.handleGarminAI(client, m, messages)
	if summaries != 1 || answers != 1 || sends != 1 {
		t.Fatalf("summaries=%d answers=%d sends=%d", summaries, answers, sends)
	}
}

func TestDeciderEntrypointsIgnoreOtherChannels(t *testing.T) {
	b, client, _ := newSupportDecisionTestBot(t)
	b.garminDecisions = supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		t.Fatal("Decider called outside app-support")
		return nil, nil
	})
	for _, channel := range []string{garminGeneralID, garminBotsID, garminChatThreadID, "5"} {
		m := supportTestMessage(20)
		m.ChannelID = snowflakeID(channel)
		m.Content = "That fixed it, thanks!"
		messages := []cmd.GarminAIMessage{{Role: "user", Name: garminAIMessageName(m.Author.ID.String()), Content: "Playback fails"}, {Role: "assistant", Content: "Try the approved playback checks."}, {Role: "user", Content: m.Content}}
		b.handleGarminSupportDecision(client, m, messages)
		b.recordConfirmedSupportFix(context.Background(), m, messages)
		note := "playback"
		accepted, hint, err := b.supportAnswerGrounded(context.Background(), m, messages, messages, "Qwen answer.", &note)
		if !accepted || hint != "" || err != nil || note != "" {
			t.Fatalf("non-support check: accepted=%v hint=%q err=%v note=%q", accepted, hint, err, note)
		}
	}
}
