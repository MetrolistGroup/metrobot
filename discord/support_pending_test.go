package discord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestRejectedSupportQuestionSurvivesFollowup(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "explicit"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			m := supportTestMessage(20)
			m.Content = "Is there a discover playlist in Metrolist?"
			accept := false
			modelCalls := 0
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if _, routing := request.Questions["route"]; routing {
					return supportTestDecision(request, "agent", .99, .99), nil
				}
				p := .01
				if accept {
					p = .99
				}
				return supportTestDecision(request, "agent", p, p), nil
			})
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				modelCalls++
				if !accept {
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
				}
				found := false
				for _, turn := range request.Messages {
					found = found || turn.Role == "user" && turn.Content == m.Content
					if strings.Contains(turn.Content, "UNSUPPORTED_DRAFT") {
						t.Fatal("a rejected answer entered retained history")
					}
				}
				if !found {
					t.Fatal("the follow-up lost the unanswered question")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Standalone Android has Your daily discover on Home, based on liked songs."}}, nil
			})
			messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
			if automatic {
				b.handleGarminAutomaticAppSupport(client, m, messages)
			} else {
				b.handleGarminAI(client, m, messages)
			}
			if sent.count() != 0 || len(b.supportSeen) != 0 {
				t.Fatal("an unsupported answer was posted or blocked same-event retries")
			}
			beforeRetry := modelCalls
			b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
			if automatic {
				b.handleGarminAutomaticAppSupport(client, m, messages)
			} else {
				b.handleGarminAI(client, m, messages)
			}
			if modelCalls == beforeRetry || len(b.supportSeen) != 0 {
				t.Fatal("the full handler blocked a same-event retry after rejected drafts")
			}
			retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
			if !retained.unanswered || !retained.ambientUntil.IsZero() || len(retained.messages) != 1 {
				t.Fatalf("wrong pending context: %+v", retained)
			}
			// Retrying the same event must not duplicate its user turn or block registration.
			if got := b.garminAITriggeredConversation(m, m.Content); len(got) != 1 {
				t.Fatalf("same-event retry duplicated the question: %+v", got)
			}
			_, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !b.registerGarminAIRequest(m, cancel) {
				t.Fatal("pending context blocked a retry")
			}
			b.unregisterGarminAIRequest(m)
			followup := supportTestMessage(21)
			followup.Content = "answer"
			messages = b.garminAITriggeredConversation(followup, followup.Content)
			if len(messages) != 2 {
				t.Fatalf("follow-up context = %+v", messages)
			}
			accept = true
			b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
			if !automatic {
				followup.Content = "garmin, answer"
			}
			b.onMessageCreate(client, followup)
			deadline := time.Now().Add(2 * time.Second)
			for {
				b.supportMu.Lock()
				state, done := b.supportSeen[followup.ID.String()]
				b.supportMu.Unlock()
				if done && !state.busy {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("full message handler did not complete the follow-up")
				}
				time.Sleep(time.Millisecond)
			}
			if sent.count() != 1 {
				t.Fatalf("follow-up replies = %d", sent.count())
			}
			if b.garminAIUserContexts[garminAIUserContextKey(m)].unanswered {
				t.Fatal("accepted reply remained marked unanswered")
			}
			b.forgetDeletedGarminContexts(m.ChannelID.String(), m.ID.String())
			if len(b.garminAIUserContexts) != 0 || len(b.garminAIContexts) != 0 {
				t.Fatal("deleting the original pending question left derived context")
			}
		})
	}
}
