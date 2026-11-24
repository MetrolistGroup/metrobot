package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
)

// garminToolResultError reads the model-facing error message of a tool result in tests.
func garminToolResultError(output string) string {
	var result struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(output), &result)
	return strings.TrimSpace(result.Error)
}

func TestGarminCommunityToolPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := garminHistoryTestSession(t, func(request *http.Request) (string, error) {
		cancel()
		if request.Context().Err() == nil {
			t.Error("community history request did not inherit cancellation")
		}
		return `[]`, nil
	})
	b := &Bot{Config: testConfig(config.Config{})}
	b.Config.Discord.AI.ReadableChannels = map[string]string{"news": garminHistoryTestChannel}
	call := cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{Name: "read_community_channel", Arguments: `{"channel":"news"}`}}
	if output, _, _, failed := b.runGarminAITool(ctx, session, garminHistoryTestMessage(), call); !failed || !strings.Contains(garminToolResultError(output), "canceled") {
		t.Fatalf("canceled community read = %q failed=%v", output, failed)
	}
}

func TestGarminRememberStopsAfterCancellationDuringPermissionCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	authority := testGuildAuthority{guildID: "12", ownerID: "99", roles: []testRole{{id: "80", permissions: discord.PermissionManageGuild}}, members: map[string][]string{"57": {"80"}}}
	session := mustTestClient(t)
	session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		if response, ok := authority.response(request); ok {
			return response, nil
		}
		t.Fatalf("unexpected request %s", request.URL)
		return nil, nil
	})})
	b := &Bot{Config: testConfig(config.Config{DiscordGuildID: "12"}), garminMemory: memory}
	m := &discord.Message{ID: 100, ChannelID: 200, GuildID: snowflakePtr("12"), Author: discord.User{ID: 57}, Content: "garmin, remember that Metrolist is open source"}
	call := cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{Name: "remember", Arguments: `{"content":"Metrolist is open source."}`}}
	if output, _, updated, failed := b.runGarminAITool(ctx, session, m, call); updated || !failed || !strings.Contains(garminToolResultError(output), "canceled") {
		t.Fatalf("canceled memory update = %q updated=%v failed=%v", output, updated, failed)
	}
	if saved, err := memory.Read(); err != nil || strings.Contains(saved, "Metrolist is open source") {
		t.Fatalf("canceled request persisted memory: %q err=%v", saved, err)
	}
}

func TestNoteBodyShapedLikeToolErrorIsContent(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	const body = `{"error":"Sign in required"}`
	if err := b.Notes.AddNote("error-example", "An error response example", body); err != nil {
		t.Fatal(err)
	}
	m := supportTestMessage(100)
	call := func(name string) cmd.GarminAIToolCall {
		return cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{Name: "get_note", Arguments: mustJSON(map[string]string{"name": name})}}
	}
	if output, _, _, failed := b.runGarminAITool(context.Background(), client, m, call("error-example")); failed || output != body {
		t.Fatalf("valid note = %q failed=%v", output, failed)
	}
	if output, _, _, failed := b.runGarminAITool(context.Background(), client, m, call("missing")); !failed || garminToolResultError(output) == "" {
		t.Fatalf("missing note = %q failed=%v", output, failed)
	}
	if output, _, _, failed := b.runGarminAITool(context.Background(), client, m, cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{Name: "read_channel_history", Arguments: `{}`}}); !failed || !strings.Contains(garminToolResultError(output), "app-support") {
		t.Fatalf("support history policy = %q failed=%v", output, failed)
	}

	const answer = "That note shows the sign-in error example."
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		for _, message := range request.Messages {
			if message.Role == "tool" && message.Content == body {
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
			}
		}
		return garminHistoryTestCall("get_note", `{"name":"error-example"}`), nil
	})
	m.Content = "what does the error example note say?"
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{garminAIUserMessage(m, m.Content)})
	sent.mu.Lock()
	defer sent.mu.Unlock()
	if len(sent.bodies) != 1 || !strings.Contains(sent.bodies[0], answer) {
		t.Fatalf("retrieved note was treated as a failed lookup: %v", sent.bodies)
	}
}
