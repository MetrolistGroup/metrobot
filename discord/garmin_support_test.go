package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const testKMPNote = `## What is KMP?

KMP stands for Kotlin Multiplatform.

## What platforms will be supported?

* Linux
* macOS
* Windows
* Android

## When will Metrolist-KMP be released?

There is currently no fixed release date.`

func TestGarminAppSupportTriageUsesModelActions(t *testing.T) {
	for _, test := range []struct {
		name  string
		calls []cmd.GarminAIToolCall
		want  bool
	}{
		{name: "handoff", calls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: "handoff_to_app_support_agent"}}}, want: true},
		{name: "ignore", calls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: "do_not_respond"}}}},
		{name: "fail closed on text"},
		{name: "ignore wins", calls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: "handoff_to_app_support_agent"}}, {Function: cmd.GarminAIFunctionCall{Name: "do_not_respond"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bot := &Bot{}
			bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if !strings.Contains(request.SystemPrompt, "keyword by itself is never enough") || !strings.Contains(request.SystemPrompt, "donation-proof") || !strings.Contains(request.SystemPrompt, "staff coordination") {
					t.Fatalf("triage prompt does not cover observed false replies: %q", request.SystemPrompt)
				}
				if got, want := garminToolNames(request.Tools), []string{"handoff_to_app_support_agent", "do_not_respond"}; !reflect.DeepEqual(got, want) || request.ToolChoice != "required" {
					t.Fatalf("triage tools = %v with choice %q, want %v with required choice", got, request.ToolChoice, want)
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "should never be sent", ToolCalls: test.calls}}, nil
			})
			message := &discordgo.MessageCreate{Message: &discordgo.Message{
				ID: "20", GuildID: "guild", ChannelID: garminAppSupportID, Content: "insane",
				Author: &discordgo.User{ID: "user", Username: "user"}, Member: &discordgo.Member{},
			}}
			got, err := bot.runGarminAppSupportTriage(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "insane"}})
			if err != nil || got != test.want {
				t.Fatalf("triage = %v, %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestOnMessageCreateTriagesAppSupportWithoutKeywordGate(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	requests := make(chan cmd.GarminAIRequest, 1)
	bot := &Bot{
		Config: &config.Config{DiscordGuildID: "guild"}, DB: database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(),
		garminProcessor: cmd.NewGarminProcessor(), garminAIContexts: make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext), garminContextCutoffs: make(map[string]string),
		garminAIRequests: make(map[string]map[string]context.CancelFunc),
	}
	bot.garminAIContexts["10"] = garminAIContext{
		userID: "user", guildID: "guild", channelID: garminAppSupportID, expiresAt: time.Now().Add(time.Hour),
		messages: []cmd.GarminAIMessage{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "earlier reply"}},
	}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		requests <- request
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
			Function: cmd.GarminAIFunctionCall{Name: "do_not_respond", Arguments: `{}`},
		}}}}, nil
	})

	bot.onMessageCreate(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "20", GuildID: "guild", ChannelID: garminAppSupportID, Content: "insane",
		Author: &discordgo.User{ID: "user"}, MessageReference: &discordgo.MessageReference{MessageID: "10"},
	}})
	select {
	case request := <-requests:
		if request.SystemPrompt != garminAppSupportTriagePrompt || len(request.Messages) != 3 || request.Messages[2].Content != "insane" {
			t.Fatalf("app-support reply was not triaged with its conversation: %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("keyword-free app-support message was not sent to Qwen triage")
	}
}

func TestAutomaticAppSupportHandoffRunsSupportAgent(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}

	var reply string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/typing"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode reply: %v", err)
			}
			reply = body.Content
			_, _ = w.Write([]byte(`{"id":"reply","channel_id":"` + garminAppSupportID + `"}`))
		default:
			t.Errorf("unexpected Discord request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{{ID: "guild", Name: "@everyone"}}}); err != nil {
		t.Fatal(err)
	}
	if err := session.State.ChannelAdd(&discordgo.Channel{ID: garminAppSupportID, GuildID: "guild", Name: "app-support"}); err != nil {
		t.Fatal(err)
	}

	calls := 0
	bot := &Bot{
		DB: database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(), garminMemory: memory,
		garminAILastUsed: make(map[string]time.Time), garminAIContexts: make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext), garminContextCutoffs: make(map[string]string),
		garminAIRequests: make(map[string]map[string]context.CancelFunc), garminAISlots: make(chan struct{}, 3),
	}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if request.SystemPrompt == garminAppSupportTriagePrompt {
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: "handoff_to_app_support_agent", Arguments: `{}`}}}}}, nil
		}
		if !strings.Contains(request.SystemPrompt, "# Metrolist Support Triage") || !strings.Contains(request.Context, "dedicated Metrolist app-support agent") {
			t.Fatal("handoff did not reach the constrained support agent")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "the support answer"}}, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "20", GuildID: "guild", ChannelID: garminAppSupportID, Content: "playback pauses after two songs",
		Author: &discordgo.User{ID: "user", Username: "user"}, Member: &discordgo.Member{},
	}}
	bot.handleGarminAutomaticAppSupport(session, message, []cmd.GarminAIMessage{garminAIUserMessage(message, message.Content)})
	if calls != 2 || reply != "the support answer" {
		t.Fatalf("handoff made %d model calls and replied %q", calls, reply)
	}
}

func TestAppSupportAgentReturnsExactNoteForPlatformRenderer(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote("kmp", "Kotlin Multiplatform FAQ", testKMPNote); err != nil {
		t.Fatal(err)
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}, garminMemory: memory}
	bot.garminAI = garminAITestFunc(func(_ context.Context, _ cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
			ID: "note", Function: cmd.GarminAIFunctionCall{Name: "get_note", Arguments: `{"name":"kmp"}`},
		}}}}, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "20", ChannelID: garminAppSupportID, Author: &discordgo.User{ID: "user"}}}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "what is KMP?"}})
	if err != nil || result.NoteName != "kmp" || result.Answer != testKMPNote {
		t.Fatalf("support note result = %#v, %v", result, err)
	}
}

func TestKMPNoteUsesCompactComponentsV2(t *testing.T) {
	components, ok := kmpNoteComponents(testKMPNote, 1)
	if !ok || len(components) != 1 {
		t.Fatalf("KMP components = %#v, %v", components, ok)
	}
	container, ok := components[0].(discordgo.Container)
	if !ok || len(container.Components) != 4 {
		t.Fatalf("KMP container = %#v", components[0])
	}
	answer, ok := container.Components[2].(discordgo.TextDisplay)
	if !ok || !strings.Contains(answer.Content, "Linux") || strings.Contains(answer.Content, "fixed release date") {
		t.Fatalf("selected KMP answer = %#v", container.Components[2])
	}

	var payload struct {
		Content    string            `json:"content"`
		Flags      int               `json:"flags"`
		Components []json.RawMessage `json:"components"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode KMP message: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","channel_id":"support"}`))
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	if bot.sendKMPNoteReply(session, "support", "message", testKMPNote) == nil {
		t.Fatal("KMP note was not sent")
	}
	if payload.Content != "" || payload.Flags&int(discordgo.MessageFlagsIsComponentsV2) == 0 || len(payload.Components) != 1 {
		t.Fatalf("KMP message payload = %#v", payload)
	}
}

func TestSendGarminAppSupportReplyPreservesLongAnswerAsAttachment(t *testing.T) {
	content := strings.Repeat("exact-note-content\n", 150)
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading long support reply: %v", err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","channel_id":"support"}`))
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", ChannelID: garminAppSupportID}}
	if reply := bot.sendGarminAppSupportReply(session, message, content, ""); reply == nil {
		t.Fatal("long app support answer was not sent")
	}
	if !strings.Contains(requestBody, "app-support-note.md") || !strings.Contains(requestBody, content) {
		t.Fatal("long app support answer was not preserved in the attachment")
	}
}
