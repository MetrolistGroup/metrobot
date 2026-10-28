package discord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestSupportFallbackUsesCurrentApprovedNotes(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "Songs won't play"}}
	if got := b.supportFallbackReply(messages); !strings.Contains(got, "Exact saved nightly instructions.") {
		t.Fatal(got)
	}
	body := strings.Repeat("Approved playback checks. ", 600) + "FULL_TAIL"
	if err := b.Notes.AddNote("playback", "Playback checks", body); err != nil {
		t.Fatal(err)
	}
	if got := b.supportFallbackReply(messages); !strings.HasSuffix(got, "FULL_TAIL") || !strings.Contains(got, "not a confirmed fix") {
		t.Fatal("fallback truncated or claimed a diagnosis")
	}
	if err := b.Notes.EditNote("playback", "Playback checks", "UPDATED_CHECKS"); err != nil {
		t.Fatal(err)
	}
	if got := b.supportFallbackReply(messages); !strings.Contains(got, "UPDATED_CHECKS") {
		t.Fatal("fallback used stale content")
	}
	if err := b.DB.EditNote("playback", "", "MANUAL_ONLY"); err != nil {
		t.Fatal(err)
	}
	if got := b.supportFallbackReply(messages); strings.Contains(got, "MANUAL_ONLY") || !strings.Contains(got, "Exact saved nightly instructions.") {
		t.Fatal("fallback ignored opt-out")
	}
	if err := b.Notes.DeleteNote("nightly"); err != nil {
		t.Fatal(err)
	}
	if got := b.supportFallbackReply(messages); got != supportUnavailableReply {
		t.Fatal("missing notes were invented")
	}
}

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

func TestSupportFallbackFollowsCurrentTopic(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	for _, question := range []string{"Source error", "Downloaded songs work. It only fails on songs I stream.", "Streaming fails"} {
		if got := b.supportFallbackReply([]cmd.GarminAIMessage{{Role: "user", Content: question}}); !strings.Contains(got, "Exact saved nightly instructions.") {
			t.Errorf("%q lost its playback fallback: %s", question, got)
		}
	}
	for _, question := range []string{"hi", "is kmp released", "Will kmp work on Windows ARM?", "who would i contact for this though", "how do i report a fake Google Play app", "How do I translate song lyrics?", "Can I reorder songs in playlists?", "KMP songs won't play"} {
		messages := []cmd.GarminAIMessage{{Role: "user", Content: "Songs won't play"}, {Role: "user", Content: question}}
		if got := b.supportFallbackReply(messages); got != supportUnavailableReply {
			t.Errorf("%q received unrelated playback guidance: %s", question, got)
		}
	}
}

func TestUnhandledSupportFallsBackWithoutAnotherModelCall(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, failure := range []string{"provider", "evidence"} {
			t.Run(map[bool]string{false: "explicit", true: "automatic"}[automatic]+"/"+failure, func(t *testing.T) {
				b, client, sent := newSupportDecisionTestBot(t)
				if err := b.Notes.AddNote("playback", "Playback checks", "SAFE_PLAYBACK_CHECKS"); err != nil {
					t.Fatal(err)
				}
				b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
					if _, routing := request.Questions["route"]; routing {
						return supportTestDecision(request, "agent", .99, .99), nil
					}
					return supportTestDecision(request, "agent", .01, .01), nil
				})
				b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					if failure == "provider" {
						return nil, errors.New("unavailable")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
				})
				m := supportTestMessage(20)
				m.Content = "How can I fix playback?"
				messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
				if automatic {
					b.handleGarminAutomaticAppSupport(client, m, messages)
				} else {
					b.handleGarminAI(client, m, messages)
				}
				sent.mu.Lock()
				body := strings.Join(sent.bodies, "\n")
				sent.mu.Unlock()
				if sent.count() != 1 || !strings.Contains(body, "SAFE_PLAYBACK_CHECKS") || strings.Contains(body, "UNSUPPORTED_DRAFT") {
					t.Fatalf("wrong fallback: %s", body)
				}
				retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
				if !retained.unanswered || !retained.ambientUntil.IsZero() || len(retained.messages) != 1 {
					t.Fatal("fallback lost pending context or started ambient replies")
				}
			})
		}
	}
}
