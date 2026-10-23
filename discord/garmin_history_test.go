package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/firecrawl"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

const garminHistoryTestChannel = "100000000000000005"

func garminHistoryTestMessage() *discord.Message {
	return &discord.Message{
		ID: snowflakeID("1000"), ChannelID: snowflakeID(garminHistoryTestChannel), GuildID: snowflakePtr("100000000000000012"),
		Author: discord.User{ID: snowflakeID("100000000000000056"), Username: "speaker"}, Member: &discord.Member{},
		Content: "garmin, summarize the last 100 messages",
	}
}

func garminHistoryTestMessages(count int, content string) []map[string]any {
	messages := make([]map[string]any, 0, count)
	for i := range count {
		messages = append(messages, map[string]any{
			"id": strconv.Itoa(999 - i), "channel_id": garminHistoryTestChannel,
			"timestamp": "2026-10-23T12:00:00Z", "content": fmt.Sprintf("%d: %s", i, content),
			"author": map[string]any{"id": strconv.Itoa(100000 + i), "username": fmt.Sprintf("author_%d", i), "global_name": "Do not use this name"},
		})
	}
	return messages
}

func garminHistoryTestSession(t *testing.T, handler func(*http.Request) (string, error)) *bot.Client {
	t.Helper()
	session := mustTestClient(t)
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{
		Guild: discord.Guild{ID: snowflakeID("100000000000000012")},
		Roles: []discord.Role{{ID: snowflakeID("42"), Name: "they/them"}},
	}, Members: []discord.Member{{User: discord.User{ID: snowflakeID("100000"), Username: "cached"}, Nick: ptr("Cached Nick"), RoleIDs: []snowflake.ID{snowflakeID("42")}}}})
	session.Caches.AddChannel(testChannel(t, map[string]any{"id": garminHistoryTestChannel, "guild_id": "100000000000000012", "name": "chat"}))
	session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/channels/"+garminHistoryTestChannel+"/messages") {
			t.Fatalf("unexpected REST lookup (or identity/attachment fan-out): %s %s", request.Method, request.URL.Path)
		}
		body, err := handler(request)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})
	return session
}

func garminHistoryTestCall(name, args string) *cmd.GarminAICompletion {
	return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
		ID: name, Type: "function", Function: cmd.GarminAIFunctionCall{Name: name, Arguments: args},
	}}}}
}

func TestGarminHistoryPasses100MessagesToCompletion(t *testing.T) {
	messages := garminHistoryTestMessages(100, strings.Repeat("context ", 30))
	messages[0]["message_reference"] = map[string]any{"message_id": "998", "channel_id": garminHistoryTestChannel}
	messages[0]["attachments"] = []map[string]any{{"filename": "picture.png", "content_type": "image/png", "url": "https://cdn.example/picture.png"}}
	messages[1]["member"] = map[string]any{"nick": "Embedded Nick", "roles": []string{"42"}}
	restCalls := 0
	session := garminHistoryTestSession(t, func(request *http.Request) (string, error) {
		restCalls++
		if request.URL.Query().Get("before") != "1000" {
			t.Fatalf("history cutoff changed: %s", request.URL.RawQuery)
		}
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		if limit != 20 && limit != 100 {
			t.Fatalf("unexpected fetch limit: %d", limit)
		}
		return mustJSON(messages[:limit]), nil
	})
	b := &Bot{garminFirecrawl: firecrawl.NewClient([]string{"fake"})}
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if !garminToolAvailable(request.Tools, "read_channel_history") || !garminToolAvailable(request.Tools, "search_web") {
			t.Fatalf("optional tools missing: %v", garminToolNames(request.Tools))
		}
		if calls == 1 {
			if len(request.Messages) != 1 {
				t.Fatal("channel summary triggered a public search preflight")
			}
			return garminHistoryTestCall("read_channel_history", `{"limit":100,"channel":"other","channel_id":"999999","before_id":"2000","message_id":"2000"}`), nil
		}
		output := request.Messages[len(request.Messages)-1].Content
		var history struct {
			Messages []struct {
				ID        string `json:"id"`
				Timestamp string `json:"timestamp"`
				ReplyTo   string `json:"reply_to_message_id"`
			} `json:"messages"`
			Participants map[string]map[string]any `json:"participants"`
			Fetched      int                       `json:"fetched_count"`
			Returned     int                       `json:"returned_count"`
			Truncated    bool                      `json:"truncated"`
		}
		if err := json.Unmarshal([]byte(output), &history); err != nil {
			t.Fatalf("history JSON was sliced: %v", err)
		}
		if len(output) <= garminAIToolResultSize || len(output) > garminAIHistoryResultSize || len(history.Messages) != 100 || history.Fetched != 100 || history.Returned != 100 || history.Truncated {
			t.Fatalf("100-message coverage: bytes=%d, fetched=%d, returned=%d, truncated=%v", len(output), history.Fetched, history.Returned, history.Truncated)
		}
		if history.Messages[0].ID != "900" || history.Messages[99].ID != "999" || history.Messages[99].ReplyTo != "998" || history.Messages[0].Timestamp != "2026-10-23T12:00:00Z" {
			t.Fatalf("chronology or reply linkage lost: %#v", history.Messages)
		}
		if len(history.Participants) != 100 || history.Participants["100000"]["display_name"] != "Cached Nick" || history.Participants["100001"]["display_name"] != "Embedded Nick" || history.Participants["100002"]["display_name"] != "author_2" || strings.Contains(output, "Do not use this name") {
			t.Fatalf("authoritative participant identities lost: %#v", history.Participants)
		}
		if _, exists := history.Participants["100002"]["roles"]; exists {
			t.Fatal("roles guessed for an uncached author")
		}
		for _, message := range request.Messages {
			if len(message.Images) != 0 {
				t.Fatal("history attachment downloaded or attached as visual input")
			}
		}
		if !request.DisableReasoning {
			t.Fatal("retrieval synthesis did not disable reasoning")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "summary grounded in 100 messages"}}, nil
	})
	message := garminHistoryTestMessage()
	result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{garminAIUserMessage(message, "summarize the last 100 messages")})
	if err != nil || result.Answer != "summary grounded in 100 messages" || calls != 2 || restCalls != 2 {
		t.Fatalf("history run = %#v, error=%v, AI=%d, REST=%d", result, err, calls, restCalls)
	}
}

func TestGarminHistoryBoundsResetFilteringAndPartialCoverage(t *testing.T) {
	for _, test := range []struct {
		limit, fetch, returned int
		query                  string
	}{
		{1000, 100, 2, ""}, {0, 15, 2, ""}, {-1, 15, 2, ""}, {100, 100, 1, "KEEP"}, {1, 100, 1, "keep"},
	} {
		t.Run(fmt.Sprintf("%d/%s", test.limit, test.query), func(t *testing.T) {
			messages := garminHistoryTestMessages(7, "keep")
			messages[0]["id"] = "1001"
			messages[1]["id"] = "1000"
			messages[2]["id"] = "999"
			messages[3]["id"] = "998"
			messages[3]["channel_id"] = "100000000000000006"
			messages[3]["content"] = "cross-channel secret"
			messages[4]["id"] = "997"
			messages[4]["content"] = "another topic"
			messages[5]["id"] = "995"
			messages[5]["content"] = "pre-reset secret"
			messages[6]["id"] = "994"
			session := garminHistoryTestSession(t, func(request *http.Request) (string, error) {
				if request.URL.Query().Get("limit") != strconv.Itoa(test.fetch) || request.URL.Query().Get("before") != "1000" {
					t.Fatalf("unbounded request: %s", request.URL.RawQuery)
				}
				return mustJSON(messages), nil
			})
			b := &Bot{garminContextCutoffs: map[string]string{garminHistoryTestChannel: "995"}}
			output, _, _ := b.executeGarminAITool(context.Background(), session, garminHistoryTestMessage(), garminHistoryTestCall("read_channel_history", mustJSON(map[string]any{"limit": test.limit, "query": test.query})).Message.ToolCalls[0])
			var history struct {
				Messages  []map[string]any `json:"messages"`
				Fetched   int              `json:"fetched_count"`
				Returned  int              `json:"returned_count"`
				Truncated bool             `json:"truncated"`
				Fewer     bool             `json:"fewer_than_requested"`
			}
			if err := json.Unmarshal([]byte(output), &history); err != nil || history.Fetched != 7 || history.Returned != test.returned || len(history.Messages) != test.returned || history.Truncated {
				t.Fatalf("partial history = %s, error=%v", output, err)
			}
			if test.limit != 1 && !history.Fewer {
				t.Fatal("insufficient/deleted messages reported as complete coverage")
			}
			if strings.Contains(output, "secret") || strings.Contains(output, `"id":"1000"`) || strings.Contains(output, `"id":"1001"`) || strings.Contains(output, `"id":"994"`) {
				t.Fatalf("history boundary leaked: %s", output)
			}
		})
	}
}

func TestGarminHistoryOversizeStaysBoundedValidJSON(t *testing.T) {
	messages := garminHistoryTestMessages(100, strings.Repeat("é", 6000))
	roles := make([]string, 40)
	for i := range roles {
		roles[i] = strconv.Itoa(i + 1)
	}
	messages[0]["member"] = map[string]any{"nick": strings.Repeat("n", 1000), "roles": roles}
	attachments := make([]map[string]any, 15)
	for i := range attachments {
		attachments[i] = map[string]any{"filename": strings.Repeat("f", 1000), "url": "https://cdn.example/" + strings.Repeat("u", 3000), "content_type": strings.Repeat("t", 300)}
	}
	messages[0]["attachments"] = attachments
	session := garminHistoryTestSession(t, func(*http.Request) (string, error) { return mustJSON(messages), nil })
	output, _, _ := (&Bot{}).executeGarminAITool(context.Background(), session, garminHistoryTestMessage(), garminHistoryTestCall("read_channel_history", `{"limit":100}`).Message.ToolCalls[0])
	output = garminAIToolResult("read_channel_history", output)
	var history struct {
		Messages []struct {
			Content     string              `json:"content"`
			Attachments []map[string]string `json:"attachments"`
		} `json:"messages"`
		Participants map[string]map[string]any `json:"participants"`
		Fetched      int                       `json:"fetched_count"`
		Returned     int                       `json:"returned_count"`
		Omitted      int                       `json:"omitted_count"`
		Fields       bool                      `json:"fields_truncated"`
		Truncated    bool                      `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(output), &history); err != nil || !utf8.ValidString(output) || len(output) > garminAIHistoryResultSize || !history.Truncated || !history.Fields || history.Fetched != 100 || history.Returned < 1 || history.Returned >= 100 || history.Returned+history.Omitted != 100 {
		t.Fatalf("oversize coverage: bytes=%d, returned=%d, omitted=%d, fields=%v, truncated=%v, error=%v", len(output), history.Returned, history.Omitted, history.Fields, history.Truncated, err)
	}
	last := history.Messages[len(history.Messages)-1]
	if utf8.RuneCountInString(last.Content) > 4000 || len(last.Attachments) != 10 || utf8.RuneCountInString(last.Attachments[0]["filename"]) > 256 || utf8.RuneCountInString(last.Attachments[0]["url"]) > 2048 {
		t.Fatalf("unbounded message metadata: %#v", last)
	}
	identity := history.Participants["100000"]
	if utf8.RuneCountInString(identity["display_name"].(string)) > 128 || len(identity["roles"].([]any)) != 25 {
		t.Fatalf("unbounded participant metadata: %#v", identity)
	}
}

func TestGarminHistoryCancellationAndResetSuppressInflightResult(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	b := &Bot{DB: database}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	message := garminHistoryTestMessage()
	if !b.registerGarminAIRequest(message, cancel) {
		t.Fatal("request registration failed")
	}
	defer b.unregisterGarminAIRequest(message)
	session := garminHistoryTestSession(t, func(request *http.Request) (string, error) {
		if err := b.resetGarminContext(garminHistoryTestChannel, "1001"); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(request.Context().Err(), context.Canceled) {
			t.Fatal("Discord history request did not inherit cancellation")
		}
		return mustJSON(garminHistoryTestMessages(1, "must not reach completion")), nil
	})
	visible, output, _, _ := b.executeGarminAIToolIfVisible(ctx, session, message, garminHistoryTestCall("read_channel_history", `{"limit":100}`).Message.ToolCalls[0])
	if visible || output != "" {
		t.Fatalf("reset history escaped suppression: visible=%v output=%s", visible, output)
	}
	if _, err := b.readGarminChannelMessagesContext(ctx, nil, garminHistoryTestChannel, "1000", "", 100, garminAIHistoryResultSize); !errors.Is(err, context.Canceled) {
		t.Fatalf("already-canceled read = %v", err)
	}
}

func TestGarminHistoryErrorsRemainToolData(t *testing.T) {
	for _, test := range []struct{ args, body, want string }{
		{`{"limit":100}`, `[{"id":`, "reading"},
		{mustJSON(map[string]string{"query": strings.Repeat("x", 501)}), `[]`, "500 characters"},
	} {
		session := garminHistoryTestSession(t, func(*http.Request) (string, error) { return test.body, nil })
		output, _, _ := (&Bot{}).executeGarminAITool(context.Background(), session, garminHistoryTestMessage(), garminHistoryTestCall("read_channel_history", test.args).Message.ToolCalls[0])
		if !json.Valid([]byte(output)) || !strings.Contains(garminToolResultError(output), test.want) {
			t.Fatalf("history failure = %s", output)
		}
	}
}

func TestGarminOptionalSearchAndHistoryChaining(t *testing.T) {
	for _, sequence := range [][]string{{"read_channel_history", "search_web"}, {"search_web", "search_web"}, {"search_web", "read_channel_history"}, {"read_channel_history", "search_web", "search_web"}} {
		t.Run(strings.Join(sequence, "/"), func(t *testing.T) {
			searches := 0
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				searches++
				var args garminToolArgs
				if err := json.NewDecoder(request.Body).Decode(&args); err != nil || args.Query != "quark etymology" {
					t.Fatalf("autonomous search args=%#v, error=%v", args, err)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"web":[{"url":"https://example.org/quark","markdown":"A sourced explanation"}]}}`))}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) { return mustJSON(garminHistoryTestMessages(2, "discussion")), nil })
			b := &Bot{garminFirecrawl: firecrawl.NewClient([]string{"fake"})}
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if calls == 0 && len(request.Messages) != 1 {
					t.Fatal("unfamiliar subject was forcibly searched before the model could choose")
				}
				if calls == garminAIMaxToolRounds-1 {
					if len(request.Tools) != 0 {
						t.Fatal("last round must synthesize within the existing round budget")
					}
				} else if !garminToolAvailable(request.Tools, "search_web") || !garminToolAvailable(request.Tools, "read_channel_history") {
					t.Fatalf("optional tools removed too early: %v", garminToolNames(request.Tools))
				}
				if calls > 0 && (!request.DisableReasoning || request.Messages[len(request.Messages)-1].Role != "tool") {
					t.Fatal("tool result not supplied for concise synthesis/chaining")
				}
				calls++
				if calls <= len(sequence) {
					return garminHistoryTestCall(sequence[calls-1], `{"query":"quark etymology","limit":2}`), nil
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "a sourced answer"}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, who coined the term quark?"
			result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{garminAIUserMessage(message, "who coined the term quark?")})
			wantSearches := 0
			for _, tool := range sequence {
				if tool == "search_web" {
					wantSearches++
				}
			}
			if err != nil || calls != len(sequence)+1 || searches != wantSearches || result.Answer != "a sourced answer" || result.ToolCalls != len(sequence) {
				t.Fatalf("chained result=%#v, error=%v, completions=%d, searches=%d", result, err, calls, searches)
			}
		})
	}
}

func TestGarminSearchAvailabilityDoesNotRequirePreflight(t *testing.T) {
	for _, prompt := range []string{"hey", "summarize the latest 100 messages", "find where we left off", "the latest joke was funny", "i saw some news today", "that image was funny", "what unfamiliar subject is that?"} {
		t.Run(prompt, func(t *testing.T) {
			b := &Bot{garminFirecrawl: firecrawl.NewClient([]string{"fake"})}
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if len(request.Messages) != 1 || !garminToolAvailable(request.Tools, "search_web") || !garminToolAvailable(request.Tools, "read_channel_history") {
					t.Fatalf("unnecessary preflight or missing optional tool: %#v", request)
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "short answer"}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			if result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}}); err != nil || result.ToolCalls != 0 {
				t.Fatalf("optional run=%#v, error=%v", result, err)
			}
		})
	}
}

func TestGarminAmbientHasOptionalRetrievalAndSupportDoesNot(t *testing.T) {
	for _, support := range []bool{false, true} {
		b := &Bot{garminFirecrawl: firecrawl.NewClient([]string{"fake"})}
		message := garminHistoryTestMessage()
		message.Content = "maybe never mind"
		if support {
			message.ChannelID = snowflakeID(garminAppSupportID)
		}
		b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			if support {
				if got, want := garminToolNames(request.Tools), []string{"do_not_respond", "search_metrolist_issues", "list_notes", "get_note"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("support tool scope expanded: %v", got)
				}
			} else if !garminToolAvailable(request.Tools, "search_web") || !garminToolAvailable(request.Tools, "read_channel_history") {
				t.Fatalf("ambient optional retrieval missing: %v", garminToolNames(request.Tools))
			}
			return garminHistoryTestCall("do_not_respond", `{}`), nil
		})
		var token uint64
		if !support {
			b.rememberGarminAIContext("previous", message, []cmd.GarminAIMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hey"}})
			token = b.tryBeginGarminAIAmbient(message)
			defer b.endGarminAIAmbient(message, token)
			if token == 0 {
				t.Fatal("eligible ambient conversation was not active")
			}
		}
		result, err := b.runGarminAIWithMode(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "maybe never mind"}}, !support, token)
		if err != nil || !result.Silent {
			t.Fatalf("ambient/support discovery=%#v, error=%v", result, err)
		}
	}
}

func TestGarminSupportRejectsUnadvertisedRetrieval(t *testing.T) {
	b := &Bot{garminFirecrawl: firecrawl.NewClient(nil)}
	message := garminHistoryTestMessage()
	message.ChannelID = snowflakeID(garminAppSupportID)
	message.Content = "maybe never mind"
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls == 1 {
			completion := garminHistoryTestCall("search_web", `{"query":"anything"}`)
			completion.Message.ToolCalls = append(completion.Message.ToolCalls, garminHistoryTestCall("read_channel_history", `{}`).Message.ToolCalls...)
			return completion, nil
		}
		for _, result := range request.Messages[len(request.Messages)-2:] {
			if result.Role != "tool" || !strings.Contains(garminToolResultError(result.Content), "unavailable for this request") {
				t.Fatalf("support executed an unadvertised tool: %#v", result)
			}
		}
		return garminHistoryTestCall("do_not_respond", `{}`), nil
	})
	result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "maybe never mind"}})
	if err != nil || calls != 2 || !result.Silent || result.ToolCalls != 1 {
		t.Fatalf("support retrieval escaped its tool scope: result=%#v, error=%v, calls=%d", result, err, calls)
	}
}

func TestGarminRuntimeRetrievalGuidanceSurvivesCustomPrompt(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetGarminSystemPrompt("A custom base prompt"); err != nil {
		t.Fatal(err)
	}
	b := &Bot{DB: database}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		for _, required := range []string{"A custom base prompt", "read_channel_history", "timestamps", "participants, developments, decisions and unanswered questions", "untrusted data", "unfamiliar subjects", "Do not preflight casual chat", "private channel text"} {
			if !strings.Contains(request.SystemPrompt, required) {
				t.Errorf("custom prompt lost runtime guidance %q", required)
			}
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "hey"}}, nil
	})
	if _, err := b.runGarminAI(context.Background(), nil, garminHistoryTestMessage(), []cmd.GarminAIMessage{{Role: "user", Content: "hey"}}); err != nil {
		t.Fatal(err)
	}
	if got := normalizeGarminAIAnswer(`read_channel_history limit=100`); strings.Contains(got, "read_channel_history") {
		t.Fatalf("history tool syntax leaked: %s", got)
	}
	if got := sanitizeGarminInternalToolDisclosure("what tool do you use?", "i use read_channel_history"); strings.Contains(got, "read_channel_history") {
		t.Fatalf("history tool disclosure leaked: %s", got)
	}
}
