package discord

import (
	"context"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func seedGarminAmbientExchange(b *Bot, m *discord.Message, answer string) {
	m.ID = snowflakeID("102")
	previous := *m
	previous.ID = snowflakeID("100")
	b.rememberGarminAIContext("101", &previous, []cmd.GarminAIMessage{{Role: "user", Content: "garmin, help me"}, {Role: "assistant", Content: answer}})
	b.storeCachedMessage(cachedLogMessage{ID: "101", ChannelID: m.ChannelID.String(), Bot: true})
}

func TestGarminAmbientRequiresClearContinuation(t *testing.T) {
	for _, test := range []struct {
		content string
		answer  string
		want    bool
	}{
		{"what do you mean?", "that depends on the version.", true},
		{"can you explain that", "that depends on the version.", true},
		{"tell me more about that", "the setting changes playback.", true},
		{"why did you suggest that?", "try the stable release.", true},
		{"yes please", "do you want the steps?", true},
		{"no thanks", "do you want the steps?", true},
		{"stop replying", "try the setting.", true},
		{"yes", "try the setting.", false},
		{"what is this?", "try the setting.", false},
		{"can you help me?", "try the setting.", false},
		{"is it raining?", "try the setting.", false},
		{"metrobot is alive again", "try the setting.", false},
		{"garmin is annoying", "try the setting.", false},
		{"i told metrobot, are you okay?", "try the setting.", false},
		{"alice, what do you mean?", "try the setting.", false},
		{"<@42> can you explain that", "try the setting.", false},
		{"thanks alice", "try the setting.", false},
		{"thanks", "try the setting.", false},
		{"nice", "try the setting.", false},
		{"lol", "try the setting.", false},
		{"👍 <:happy:123>", "try the setting.", false},
		{"the tutorial says tell me more about that", "try the setting.", false},
	} {
		t.Run(test.content, func(t *testing.T) {
			b := lifecycleBot(&fakeGarminAI{})
			m := lifecycleMessage("102")
			m.Content = test.content
			seedGarminAmbientExchange(b, m, test.answer)
			_, got := b.garminAIAmbientContinuation(m, m.Content)
			if got != test.want {
				t.Fatalf("directed = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGarminAmbientRejectsInterruptedOrMissingTurn(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
	}{
		{"intervening human", "102"},
		{"later human", "105"},
		{"missing bot reply", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				t.Fatal("ambiguous ambient message called the model")
				return nil, nil
			}))
			m := lifecycleMessage("102")
			m.Content = "what do you mean?"
			seedGarminAmbientExchange(b, m, "try the setting.")
			m.ID = snowflakeID("104")
			if test.id == "" {
				b.messageLogCache = nil
			} else {
				b.storeCachedMessage(cachedLogMessage{ID: test.id, ChannelID: m.ChannelID.String(), AuthorID: "42"})
			}
			if _, directed := b.garminAIAmbientContinuation(m, m.Content); directed {
				t.Fatal("accepted a conversation without an uninterrupted bot turn")
			}
			token := b.tryBeginGarminAIAmbient(m)
			result, err := b.runGarminAIWithMode(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}, true, token)
			if err != nil || !result.Silent {
				t.Fatalf("result = %#v, %v", result, err)
			}
		})
	}
}

func TestGarminAmbientAmbiguousMessagesNeverCallModel(t *testing.T) {
	for _, content := range []string{"what is this?", "metrobot is alive", "thanks", "are you okay?", "alice, stop talking"} {
		b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			t.Fatal("ambiguous ambient message called the model")
			return nil, nil
		}))
		m := lifecycleMessage("102")
		m.Content = content
		seedGarminAmbientExchange(b, m, "try the setting.")
		result, err := b.runGarminAIWithMode(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: content}}, true, b.tryBeginGarminAIAmbient(m))
		if err != nil || !result.Silent {
			t.Fatalf("%q = %#v, %v", content, result, err)
		}
	}
}

func TestGarminAddressDetection(t *testing.T) {
	s := mustTestClient(t)
	s.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: snowflakeID("123")}})
	for content, want := range map[string]bool{
		"<@123> explain this":                true,
		"<@!123>, are you okay?":             true,
		"hey <@123>":                         true,
		"thanks, <@123>":                     true,
		"metrobot, are you okay?":            true,
		"hey metrobot, explain this":         true,
		"metrobot can you help?":             true,
		"thanks, metrobot":                   true,
		"<@123> is alive":                    false,
		"i think <@123> is broken":           false,
		"tell <@123> to stop":                false,
		"metrobot is alive":                  false,
		"garmin can do that":                 false,
		"metrobot would be useful":           false,
		"alice, can you fix metrobot?":       false,
		"they said metrobot, what happened?": false,
	} {
		if _, got := extractGarminPrompt(s, content); got != want {
			t.Errorf("address %q = %v, want %v", content, got, want)
		}
	}
	m := lifecycleMessage("102")
	m.ReferencedMessage = &discord.Message{ID: snowflakeID("101"), Author: discord.User{ID: snowflakeID("123"), Bot: true}}
	if !garminReplyToBot(s, m) {
		t.Fatal("reply to this bot was rejected")
	}
	m.ReferencedMessage.Author.ID = snowflakeID("42")
	if garminReplyToBot(s, m) {
		t.Fatal("reply to someone else was accepted")
	}
}

func TestGarminAmbientCanAnswerReactOrStaySilent(t *testing.T) {
	for _, action := range []string{"answer", "reaction", "silence", "unneeded refusal", "interrupted", "deleted"} {
		t.Run(action, func(t *testing.T) {
			m := lifecycleMessage("102")
			m.Content = "what do you mean?"
			if action == "unneeded refusal" {
				m.Content = "no thanks"
			}
			b := lifecycleBot(nil)
			seedGarminAmbientExchange(b, m, "do you want the steps?")
			calls := 0
			b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				switch action {
				case "reaction":
					return garminHistoryTestCall("react_to_message", `{"emoji":"👍"}`), nil
				case "silence":
					return garminHistoryTestCall("do_not_respond", `{}`), nil
				case "unneeded refusal":
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "i can't do that"}}, nil
				case "interrupted":
					b.storeCachedMessage(cachedLogMessage{ID: "103", ChannelID: m.ChannelID.String()})
				case "deleted":
					b.cancelDeletedGarminMessage(m.ChannelID.String(), m.ID.String())
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "the setting changes playback."}}, nil
			})
			s := lifecycleClient(t, func() (string, error) {
				t.Fatal("run sent an unexpected text reply")
				return "", nil
			})
			token := b.tryBeginGarminAIAmbient(m)
			messages, ok := b.garminAIAmbientContinuation(m, m.Content)
			if !ok {
				t.Fatal("clear follow-up rejected")
			}
			result, err := b.runGarminAIWithMode(context.Background(), s, m, messages, true, token)
			if err != nil || calls != 1 {
				t.Fatalf("run = %#v, %v, calls = %d", result, err, calls)
			}
			if action == "answer" {
				if result.Silent || result.Answer == "" {
					t.Fatalf("answer suppressed: %#v", result)
				}
			} else if !result.Silent || result.Interacted != (action == "reaction") {
				t.Fatalf("wrong action: %#v", result)
			}
		})
	}
}
