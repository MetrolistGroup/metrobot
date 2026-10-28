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

// Opt-in paid replay through the real message handler, with Discord delivery mocked.
func TestSupportUpdateMessageLive(t *testing.T) {
	testSupportMessageLive(t, []cmd.GarminAIMessage{{Role: "user", Content: "how do i even update the app? how do i know theres an update"}},
		[]string{"settings", "updater", "check for updates"},
		[]string{"metrolist doesn't have an in-app update checker", "metrolist has no in-app updater", "metrolist doesn't have update notifications"}, "")
}

func TestSupportSlowDownloadMessageLive(t *testing.T) {
	testSupportMessageLive(t, []cmd.GarminAIMessage{{Role: "user", Content: "why is metrolist download speeds so slow"}},
		[]string{"nightly", "back", "unstable"}, nil,
		"9dbe585f65a6950e9a8c088f1603e01742401838")
}

func TestSupportPlaybackAndCastingLive(t *testing.T) {
	for _, test := range []struct {
		name     string
		messages []cmd.GarminAIMessage
	}{
		{"source-error", []cmd.GarminAIMessage{{Role: "user", Content: "What if it said playback failed because of Source error"}}},
		{"streamed-only", []cmd.GarminAIMessage{
			{Role: "user", Content: "What if it said playback failed because of Source error"},
			{Role: "assistant", Content: "I can't verify a fix for 'Source error' playback failure. Does it affect streamed songs, downloaded songs, or both?"},
			{Role: "user", Content: "Undownloaded song only, I can still listen to downloaded song."},
		}},
		{"wifi-casting", []cmd.GarminAIMessage{{Role: "user", Content: "The option to stream to another device via WiFi is still not working?"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources, answer := testSupportMessageLive(t, test.messages, nil, []string{"try reinstalling", "uninstall and reinstall", "clear app data to fix", "definitely fixed"}, "")
			if test.name == "wifi-casting" && !strings.Contains(answer, "general playback steps") && !strings.Contains(strings.ToLower(answer), "cast") {
				t.Error("Wi-Fi answer omitted scoped casting guidance")
			}
			if test.name != "wifi-casting" && !strings.Contains(answer, "general playback steps") {
				lower := strings.ToLower(answer)
				if strings.Contains(lower, "nightly") {
					if err := verifyNightlyReplayEvidence(sources, answer, ""); err != nil {
						t.Error(err)
					}
					if !strings.Contains(lower, "back") || !containsAnyGarminPhrase(lower, "over your existing", "over the existing", "in-place") {
						t.Error("nightly trial omitted backup or in-place installation")
					}
				} else if !containsAnyGarminPhrase(lower, "try another", "force-close", "force close", "reopen") {
					t.Error("playback reply gave neither a sourced trial nor concrete low-risk checks")
				}
			}
		})
	}
}

func testSupportMessageLive(t *testing.T, messages []cmd.GarminAIMessage, want, forbidden []string, expectedFix string) ([]supportLiveSource, string) {
	t.Helper()
	configPath := os.Getenv("METROBOT_SUPPORT_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("set the support live config, notes and report paths for a paid, non-posting replay")
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
		t.Fatal("replay requires configured chat keys and models")
	}
	var notes []struct {
		Name        string `json:"name"`
		Description string `json:"short_desc"`
		Content     string `json:"content"`
	}
	read(os.Getenv("METROBOT_SUPPORT_LIVE_NOTES"), &notes)
	b, client, sent := newSupportDecisionTestBot(t)
	existing, err := b.Notes.DB.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range existing {
		if err := b.Notes.DeleteNote(note.Name); err != nil {
			t.Fatal(err)
		}
	}
	for _, note := range notes {
		if err := b.DB.AddNote(note.Name, note.Description, note.Content); err != nil {
			t.Fatal(err)
		}
	}
	model := cmd.NewOpenRouterClient(config.Keys, config.Model)
	var tools []string
	b.garminAI = garminAITestFunc(func(ctx context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		completion, err := model.Complete(ctx, request)
		if completion != nil {
			for _, call := range completion.Message.ToolCalls {
				tools = append(tools, call.Function.Name)
			}
		}
		return completion, err
	})
	decisions := cmd.NewDecisionsClient(config.Keys, config.DecisionModel)
	var checks []map[string]any
	var sources []supportLiveSource
	b.garminDecisions = supportDecisionTestFunc(func(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		response, err := decisions.Decide(ctx, request)
		state, _ := request.State.(map[string]any)
		if draft, ok := state["proposed_answer"]; ok {
			encoded, encodeErr := json.Marshal(state["tool_results"])
			var supplied []supportLiveSource
			if encodeErr != nil || json.Unmarshal(encoded, &supplied) != nil {
				t.Error("could not record tool provenance")
			}
			sources = nil
			for _, source := range supplied {
				sources = append(sources, source)
			}
			check := map[string]any{"draft": draft, "api_error": err != nil}
			if response != nil {
				check["grounded"] = response.Answers["grounded"].Noul
				check["problem"] = response.Answers["problem"].Choice
			}
			checks = append(checks, check)
		}
		return response, err
	})
	var githubConfig struct {
		Token string `json:"github_token"`
	}
	read(configPath, &githubConfig)
	b.garminGitHub = gh.NewAssistantClient(githubConfig.Token, "MetrolistGroup", "Metrolist")
	m := supportTestMessage(20)
	m.CreatedAt = time.Now().UTC()
	m.Content = messages[len(messages)-1].Content
	if len(messages) > 1 {
		previous := *m
		previous.ID--
		b.rememberGarminAIUserContext(&previous, messages[:len(messages)-1])
	}
	b.onMessageCreate(client, m)
	deadline := time.Now().Add(garminSupportTimeout + 10*time.Second)
	for {
		b.supportMu.Lock()
		state, completed := b.supportSeen[m.ID.String()]
		b.supportMu.Unlock()
		if completed && !state.busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("full message handler did not complete the real update prompt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	sent.mu.Lock()
	body := strings.Join(sent.bodies, "\n")
	deliveries := make([]json.RawMessage, len(sent.bodies))
	for i, payload := range sent.bodies {
		deliveries[i] = json.RawMessage(payload)
	}
	sent.mu.Unlock()
	report, err := json.MarshalIndent(map[string]any{"deliveries": deliveries, "checks": checks, "tools": tools, "sources": sources}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	reportPath := os.Getenv("METROBOT_SUPPORT_LIVE_REPORT")
	if strings.Contains(t.Name(), "/") {
		reportPath += "." + strings.ReplaceAll(t.Name(), "/", "-") + ".json"
	}
	if err := os.WriteFile(reportPath, report, 0o600); err != nil {
		t.Fatal(err)
	}
	lower := strings.ReplaceAll(strings.ToLower(body), "*", "")
	if sent.count() != 1 || strings.Contains(body, "I couldn't verify a reliable answer from the available references.") || strings.Contains(body, supportUnavailableReply) {
		t.Fatal("real message did not deliver a useful answer; inspect the private report")
	}
	for _, fact := range want {
		if !strings.Contains(lower, fact) {
			t.Errorf("update answer omitted %q", fact)
		}
	}
	for _, denial := range forbidden {
		if strings.Contains(lower, denial) {
			t.Errorf("answer repeated unsupported denial %q", denial)
		}
	}
	if expectedFix != "" {
		if err := verifyNightlyReplayEvidence(sources, body, expectedFix); err != nil {
			t.Error(err)
		}
	}
	return sources, body
}

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
		GitHubToken   string   `json:"github_token"`
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
		PromptChars  int              `json:"prompt_chars"`
		ModelCalls   int              `json:"model_calls"`
	}
	var reports []report
	for i, test := range []struct {
		id           string
		messages     []cmd.GarminAIMessage
		attachment   bool
		wantRelease  bool
		wantNote     string
		selectedNote string
		wantAnswer   bool
		wantSilent   bool
		casual       bool
		forbidden    []string
	}{
		{id: "source-error", messages: []cmd.GarminAIMessage{{Role: "user", Content: "What if it said playback failed because of Source error"}}, wantAnswer: true},
		{id: "streamed-only", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Source error"}, {Role: "assistant", Content: "Does it happen with downloaded songs too?"}, {Role: "user", Content: "Downloaded songs work. It only fails on songs I stream."}}, wantAnswer: true},
		{id: "wifi-casting", messages: []cmd.GarminAIMessage{{Role: "user", Content: "The option to stream to another device via WiFi is still not working?"}}, wantAnswer: true, forbidden: []string{"downloaded songs", "source error"}},
		{id: "kmp-availability", messages: []cmd.GarminAIMessage{{Role: "user", Content: "is kmp released?"}}, wantAnswer: true, wantNote: "kmp", forbidden: []string{"pinned", "check the `kmp` note", "is released for", "source exists"}},
		{id: "kmp-windows-arm", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Will kmp work on Windows ARM or is it only Windows x64?"}}, wantAnswer: true, selectedNote: "kmp", forbidden: []string{"prismo", "prism", "emulation", "playback checks", "at this time"}},
		{id: "impersonation-report", messages: []cmd.GarminAIMessage{{Role: "user", Content: "how do i tell someone about a fake google play impersonation app"}}, wantAnswer: true, forbidden: []string{"in-app reporting", "cannot verify", "dsc.gg", "t.me", "playback"}},
		{id: "impersonation-contact", messages: []cmd.GarminAIMessage{{Role: "user", Content: "how do i tell someone about a fake google play impersonation app"}, {Role: "assistant", Content: "Share the fake app's store link with the moderators here."}, {Role: "user", Content: "who would i contact for this though"}}, wantAnswer: true, forbidden: []string{"dsc.gg", "t.me", "playback", "cannot verify"}},
		{id: "kmp-new-topic", messages: []cmd.GarminAIMessage{{Role: "user", Content: "how do i report a fake google play impersonation app"}, {Role: "assistant", Content: "Share its store link with the moderators here."}, {Role: "user", Content: "has kmp been released?"}}, wantAnswer: true, wantNote: "kmp", forbidden: []string{"impersonation", "moderators", "dsc.gg", "pinned", "playback"}},
		{id: "casual-banter", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "garmin you look like a microwave"}}, wantAnswer: true, forbidden: []string{"as an ai", "physical appearance", "please be respectful", "assist you"}},
		{id: "update-question", messages: []cmd.GarminAIMessage{{Role: "user", Content: "how do i even update the app? how do i know theres an update"}}, wantAnswer: true},
		{id: "update-question-selected", messages: []cmd.GarminAIMessage{{Role: "user", Content: "how do i even update the app? how do i know theres an update"}}, selectedNote: "up", wantAnswer: true},
		{id: "update-izzy", messages: []cmd.GarminAIMessage{{Role: "user", Content: "I installed standalone Metrolist from IzzyOnDroid. Where is the updater?"}}, selectedNote: "up", wantAnswer: true},
		{id: "discover-question", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Is there a discover playlist in Metrolist?"}}, wantAnswer: true},
		{id: "discover-note", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Where is Your daily discover in standalone Android?"}}, selectedNote: "discover", wantNote: "discover"},
		{id: "discover-followup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Is there a discover playlist in Metrolist?"}, {Role: "assistant", Content: "I'm ready to assist. What is your question?"}, {Role: "user", Content: "answer"}}, wantAnswer: true},
		{id: "likes-auto-sync", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Standalone Android Metrolist 13.7.0: can I turn off automatic account sync so new YouTube likes stop appearing?"}}, wantAnswer: true, forbidden: []string{"reinstall", "clear app data", "delete your"}},
		{id: "casual-unrelated", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "hello, how are you?"}}, wantAnswer: true, forbidden: []string{"as an ai", "hanging out in the wires", "chaos"}},
		{id: "casual-login-playlists", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "explain login to him and how to get yt music playlists onto metrolist"}}, wantAnswer: true},
		{id: "casual-correct-login", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "explain login and how to get youtube music playlists onto metrolist"}, {Role: "assistant", Content: "metrolist doesn't do login."}, {Role: "user", Content: "yes it does, explain how"}}, wantAnswer: true},
		{id: "casual-short-correction", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "does Metrolist support login?"}, {Role: "assistant", Content: "metrolist doesn't do login."}, {Role: "user", Content: "yes it does"}}, wantAnswer: true},
		{id: "casual-kmp-platforms", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "Is Metrolist strictly Android only? What about KMP?"}}, wantAnswer: true},
		{id: "casual-kmp-requirements", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "What minimum Android, iOS and macOS versions does Metrolist KMP support? Is its Android APK an in-place update for standalone Metrolist?"}}, wantAnswer: true},
		{id: "kmp-requirements", messages: []cmd.GarminAIMessage{{Role: "user", Content: "What minimum Android, iOS and macOS versions does Metrolist KMP support? Is its Android APK an in-place update for standalone Metrolist?"}}, wantAnswer: true, selectedNote: "kmp"},
		{id: "casual-android-playlists", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "Regular Android Metrolist 13.7.0: how do I sign in and get my YouTube Music playlists?"}}, wantAnswer: true},
		{id: "casual-kmp-only-followup", casual: true, messages: []cmd.GarminAIMessage{{Role: "user", Content: "Can I log into YouTube Music in Metrolist?"}, {Role: "assistant", Content: "Google login is available."}, {Role: "user", Content: "is that a mkp feature only"}}, wantAnswer: true},
		{id: "richpresence-wording", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I turn on RichPresence in Metrolist?"}}, wantAnswer: true, forbidden: []string{"reinstall", "profile buttons"}},
		{id: "kmp-lastfm-setup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I connect Last.fm in Metrolist KMP?"}}, wantAnswer: true, forbidden: []string{"send me your", "reinstall"}},
		{id: "short-track-threshold", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Regular Android Metrolist 13.7.0, default Last.fm settings: for a 40-second song, how long must I listen before it scrobbles?"}}, wantAnswer: true, forbidden: []string{"listen for 30", "play for 30"}},
		{id: "kmp-login-platform-unknown", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I sign into Google in Metrolist KMP?"}}, wantAnswer: true},
		{id: "logout-keep-library", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I log out of regular Android Metrolist 13.7.0 without losing my library?"}}, wantAnswer: true, forbidden: []string{"reinstall", "clear app data"}},
		{id: "confirmed-topic-followup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Where can my friends see what I'm listening to?"}, {Role: "assistant", Content: "Do you mean Discord Rich Presence?"}, {Role: "user", Content: "Yes, how do I set that up in Metrolist?"}}, wantAnswer: true},
		{id: "resolved-no-reply", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Discord works now, thanks, that fixed it!"}}, wantSilent: true},
		{id: "staff-action-no-reply", messages: []cmd.GarminAIMessage{{Role: "user", Content: "I donated and sent my receipt. Please grant me the KMP tester role now."}}, wantSilent: true},
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
		{id: "current-release-vs-nightly", messages: []cmd.GarminAIMessage{{Role: "user", Content: "what is the current release is the nightly better or ?"}}, wantRelease: true},
		{id: "debug-vs-release-crash", messages: []cmd.GarminAIMessage{{Role: "user", Content: "After changing phones, stable and nightly crash at startup but debug works. What can I test?"}}, forbidden: []string{"signing", "signed", "permission", "library mismatch", "clear data", "reinstall", "settings → about", "settings > about"}},
		{id: "chrome-download", messages: []cmd.GarminAIMessage{{Role: "user", Content: "The Metrolist APK download fails in Chrome before reaching the Android installer. Which browser should I try?"}}},
		{id: "kmp-access", messages: []cmd.GarminAIMessage{{Role: "user", Content: "What are the requirements for getting Metrolist KMP tester access?"}}, wantNote: "kmp", selectedNote: "kmp"},
		{id: "nightly-install", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I download and install nightly without uninstalling?"}}, wantNote: "nightly", selectedNote: "nightly"},
		{id: "rpc-invisible-excerpt", messages: []cmd.GarminAIMessage{{Role: "user", Content: "My Discord status is Invisible and Metrolist RPC does not show. What should I change?"}}, wantNote: "rpc", selectedNote: "rpc", forbidden: []string{"profile buttons", "social sdk", "permissions"}},
		{id: "rpc-setup-not-faq", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Guys how do I connect my Metrolist to Discord?"}}, wantAnswer: true, forbidden: []string{"profile buttons", "social sdk", "permissions", "reinstall"}},
		// Fault injection: a mismatched router note may abstain, but must not turn setup into a troubleshooting FAQ.
		{id: "rpc-setup-with-related-note", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I connect Metrolist to Discord?"}}, selectedNote: "rpc", forbidden: []string{"profile buttons", "social sdk", "permissions", "reinstall"}},
		{id: "rpc-setup-variant-followup", messages: []cmd.GarminAIMessage{
			{Role: "user", Content: "How do I connect Metrolist to Discord?"},
			{Role: "assistant", Content: "Are you using regular Android Metrolist or KMP? Which platform?"},
			{Role: "user", Content: "regular Android 13.7.0"},
		}, wantAnswer: true, forbidden: []string{"profile buttons", "social sdk", "permissions", "reinstall"}},
		{id: "android-rpc-setup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Regular Android Metrolist 13.7.0: how do I enable Discord RPC?"}}, wantAnswer: true, forbidden: []string{"settings > account", "settings → account", "paste your token", "reinstall"}},
		{id: "kmp-rpc-setup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I connect Discord in Metrolist KMP on desktop?"}}, wantAnswer: true, forbidden: []string{"settings > account", "settings → account", "paste your token", "reinstall"}},
		{id: "android-music-login", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Where do I sign in to my YouTube Music account in regular Android Metrolist 13.7.0?"}}, wantAnswer: true, forbidden: []string{"settings > account", "settings → account", "send me your", "reinstall"}},
		{id: "kmp-windows-login", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I log into YouTube Music in Metrolist KMP on Windows?"}}, wantAnswer: true, forbidden: []string{"settings > account", "settings → account", "send me your", "reinstall"}},
		{id: "android-lastfm-setup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I set up Last.fm scrobbling in regular Android Metrolist 13.7.0?"}}, wantAnswer: true, forbidden: []string{"settings > account", "settings → account", "send me your", "reinstall"}},
		// Production misses from 27 October 2026 that ended in the fixed "couldn't verify" notice.
		{id: "lyrics-translation", messages: []cmd.GarminAIMessage{{Role: "user", Content: "Gn! Could someone tell me how to add the lyrics translation to my native language (Portuguese) below the original lyrics? I'm having trouble setting it up..."}}, wantAnswer: true, forbidden: []string{"reinstall", "clear app data"}},
		{id: "login-keeps-library", messages: []cmd.GarminAIMessage{{Role: "user", Content: "help. If I make an account now after ive used this app without being logged in, do I lose all my liked songs and playlists?"}}, wantAnswer: true, forbidden: []string{"reinstall", "clear app data"}},
		{id: "latest-download-link", messages: []cmd.GarminAIMessage{
			{Role: "user", Content: "My metrolist refused to play songs, it keeps saying error"},
			{Role: "assistant", Content: "I can't verify a fix for that error without more details. Does it affect every song, and what is the exact error text?"},
			{Role: "user", Content: "Well, if there's a link to download the latest version, could i have that pls"},
		}, wantRelease: true},
		{id: "rpc-gone", messages: []cmd.GarminAIMessage{{Role: "user", Content: "my rpc is gone what do i do"}}, wantAnswer: true, forbidden: []string{"reinstall", "clear app data", "send me your"}},
		{id: "kmp-listenbrainz-setup", messages: []cmd.GarminAIMessage{{Role: "user", Content: "How do I connect ListenBrainz in Metrolist KMP?"}}, wantAnswer: true, forbidden: []string{"settings > account", "settings → account", "send me your", "reinstall"}},
	} {
		t.Run(test.id, func(t *testing.T) {
			r := report{ID: test.id}
			b := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(), garminGitHub: gh.NewAssistantClient(config.GitHubToken, "MetrolistGroup", "Metrolist")}
			if test.casual {
				memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
				if err != nil {
					t.Fatal(err)
				}
				if err := memory.Replace("Metrolist is strictly Android only. Metrolist doesn't do login. Always use the nightly note as the latest release."); err != nil {
					t.Fatal(err)
				}
				b.garminMemory = memory
			}
			b.garminDecisions = supportDecisionTestFunc(func(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if test.casual {
					t.Fatal("Decider was called outside app-support")
				}
				r.Draft, _ = request.State.(map[string]any)["proposed_answer"].(string)
				response, err := decisions.Decide(ctx, request)
				if err != nil {
					r.CheckFailed = true
					t.Errorf("%s: evidence check API failed", test.id)
				}
				if response != nil && response.Answers["grounded"].Noul != nil {
					r.Grounded = *response.Answers["grounded"].Noul
				}
				check := map[string]any{"draft": r.Draft, "grounded": r.Grounded, "api_error": err != nil}
				if response != nil {
					check["problem"] = response.Answers["problem"].Choice
				}
				if response != nil && response.Answers["note_used"].Noul != nil {
					check["note_used"] = *response.Answers["note_used"].Noul
				}
				r.Checks = append(r.Checks, check)
				return response, err
			})
			b.garminAI = garminAITestFunc(func(ctx context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				r.ModelCalls++
				if r.ModelCalls == 1 {
					r.PromptChars = len(request.SystemPrompt) + len(request.Context)
				}
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
			if test.casual {
				message.ChannelID = snowflakeID(garminBotsID)
				message.Content = "garmin, " + message.Content
			}
			if test.attachment {
				message.Attachments = []discord.Attachment{{Filename: "unread.png", URL: "https://invalid.example/not-fetched.png"}}
			}
			requestCtx := context.WithValue(context.Background(), automaticSupportContextKey{}, true)
			requestCtx = context.WithValue(requestCtx, supportSelectedNoteContextKey{}, test.selectedNote)
			ctx, cancel := context.WithTimeout(requestCtx, garminSupportTimeout)
			start := time.Now()
			result, err := b.runGarminAI(ctx, nil, message, test.messages)
			cancel()
			r.Milliseconds = time.Since(start).Milliseconds()
			if err != nil {
				r.Error = true
				t.Errorf("%s: agent failed: %v", test.id, err)
			} else {
				r.Answer, r.Note, r.Suppressed = result.Answer, result.NoteName, result.Silent
				if test.wantSilent && !result.Silent {
					t.Errorf("%s: answered a resolved/staff-action message", test.id)
				}
				r.Tools = nil
				for _, turn := range result.Conversation[len(test.messages):] {
					for _, call := range turn.ToolCalls {
						r.Tools = append(r.Tools, call.Function.Name)
					}
				}
				if (test.wantAnswer || test.wantNote != "" || test.wantRelease || test.id == "chrome-download" || test.id == "unread-image") && (r.Answer == "" || result.Silent) {
					t.Errorf("%s: no useful answer", test.id)
				}
				if strings.HasPrefix(test.id, "rpc-setup-") && r.Answer != "" {
					lower := strings.ToLower(r.Answer)
					if test.id == "rpc-setup-variant-followup" && (strings.HasPrefix(lower, "which ") || strings.HasPrefix(lower, "are you ")) && strings.HasSuffix(strings.TrimSpace(lower), "?") && !strings.Contains(lower, "\n") {
						t.Error("known-variant follow-up asked another question instead of giving setup steps")
					}
					clarifying := test.id != "rpc-setup-variant-followup" && (strings.HasPrefix(lower, "which ") || strings.HasPrefix(lower, "are you ")) && strings.HasSuffix(strings.TrimSpace(lower), "?") && strings.Count(lower, "?") == 1 && !strings.ContainsAny(lower, ".!\n") && containsAnyGarminPhrase(lower, "variant", "platform", "android", "kmp") && !containsAnyGarminPhrase(lower, "invisible", "do not disturb", "dnd", "social sdk", "permissions")
					if !clarifying && (!strings.Contains(lower, "integrations") || !containsAnyGarminPhrase(lower, "log in", "login", "sign in", "authoriz")) {
						t.Error("related FAQ replaced the requested setup instructions")
					}
				}
				if !test.casual && containsAnyGarminPhrase(strings.ToLower(r.Answer), "if you want, i can", "let me know if", "then stop there") {
					t.Errorf("%s: reply contains a canned offer or writing instruction", test.id)
				}
				for _, forbidden := range test.forbidden {
					if strings.Contains(strings.ReplaceAll(strings.ToLower(r.Answer), "not "+forbidden, ""), forbidden) {
						t.Errorf("%s: review forbidden phrase %q", test.id, forbidden)
					}
				}
				if test.casual && len(r.Checks) != 0 {
					t.Error("non-support chat called Decider")
				}
				if strings.HasPrefix(test.id, "update-question") {
					for _, fact := range []string{"settings", "updater", "check for updates"} {
						if !strings.Contains(strings.ToLower(r.Answer), fact) {
							t.Errorf("update answer omitted %q", fact)
						}
					}
				}
				if strings.HasPrefix(test.id, "discover-") && (!strings.Contains(strings.ToLower(r.Answer), "daily discover") || !strings.Contains(strings.ToLower(r.Answer), "home")) {
					t.Error("Discover answer did not explain the actual Home section")
				}
				if test.id == "likes-auto-sync" && !strings.Contains(strings.ToLower(r.Answer), "auto-sync") {
					t.Error("sync answer omitted the actual control")
				}
				if test.wantNote != "" && r.Note != test.wantNote {
					t.Errorf("%s: note = %q, want %q", test.id, r.Note, test.wantNote)
				}
				if test.wantRelease && !containsAnyGarminPhrase(strings.Join(r.Tools, ","), "get_metrolist_status", "get_metrolist_changes") {
					t.Errorf("%s: expected release lookup and synthesis, got tools=%v note=%q", test.id, r.Tools, r.Note)
				}
				if test.id == "chrome-download" && !strings.Contains(strings.ToLower(r.Answer), "firefox") {
					t.Error("Chrome download case lost applicable Firefox advice")
				}
			}
			reports = append(reports, r)
		})
	}
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("METROBOT_SUPPORT_LIVE_REPORT"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
