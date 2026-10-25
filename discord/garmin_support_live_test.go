package discord

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	gh "github.com/MetrolistGroup/metrobot/github"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

// Opt-in paid replay: temporary DB, read-only public GitHub, no Discord connection.
// Phrase checks catch the reported regressions; the saved answers still need human review.
func TestSupportAgentLive(t *testing.T) {
	configPath := os.Getenv("METROBOT_SUPPORT_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("set METROBOT_SUPPORT_LIVE_CONFIG, _NOTES and _REPORT for a paid, non-posting replay")
	}
	read := func(path string, out any) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	var config struct {
		Keys          []string `json:"openrouter_api_keys"`
		Model         string   `json:"openrouter_model"`
		DecisionModel string   `json:"openrouter_decision_model"`
	}
	read(configPath, &config)
	if len(config.Keys) == 0 || config.Model == "" || config.DecisionModel == "" {
		t.Fatal("replay requires configured chat keys and model")
	}
	var notes []struct {
		Name        string `json:"name"`
		Description string `json:"short_desc"`
		Content     string `json:"content"`
	}
	read(os.Getenv("METROBOT_SUPPORT_LIVE_NOTES"), &notes)
	database, err := db.Open(filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, note := range notes {
		if err := database.AddNote(note.Name, note.Description, note.Content); err != nil {
			t.Fatalf("note %s: %v", note.Name, err)
		}
	}
	client := cmd.NewOpenRouterClient(config.Keys, config.Model)
	decisions := cmd.NewDecisionsClient(config.Keys, config.DecisionModel)
	type report struct {
		ID           string           `json:"id"`
		Answer       string           `json:"answer"`
		Draft        string           `json:"draft"`
		Checks       []map[string]any `json:"checks"`
		Grounded     float64          `json:"grounded"`
		Suppressed   bool             `json:"suppressed"`
		CheckFailed  bool             `json:"check_failed,omitempty"`
		Note         string           `json:"note,omitempty"`
		Tools        []string         `json:"tools,omitempty"`
		Milliseconds int64            `json:"ms"`
		Error        bool             `json:"error,omitempty"`
	}
	var reports []report
	for i, test := range []struct {
		id         string
		messages   []cmd.GarminAIMessage
		attachment bool
		wantTool   string
		wantNote   string
		forbidden  []string
	}{
		{id: "search-followup", messages: []cmd.GarminAIMessage{
			{Role: "user", Content: "is it me or is searching stuff not working right now?"},
			{Role: "assistant", Content: "What happens when you search? Which app version are you on?"},
			{Role: "user", Content: "it doesn't show results"},
		}, forbidden: []string{"since you're on", "firefox", "reinstall"}},
		{id: "correct-prior-assumption", messages: []cmd.GarminAIMessage{
			{Role: "user", Content: "Search and profile top songs show no results. YouTube Music itself works. I don't use a VPN."},
			{Role: "assistant", Content: "Since you're using nightly, try reinstalling."},
			{Role: "user", Content: "I'm not using the nightly build. My version is 13.7.0."},
		}, forbidden: []string{"since you're on nightly", "try reinstalling", "clear data"}},
		{id: "unread-image", attachment: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "I keep getting this from time to time on multiple songs"}}, forbidden: []string{"cut off", "truncated", "latest nightly", "firefox", "reinstall"}},
		{id: "io-2000", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Metrolist version: 13.7.0\nAndroid version: 17\nIt was on auto play\nMany songs\nError Code: IO UNSPECIFIED (2000)"}}, forbidden: []string{"has been fixed", "fixed in recent", "latest nightly", "connectivity hiccup"}},
		{id: "current-release-vs-nightly", messages: []cmd.GarminAIMessage{{Role: "user", Content: "what is the current release is the nightly better or ?"}}, wantTool: "get_metrolist_status"},
		{id: "debug-vs-release-crash", messages: []cmd.GarminAIMessage{{Role: "user", Content: "After changing phones, stable and nightly crash at startup but debug works. What can I test?"}}, forbidden: []string{"signing", "signed", "permission", "library mismatch", "clear data", "reinstall"}},
		{id: "chrome-download", messages: []cmd.GarminAIMessage{{Role: "user", Content: "The Metrolist APK download fails in Chrome before reaching the Android installer. Which browser should I try?"}}},
		{id: "kmp-access", messages: []cmd.GarminAIMessage{{Role: "user", Content: "What are the requirements for getting Metrolist KMP tester access?"}}, wantNote: "kmp"},
		{id: "nightly-install", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I download and install nightly without uninstalling?"}}, wantNote: "nightly"},
	} {
		r := report{ID: test.id}
		b := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(), garminGitHub: gh.NewAssistantClient("", "MetrolistGroup", "Metrolist")}
		b.garminDecisions = supportDecisionTestFunc(func(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
			r.Draft, _ = request.State.(map[string]any)["proposed_answer"].(string)
			response, err := decisions.Decide(ctx, request)
			if err != nil {
				r.CheckFailed = true
				t.Errorf("%s: evidence check API failed", test.id)
			}
			if response != nil && response.Answers["grounded"].Noul != nil {
				r.Grounded = *response.Answers["grounded"].Noul
			}
			r.Checks = append(r.Checks, map[string]any{"draft": r.Draft, "grounded": r.Grounded, "api_error": err != nil})
			return response, err
		})
		b.garminAI = garminAITestFunc(func(ctx context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			completion, err := client.Complete(ctx, request)
			if completion != nil {
				for _, call := range completion.Message.ToolCalls {
					r.Tools = append(r.Tools, call.Function.Name)
				}
			}
			return completion, err
		})
		message := supportTestMessage(100 + i)
		message.Content = test.messages[len(test.messages)-1].Content
		if test.attachment {
			message.Attachments = []discord.Attachment{{Filename: "unread.png", URL: "https://invalid.example/not-fetched.png"}}
		}
		ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), automaticSupportContextKey{}, true), 60*time.Second)
		start := time.Now()
		result, err := b.runGarminAI(ctx, nil, message, test.messages)
		cancel()
		r.Milliseconds = time.Since(start).Milliseconds()
		if err != nil {
			r.Error = true
			t.Errorf("%s: agent failed", test.id)
		} else {
			r.Answer, r.Note, r.Suppressed = result.Answer, result.NoteName, result.Silent
			if (test.wantNote != "" || test.wantTool != "" || test.id == "chrome-download" || test.id == "unread-image") && (r.Answer == "" || result.Silent) {
				t.Errorf("%s: no useful answer", test.id)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(strings.ToLower(r.Answer), forbidden) {
					t.Errorf("%s: review forbidden phrase %q", test.id, forbidden)
				}
			}
			if test.wantNote != "" && r.Note != test.wantNote {
				t.Errorf("%s: note = %q, want %q", test.id, r.Note, test.wantNote)
			}
			if test.wantTool != "" && (!strings.Contains(strings.Join(r.Tools, ","), test.wantTool) || r.Note != "") {
				t.Errorf("%s: expected release lookup and synthesis, got tools=%v note=%q", test.id, r.Tools, r.Note)
			}
			if test.id == "chrome-download" && !strings.Contains(strings.ToLower(r.Answer), "firefox") {
				t.Error("Chrome download case lost applicable Firefox advice")
			}
		}
		reports = append(reports, r)
	}
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("METROBOT_SUPPORT_LIVE_REPORT"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
