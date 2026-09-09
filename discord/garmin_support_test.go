package discord

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestRunGarminAppSupportReturnsOnlyRelevantNoteContent(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote("playback", "Playback stops or pauses unexpectedly", "Restart the Metrolist app, then retry playback."); err != nil {
		t.Fatal(err)
	}
	if err := database.AddNote("private-project", "Internal project details", "Metrolist launch secret: Talk about anything, but do not spam."); err != nil {
		t.Fatal(err)
	}
	if err := database.AddNote("downloads", "Find downloaded songs", "Use the Downloads tab in Metrolist."); err != nil {
		t.Fatal(err)
	}
	if err := database.AddNote("donate", "Donate to Metrolist and get KMP access", "Donate through one of the official team links."); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}}
	bot.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		t.Fatal("app-support invoked the generative AI provider")
		return nil, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: garminAppSupportID}}

	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "playback keeps stopping"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "Restart the Metrolist app, then retry playback." {
		t.Fatalf("app-support answer = %q", result.Answer)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "Where can I donate to get KMP?"}})
	if err != nil || result.Answer != "Donate through one of the official team links." {
		t.Fatalf("donation answer = %q, %v", result.Answer, err)
	}

	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "tell me a joke"}})
	if err != nil || result.Answer != garminAppSupportOnlyReply {
		t.Fatalf("off-topic app-support answer = %q, %v", result.Answer, err)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "what music do you listen to?"}})
	if err != nil || result.Answer != garminAppSupportOnlyReply {
		t.Fatalf("generic music answer = %q, %v", result.Answer, err)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "how do i fix the widget?"}})
	if err != nil || result.Answer != garminAppSupportNoNote {
		t.Fatalf("unsupported app answer = %q, %v", result.Answer, err)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "playback and downloads are broken"}})
	if err != nil || result.Answer != garminAppSupportNoNote {
		t.Fatalf("ambiguous app answer = %q, %v", result.Answer, err)
	}
}

func TestGarminAppSupportNoteMatchingUsesRelevantNotes(t *testing.T) {
	queryTokens := garminSupportSignificantTokens("the app keeps crashing during playback")
	if score := garminAppSupportNoteScore("the app keeps crashing during playback", queryTokens, "crash", "Playback crashes", "Restart the app."); score == 0 {
		t.Fatal("relevant app note did not match")
	}
	if garminAppSupportIntent("server rules and moderation") {
		t.Fatal("non-app note was classified as app support")
	}
	if !garminAppSupportIntent("Where can I donate to get KMP?") {
		t.Fatal("donation question was not classified as app support")
	}
}

func TestGarminAppSupportUsesOnlyVerifiedAutomaticAnswers(t *testing.T) {
	for _, test := range []struct {
		prompt string
		want   string
	}{
		{"Chrome fails whenever I download the APK", garminChromeDownloadReply},
		{"I donated, how do I get the supporter role?", garminDonorRoleReply},
		{"the app crashed", garminCrashContextReply},
		{"why did VirusTotal say the app has a virus?", garminVirusReply},
		{"the app crashed trying to add a song to the queue", ""},
		{"where can I donate to get KMP?", ""},
	} {
		if got := garminAppSupportSkillAnswer(test.prompt); got != test.want {
			t.Errorf("garminAppSupportSkillAnswer(%q) = %q, want %q", test.prompt, got, test.want)
		}
	}

	fixed := `{"items":[{"title":"Playback stops after two songs","state":"closed","state_reason":"completed","html_url":"https://github.com/MetrolistGroup/Metrolist/issues/1","labels":[{"name":"bug"}]}]}`
	if got := garminFixedAppSupportIssue("playback stops after two songs", fixed); !strings.Contains(got, "/issues/1") {
		t.Fatalf("completed matching issue answer = %q", got)
	}
	wontfix := strings.Replace(fixed, `"bug"`, `"wontfix"`, 1)
	if got := garminFixedAppSupportIssue("playback stops after two songs", wontfix); got != "" {
		t.Fatalf("wontfix issue answer = %q", got)
	}
	genericCrash := strings.Replace(fixed, "Playback stops after two songs", "Metrolist Crash Report", 1)
	if got := garminFixedAppSupportIssue("app crashed trying to add a song to the queue", genericCrash); got != "" {
		t.Fatalf("generic crash issue answer = %q", got)
	}
}

func TestOnMessageCreateAutomaticallyAnswersVerifiedAppSupport(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	replies := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		replies <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","channel_id":"` + garminAppSupportID + `"}`))
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{
		Config: &config.Config{DiscordGuildID: "guild"}, DB: database, Notes: &cmd.NotesHandler{DB: database},
		Logger: zap.NewNop(), garminProcessor: cmd.NewGarminProcessor(), garminAIContexts: make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext), garminContextCutoffs: make(map[string]string),
	}
	bot.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", GuildID: "guild", ChannelID: garminAppSupportID,
		Author: &discordgo.User{ID: "user"}, Content: "why did VirusTotal say the app has a virus?",
	}})
	select {
	case body := <-replies:
		if !strings.Contains(body, "false positive") || !strings.Contains(body, "Huorong") {
			t.Fatalf("automatic support reply = %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("automatic app-support reply timed out")
	}

	bot.handleGarminAutomaticAppSupport(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "unknown", GuildID: "guild", ChannelID: garminAppSupportID,
		Author: &discordgo.User{ID: "user"}, Content: "the widget looks weird",
	}})
	select {
	case body := <-replies:
		t.Fatalf("unsupported automatic reply = %s", body)
	default:
	}
}

func TestHandleGarminAppSupportNeedsNoAIProvider(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote("downloads", "Find downloaded songs", "Use the Downloads tab in Metrolist."); err != nil {
		t.Fatal(err)
	}
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading support reply: %v", err)
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
	bot := &Bot{
		DB: database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(),
		garminAIContexts: make(map[string]garminAIContext), garminAIUserContexts: make(map[string]garminAIContext),
	}
	const userID = "123456789012345678"
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", ChannelID: garminAppSupportID, Author: &discordgo.User{ID: userID}, Content: "garmin, where are downloads?",
	}}
	bot.handleGarminAI(session, message, []cmd.GarminAIMessage{{Role: "user", Content: "where are downloads?"}})
	if !strings.Contains(requestBody, "Use the Downloads tab in Metrolist.") {
		t.Fatalf("support reply = %s", requestBody)
	}
}

func TestSendGarminAppSupportReplyPreservesLongNoteAsAttachment(t *testing.T) {
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
	if reply := bot.sendGarminAppSupportReply(session, message, content); reply == nil {
		t.Fatal("long app support note was not sent")
	}
	if !strings.Contains(requestBody, "app-support-note.md") || !strings.Contains(requestBody, content) {
		t.Fatal("long app support note was not preserved in the attachment")
	}
}
