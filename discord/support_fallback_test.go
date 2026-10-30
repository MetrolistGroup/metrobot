package discord

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"go.uber.org/zap"
)

func TestExplicitGreetingSkipsSupportAndOldQuestions(t *testing.T) {
	for _, greeting := range []string{"hi", "hello", "hey"} {
		t.Run(greeting, func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				t.Error("a greeting called the support model")
				return nil, errors.New("unexpected call")
			})
			m := supportTestMessage(30)
			b.rememberGarminAIUnansweredContext(m, []cmd.GarminAIMessage{{Role: "user", Content: "is kmp released"}})
			m.Content = "Garmin " + greeting
			b.onMessageCreate(client, m)
			deadline := time.Now().Add(time.Second)
			for sent.count() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			sent.mu.Lock()
			body := strings.Join(sent.bodies, "\n")
			sent.mu.Unlock()
			if sent.count() != 1 || !strings.Contains(body, `"content":"`+greeting+`"`) {
				t.Fatalf("greeting received the wrong reply: %s", body)
			}
		})
	}
}

func TestSupportFailuresStaySilentAndKeepPendingQuestion(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, failure := range []string{"provider", "timeout", "empty", "nil", "evidence", "checker-error"} {
			t.Run(map[bool]string{false: "explicit", true: "automatic"}[automatic]+"/"+failure, func(t *testing.T) {
				b, client, sent := newSupportDecisionTestBot(t)
				b.Config.OpenRouterDecisionModel = "perplexity/pplx-decider-v1.2"
				if err := b.Notes.AddNote("playback", "Playback checks", "DO_NOT_SEND_FALLBACK"); err != nil {
					t.Fatal(err)
				}
				b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
					if _, routing := request.Questions["route"]; routing {
						return supportTestDecision(request, "agent", .99, .99), nil
					}
					if failure == "checker-error" {
						return nil, errors.New("checker unavailable")
					}
					return supportTestDecision(request, "agent", .01, .01), nil
				})
				calls := 0
				b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					calls++
					switch failure {
					case "provider":
						return nil, errors.New("unavailable")
					case "timeout":
						return nil, context.DeadlineExceeded
					case "nil":
						return nil, nil
					case "empty":
						return &cmd.GarminAICompletion{}, nil
					default:
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
					}
				})
				m := supportTestMessage(20)
				m.Content = "How can I fix playback?"
				messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
				if automatic {
					b.handleGarminAutomaticAppSupport(client, m, messages)
				} else {
					b.handleGarminAI(client, m, messages)
				}
				retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
				if sent.count() != 0 || calls == 0 || !retained.unanswered || !retained.ambientUntil.IsZero() || len(retained.messages) != 1 || retained.replyID != "" {
					t.Fatalf("failure sent a reply or lost pending context: calls=%d deliveries=%d context=%+v", calls, sent.count(), retained)
				}
			})
		}
	}
}

func TestNonSupportAIFailuresStaySilent(t *testing.T) {
	for _, channel := range []string{garminGeneralID, "100000000000000005"} {
		for _, failure := range []string{"provider", "timeout", "nil", "empty", "busy", "unconfigured"} {
			t.Run(channel+"/"+failure, func(t *testing.T) {
				b, client, sent := newSupportDecisionTestBot(t)
				calls := 0
				b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					calls++
					switch failure {
					case "nil":
						return nil, nil
					case "empty":
						return &cmd.GarminAICompletion{}, nil
					case "timeout":
						return nil, context.DeadlineExceeded
					default:
						return nil, errors.New("provider unavailable")
					}
				})
				if failure == "unconfigured" {
					b.garminAI = nil
				}
				if failure == "busy" {
					for i := 0; i < cap(b.garminAISlots); i++ {
						b.garminAISlots <- struct{}{}
					}
				}
				m := supportTestMessage(20)
				m.ChannelID = snowflakeID(channel)
				b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: "tell me a joke"}})
				if sent.count() != 0 || len(b.garminAIUserContexts) != 0 {
					t.Fatal("failed AI request sent or retained a reply")
				}
				if failure != "busy" && failure != "unconfigured" && calls == 0 {
					t.Fatal("failure path never reached the provider")
				}
			})
		}
	}
}

func TestAIReplyDeliveryDoesNotRetryAnotherFormat(t *testing.T) {
	for _, kind := range []string{"plain", "note", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Rejected","code":50035}`))
			}))
			defer server.Close()
			client := mustTestClient(t)
			client.Rest = testREST(server.Client())
			client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: server.Client().Transport, target: server.URL}
			b := &Bot{Logger: zap.NewNop()}
			m := supportTestMessage(20)
			switch kind {
			case "plain":
				if b.sendGarminReply(client, m, "answer") != nil {
					t.Fatal("failed delivery accepted")
				}
			case "note":
				if reply, _ := b.sendGarminAppSupportReply(client, m, "answer", "nightly"); reply != nil {
					t.Fatal("failed delivery accepted")
				}
			case "attachment":
				if reply, _ := b.sendGarminAppSupportReply(client, m, strings.Repeat("answer", 500), ""); reply != nil {
					t.Fatal("failed delivery accepted")
				}
			}
			if requests != 1 {
				t.Fatalf("delivery failure caused %d attempts", requests)
			}
		})
	}
}
