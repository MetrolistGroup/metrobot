package discord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestRejectedSupportQuestionStaysSilentAndSurvivesFollowup(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "explicit"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			wait := func(id string) {
				t.Helper()
				deadline := time.Now().Add(2 * time.Second)
				for {
					b.supportMu.Lock()
					state, done := b.supportSeen[id]
					b.supportMu.Unlock()
					if done && !state.busy {
						return
					}
					if time.Now().After(deadline) {
						t.Fatal("full message handler did not complete")
					}
					time.Sleep(time.Millisecond)
				}
			}
			question := "how do i even update the app? how do i know theres an update"
			m := supportTestMessage(20)
			m.Content = question
			if !automatic {
				m.Content = "garmin, " + question
			}
			accept, modelCalls := false, 0
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
					found = found || turn.Role == "user" && turn.Content == question
					if strings.Contains(turn.Content, "UNSUPPORTED_DRAFT") {
						t.Fatal("a rejected answer entered retained history")
					}
				}
				if !found {
					t.Fatal("follow-up lost the unanswered question")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "For standalone Android GitHub builds, open Settings → Updater to check for updates and enable update notifications."}}, nil
			})
			b.onMessageCreate(client, m)
			wait(m.ID.String())
			retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
			if sent.count() != 0 || !retained.unanswered || !retained.ambientUntil.IsZero() || len(retained.messages) != 1 || retained.replyID != "" {
				t.Fatal("rejected answer sent a reply, lost the question or enabled ambient replies")
			}
			beforeRetry := modelCalls
			messages := b.garminAITriggeredConversation(m, question)
			if len(messages) != 1 {
				t.Fatal("same-event retry duplicated the question")
			}
			if automatic {
				b.handleGarminAutomaticAppSupport(client, m, messages)
			} else {
				b.handleGarminAI(client, m, messages)
			}
			if modelCalls != beforeRetry || sent.count() != 0 {
				t.Fatal("duplicate event repeated a failed request")
			}
			accept = true
			followup := supportTestMessage(21)
			followup.Content = "answer"
			if !automatic {
				followup.Content = "garmin, answer"
			}
			b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
			b.onMessageCreate(client, followup)
			wait(followup.ID.String())
			if sent.count() != 1 || b.garminAIUserContexts[garminAIUserContextKey(m)].unanswered {
				t.Fatal("follow-up did not deliver and retain the accepted answer")
			}
			b.forgetDeletedGarminContexts(m.ChannelID.String(), m.ID.String())
			if len(b.garminAIUserContexts) != 0 || len(b.garminAIContexts) != 0 {
				t.Fatal("deleting the original question left derived context")
			}
		})
	}
}
