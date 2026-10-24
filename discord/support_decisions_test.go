package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type supportDecisionTestFunc func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error)

func (f supportDecisionTestFunc) Decide(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
	return f(ctx, request)
}

type supportTestDelivery struct {
	mu     sync.Mutex
	bodies []string
	fail   atomic.Bool
}

func (d *supportTestDelivery) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.bodies)
}

func newSupportDecisionTestBot(t *testing.T) (*Bot, *bot.Client, *supportTestDelivery) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	notes := &cmd.NotesHandler{DB: database}
	for name, description := range map[string]string{"nightly": "How to get nightly builds", "rpc": "How to enable Discord rich presence", "lyrics": "How to adjust lyric timing"} {
		if err := notes.AddNote(name, description, "Exact saved "+name+" instructions."); err != nil {
			t.Fatal(err)
		}
	}
	delivery := &supportTestDelivery{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/typing") {
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		data, _ := json.Marshal(body)
		delivery.mu.Lock()
		delivery.bodies = append(delivery.bodies, string(data))
		n := len(delivery.bodies)
		delivery.mu.Unlock()
		if delivery.fail.Load() {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"synthetic rejection","code":50013}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"%d","channel_id":"%s"}`, 1000+n, garminAppSupportID)
	}))
	t.Cleanup(server.Close)
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	cacheTestGuild(client, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: 12}, Roles: []discord.Role{{ID: 12, Name: "@everyone"}}}})
	client.Caches.AddChannel(testChannel(t, map[string]any{"id": garminAppSupportID, "guild_id": "12", "name": "app-support"}))
	b := &Bot{Config: &config.Config{DiscordGuildID: "12"}, DB: database, Notes: notes, Logger: zap.NewNop(), garminProcessor: cmd.NewGarminProcessor(), garminAISlots: make(chan struct{}, 3), garminAILastUsed: make(map[string]time.Time)}
	return b, client, delivery
}

func supportTestMessage(id int) *discord.Message {
	return &discord.Message{ID: snowflakeID(fmt.Sprint(id)), GuildID: snowflakePtr("12"), ChannelID: snowflakeID(garminAppSupportID), Author: discord.User{ID: 42, Username: "private-profile"}, Content: "How do I install the nightly build?"}
}

func supportTestDecision(request cmd.DecisionRequest, choice string, shouldAnswer, top float64) *cmd.DecisionResponse {
	options := request.Questions["route"].Criteria
	for id, criterion := range options {
		if strings.HasPrefix(criterion, "Saved note "+choice+":") {
			choice = id
			break
		}
	}
	probabilities := make(map[string]float64)
	for id := range options {
		probabilities[id] = (1 - top) / float64(len(options)-1)
	}
	probabilities[choice] = top
	data, _ := json.Marshal(map[string]any{
		"model": "perplexity/pplx-decider-flash-v1.2", "usage": map[string]any{"input_tokens": 100, "output_tokens": 1, "cost": 0.00000104},
		"answers": map[string]any{"should_answer": map[string]any{"type": "noul", "noul": shouldAnswer}, "route": map[string]any{"type": "choice", "choice": choice, "probabilities": probabilities}},
	})
	var response cmd.DecisionResponse
	_ = json.Unmarshal(data, &response)
	return &response
}

func TestSupportDecisionSyntheticFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/support-routing.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name         string  `json:"name"`
		Message      string  `json:"message"`
		ReplyTo      string  `json:"reply_to"`
		Route        string  `json:"route"`
		ShouldAnswer float64 `json:"should_answer"`
		Probability  float64 `json:"probability"`
		Expected     string  `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			m := supportTestMessage(20)
			m.Content = fixture.Message
			messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
			if fixture.ReplyTo != "" {
				m.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("10")}
				m.ReferencedMessage = &discord.Message{ID: 10, Author: discord.User{ID: 99}, Content: "Try the nightly build."}
				if fixture.ReplyTo == "bot" {
					m.ReferencedMessage.Author.ID = client.ID()
					messages = append([]cmd.GarminAIMessage{{Role: "assistant", Content: "Try the nightly build."}}, messages...)
					b.rememberGarminAIContext("10", m, messages[:1])
				}
			}
			calls := 0
			b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				t.Error("unexpected chat-model fallback")
				return nil, errors.New("no fallback")
			})
			b.garminDecisions = supportDecisionTestFunc(func(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				calls++
				if request.Questions["should_answer"].Type != "noul" || request.Questions["route"].Type != "choice" || strings.Contains(request.Questions["route"].Instructions, "should_answer") {
					t.Error("questions must be independent Noul and Choice evaluations")
				}
				for _, exclusion := range []string{"chatter", "thanks", "advice to others", "staff coordination", "human-directed", "donation proof"} {
					if !strings.Contains(request.Questions["should_answer"].Instructions, exclusion) || !strings.Contains(request.Questions["route"].Instructions, exclusion) {
						t.Errorf("missing routing policy: %s", exclusion)
					}
				}
				state, _ := json.Marshal(request.State)
				if strings.Contains(string(state), "private-profile") || strings.Contains(string(state), "discord_42") {
					t.Error("profile leaked to decision state")
				}
				return supportTestDecision(request, fixture.Route, fixture.ShouldAnswer, fixture.Probability), nil
			})
			b.handleGarminAutomaticAppSupport(client, m, messages)
			want := 0
			if fixture.Expected == "note" {
				want = 1
			}
			if sent.count() != want || (fixture.Expected == "guard" && calls != 0) || (fixture.Expected != "guard" && calls != 1) {
				t.Fatalf("sends=%d want=%d calls=%d", sent.count(), want, calls)
			}
		})
	}
}

func TestSupportDecisionExactRenderingContinuationAndCooldown(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	if err := b.Notes.EditNote("nightly", "How to get nightly builds", "Intro\n```random\n[\"first\",\"second\"]\n```"); err != nil {
		t.Fatal(err)
	}
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return supportTestDecision(request, "nightly", .99, .98), nil
	})
	m := supportTestMessage(20)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
	b.handleGarminAutomaticAppSupport(client, m, messages)
	b.handleGarminAutomaticAppSupport(client, m, messages)
	b.handleGarminAutomaticAppSupport(client, supportTestMessage(21), messages)
	if sent.count() != 1 {
		t.Fatalf("dedup/cooldown sends=%d", sent.count())
	}
	context := b.garminAIContexts["1001"].messages
	var body struct {
		Content         string         `json:"content"`
		AllowedMentions map[string]any `json:"allowed_mentions"`
	}
	if err := json.Unmarshal([]byte(sent.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if len(context) != 2 || context[1].Content != body.Content || !strings.HasPrefix(body.Content, "Intro\n") || body.AllowedMentions == nil {
		t.Fatalf("delivered/stored note mismatch: %q %#v", body.Content, context)
	}
	followup := supportTestMessage(22)
	followup.Content = "Where do I find that?"
	followup.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("1001")}
	b.garminAI = garminAITestFunc(nil) // Continuation lookup only requires a configured agent.
	continued, ok := b.garminAIContinuation(followup, followup.Content)
	if !ok || len(continued) != 3 || continued[1].Content != body.Content {
		t.Fatalf("continuation=%#v %v", continued, ok)
	}
	b.supportMu.Lock()
	for key, state := range b.supportNotes {
		state.expiresAt = time.Now().Add(-time.Second)
		b.supportNotes[key] = state
	}
	b.supportMu.Unlock()
	b.handleGarminAutomaticAppSupport(client, supportTestMessage(23), messages)
	if sent.count() != 2 {
		t.Fatal("expired cooldown did not permit note")
	}
}

func TestSupportDecisionFailureCanRetryAndDoesNotFallback(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	core, observed := observer.New(zap.DebugLevel)
	b.Logger = zap.New(core)
	calls := 0
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		t.Error("chat fallback on outage")
		return nil, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("private-key https://private.example user body")
		}
		return supportTestDecision(request, "nightly", .99, .98), nil
	})
	m := supportTestMessage(20)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if sent.count() != 0 || len(b.supportSeen) != 0 {
		t.Fatal("outage sent/remembered a message")
	}
	for _, entry := range observed.All() {
		data, _ := json.Marshal(entry.ContextMap())
		if strings.Contains(string(data), "private") || strings.Contains(string(data), "user body") {
			t.Fatal("unsafe error logging")
		}
	}
	sent.fail.Store(true)
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if len(b.supportNotes) != 0 || len(b.supportSeen) != 0 {
		t.Fatal("failed send claimed cooldown or seen state")
	}
	sent.fail.Store(false)
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if sent.count() != 2 || len(b.supportNotes) != 1 || calls != 3 {
		t.Fatalf("retry sends=%d calls=%d cooldowns=%d", sent.count(), calls, len(b.supportNotes))
	}
}

func TestSupportDecisionLiveCatalogAndStaleSelection(t *testing.T) {
	for _, mutation := range []string{"delete", "rename", "manual-only", "description"} {
		t.Run(mutation, func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			if err := b.DB.AddNote("manual", "", "Never automatically send this"); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.AddNote("none", "An ordinary note named none", "Exact reserved-name note"); err != nil {
				t.Fatal(err)
			}
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				for id, criterion := range request.Questions["route"].Criteria {
					if strings.Contains(criterion, "Saved note manual:") || (strings.Contains(criterion, "Saved note none:") && id == "none") {
						t.Error("manual/reserved name handling failed")
					}
				}
				response := supportTestDecision(request, "nightly", .99, .98)
				var err error
				switch mutation {
				case "delete":
					err = b.Notes.DeleteNote("nightly")
				case "rename":
					err = b.Notes.RenameNote("nightly", "new-nightly")
				case "manual-only":
					err = b.DB.EditNote("nightly", "", "Exact manual-only note")
				case "description":
					err = b.Notes.EditNote("nightly", "Different scope", "Updated text")
				}
				if err != nil {
					t.Fatal(err)
				}
				return response, nil
			})
			m := supportTestMessage(20)
			b.handleGarminAutomaticAppSupport(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
			if sent.count() != 0 {
				t.Fatal("stale selection was sent")
			}
			_, catalog, err := b.supportDecisionRequest(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, note := range catalog {
				if note.Name == "nightly" && (mutation == "delete" || mutation == "rename" || mutation == "manual-only") {
					t.Fatal("stale note in live catalog")
				}
				if note.Name == "new-nightly" {
					found = true
				}
			}
			if mutation == "rename" && !found {
				t.Fatal("renamed note missing")
			}
		})
	}
}

func TestSupportDecisionConcurrentDedupAndDeletionCancellation(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	b.garminDecisions = supportDecisionTestFunc(func(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		calls.Add(1)
		entered <- ctx
		<-release
		return supportTestDecision(request, "nightly", .99, .98), nil
	})
	m := supportTestMessage(20)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
	done := make(chan struct{})
	go func() { defer close(done); b.handleGarminAutomaticAppSupport(client, m, messages) }()
	ctx := <-entered
	b.handleGarminAutomaticAppSupport(client, m, messages)
	b.cancelDeletedGarminMessage(m.ChannelID.String(), m.ID.String())
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("deletion did not cancel decision")
	}
	close(release)
	<-done
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if calls.Load() != 1 || sent.count() != 0 || b.garminMessageVisible(m.ChannelID.String(), m.ID.String()) {
		t.Fatalf("deleted/duplicate message escaped: calls=%d sends=%d", calls.Load(), sent.count())
	}
}

func TestSupportDecisionSlotsAreSeparateAndBounded(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	for i := 0; i < cap(b.garminAISlots); i++ {
		b.garminAISlots <- struct{}{}
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return supportTestDecision(request, "nightly", .99, .98), nil
	})
	var wg sync.WaitGroup
	for i := 20; i < 22; i++ {
		m := supportTestMessage(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.handleGarminAutomaticAppSupport(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
		}()
	}
	<-entered
	<-entered
	m := supportTestMessage(22)
	b.handleGarminAutomaticAppSupport(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if calls.Load() != 2 {
		t.Fatal("decision slots exceeded")
	}
	close(release)
	wg.Wait()
	if sent.count() != 1 {
		t.Fatalf("general AI saturation or concurrent same-note cooldown failed: sends=%d", sent.count())
	}
}

func TestSupportDeletionEventsCancelBeforeLogFiltering(t *testing.T) {
	b := &Bot{}
	for _, bulk := range []bool{false, true} {
		m := supportTestMessage(20)
		if bulk {
			m.ID = 21
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if !b.registerGarminAIRequest(m, cancel) {
			t.Fatal("registration failed")
		}
		if bulk {
			b.onMessageDeleteBulkLog(nil, &gateway.EventMessageDeleteBulk{ChannelID: m.ChannelID, IDs: []snowflake.ID{m.ID}})
		} else {
			b.onMessageDeleteLog(nil, &events.MessageDelete{GenericMessage: &events.GenericMessage{ChannelID: m.ChannelID, MessageID: m.ID}})
		}
		if !errors.Is(ctx.Err(), context.Canceled) || b.garminMessageVisible(m.ChannelID.String(), m.ID.String()) {
			t.Fatal("delete was filtered before cancellation")
		}
	}
}

func TestSupportDecisionStateBoundaries(t *testing.T) {
	states := map[string]supportRequestState{}
	now := time.Now()
	for i := 0; i < supportStateMax; i++ {
		if !claimSupportState(&states, fmt.Sprint(i), now) {
			t.Fatal("early capacity refusal")
		}
	}
	if claimSupportState(&states, "extra", now.Add(time.Hour)) {
		t.Fatal("active entries were evicted")
	}
	finishSupportState(states, "0", true, now)
	if claimSupportState(&states, "0", now.Add(time.Minute)) || !claimSupportState(&states, "extra", now.Add(supportCooldown)) {
		t.Fatal("TTL/capacity not enforced")
	}
	b := &Bot{}
	for i := 0; i < supportStateMax+5; i++ {
		b.cancelDeletedGarminMessage("5", fmt.Sprint(i))
	}
	if len(b.garminDeletedMessages) != supportStateMax {
		t.Fatal("deletion state unbounded")
	}
}

func TestSupportDecisionGuardsAndMinimalInput(t *testing.T) {
	b, client, _ := newSupportDecisionTestBot(t)
	for _, kind := range []string{"bot", "webhook", "other guild", "other channel", "human mention", "attachment only", "unknown reply"} {
		m := supportTestMessage(20)
		switch kind {
		case "bot":
			m.Author.Bot = true
		case "webhook":
			m.WebhookID = snowflakePtr("3")
		case "other guild":
			m.GuildID = snowflakePtr("99")
		case "other channel":
			m.ChannelID = 99
		case "human mention":
			m.Mentions = []discord.User{{ID: 99}}
		case "attachment only":
			m.Content = ""
			m.Attachments = []discord.Attachment{{Filename: "lyrics-crash.png", URL: "https://private.example"}}
		case "unknown reply":
			m.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("99")}
		}
		if b.supportDecisionEligible(client, m) {
			t.Errorf("guard missed %s", kind)
		}
	}
	m := supportTestMessage(20)
	m.Content = strings.Repeat("é", 3000)
	m.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("10")}
	m.ReferencedMessage = &discord.Message{ID: 10, Content: "parent", Attachments: []discord.Attachment{{URL: "https://private.example"}}}
	messages := []cmd.GarminAIMessage{{Role: "assistant", Name: garminAIConversationSummaryName, Content: "persistent summary"}, {Role: "assistant", Content: "prior bot text", Images: []string{"https://private.example"}, Reasoning: "hidden reasoning"}, {Role: "user", Content: m.Content, Name: "discord_42"}}
	input := b.supportDecisionInput(m, messages)
	data, _ := json.Marshal(input)
	if len(input.Message) > 4000 || len(input.Conversation) != 1 || strings.Contains(string(data), "private") || strings.Contains(string(data), "reasoning") || strings.Contains(string(data), "discord_") || strings.Contains(string(data), "summary") {
		t.Fatalf("unbounded/private state: %s", data)
	}
}

func TestConfiguredSupportDecisionsUsedByMessageHandler(t *testing.T) {
	b, client, _ := newSupportDecisionTestBot(t)
	requests := make(chan cmd.DecisionRequest, 1)
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		requests <- request
		return supportTestDecision(request, "none", .01, .99), nil
	})
	b.onMessageCreate(client, supportTestMessage(20))
	select {
	case request := <-requests:
		if request.State.(supportDecisionState).Message != "How do I install the nightly build?" {
			t.Fatal("wrong current message")
		}
	case <-time.After(time.Second):
		t.Fatal("configured decisions not used")
	}
	// Wait for handler completion before the test database closes.
	deadline := time.Now().Add(time.Second)
	for {
		b.supportMu.Lock()
		state := b.supportSeen["20"]
		b.supportMu.Unlock()
		if !state.busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("handler did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSupportDecisionAgentFailedHandoffCanRetry(t *testing.T) {
	for _, failure := range []string{"busy", "cooldown", "model", "reply", "note"} {
		t.Run(failure, func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			decisionCalls := 0
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				decisionCalls++
				return supportTestDecision(request, "agent", .99, .99), nil
			})
			failed := true
			b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if failure == "model" && failed {
					return nil, errors.New("synthetic model failure")
				}
				if failure == "note" {
					return garminHistoryTestCall("get_note", `{"name":"nightly"}`), nil
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Verified support answer"}}, nil
			})
			m := supportTestMessage(20)
			messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
			switch failure {
			case "busy":
				for i := 0; i < cap(b.garminAISlots); i++ {
					b.garminAISlots <- struct{}{}
				}
			case "cooldown":
				b.garminAILastUsed[m.Author.ID.String()] = time.Now()
			case "reply", "note":
				sent.fail.Store(true)
			}
			b.handleGarminAutomaticAppSupport(client, m, messages)
			if _, seen := b.supportSeen[m.ID.String()]; seen || len(b.supportNotes) != 0 {
				t.Fatal("failed handoff retained seen state or a note cooldown")
			}
			attempts := sent.count()
			failed = false
			sent.fail.Store(false)
			if failure == "busy" {
				for i := 0; i < cap(b.garminAISlots); i++ {
					<-b.garminAISlots
				}
			}
			b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
			b.handleGarminAutomaticAppSupport(client, m, messages)
			if state, seen := b.supportSeen[m.ID.String()]; !seen || state.busy || sent.count() != attempts+1 || decisionCalls != 2 {
				t.Fatalf("retry did not complete: seen=%v state=%+v sends=%d decisions=%d", seen, state, sent.count(), decisionCalls)
			}
			b.handleGarminAutomaticAppSupport(client, m, messages)
			if sent.count() != attempts+1 || decisionCalls != 2 {
				t.Fatal("successful handoff was not deduplicated")
			}
		})
	}
}

func TestSupportDecisionAgentIntentionalSilenceIsRemembered(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	decisionCalls := 0
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		decisionCalls++
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return garminHistoryTestCall("do_not_respond", `{}`), nil
	})
	m := supportTestMessage(20)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
	b.handleGarminAutomaticAppSupport(client, m, messages)
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if state, seen := b.supportSeen[m.ID.String()]; !seen || state.busy || sent.count() != 0 || decisionCalls != 1 {
		t.Fatalf("intentional silence was not deduplicated: seen=%v state=%+v sends=%d decisions=%d", seen, state, sent.count(), decisionCalls)
	}
}

func TestSupportDecisionAgentHandoffRestrictsManualNotes(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	if err := b.DB.AddNote("manual", "", "Private manual-only body"); err != nil {
		t.Fatal(err)
	}
	var aiCalls int
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		aiCalls++
		if request.SystemPrompt == garminAppSupportTriagePrompt {
			t.Error("legacy triage used")
		}
		for _, message := range request.Messages {
			if len(message.Images) > 0 {
				t.Error("automatic handoff fetched attachments")
			}
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Verified support answer"}}, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	m := supportTestMessage(20)
	b.handleGarminAutomaticAppSupport(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content, Images: []string{"https://private.example"}}})
	if aiCalls != 1 || sent.count() != 1 {
		t.Fatalf("agent calls=%d sends=%d", aiCalls, sent.count())
	}
	ctx := context.WithValue(context.Background(), automaticSupportContextKey{}, true)
	output, _, _ := b.executeGarminAITool(ctx, client, m, garminHistoryTestCall("list_notes", `{}`).Message.ToolCalls[0])
	if strings.Contains(output, "manual") {
		t.Fatal("manual-only note listed to automatic agent")
	}
	output, _, _ = b.executeGarminAITool(ctx, client, m, garminHistoryTestCall("get_note", `{"name":"manual"}`).Message.ToolCalls[0])
	if strings.Contains(output, "Private manual-only body") || !strings.Contains(output, "unavailable") {
		t.Fatalf("manual note fetched: %s", output)
	}
}
