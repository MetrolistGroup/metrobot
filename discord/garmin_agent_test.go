package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/firecrawl"
	gh "github.com/MetrolistGroup/metrobot/github"
	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/MetrolistGroup/metrobot/nanoreview"
	"github.com/MetrolistGroup/metrobot/technicalcity"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

type garminAITestFunc func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error)

type garminRoundTripFunc func(*http.Request) (*http.Response, error)

func (f garminRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f garminAITestFunc) Complete(ctx context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
	return f(ctx, request)
}

func TestFormatGarminAIUsage(t *testing.T) {
	result := &garminAIResult{
		ToolCalls:     3,
		Skills:        map[string]struct{}{"metrolist": {}, "support": {}},
		MemoryUpdated: true,
	}
	got := formatGarminAIUsage(result)
	want := "-# used 2 skills\n-# used 3 tools\n-# memory updated\n"
	if got != want {
		t.Fatalf("formatGarminAIUsage() = %q, want %q", got, want)
	}
}

func TestFormatAndTruncateGarminAIResultPreservesUsage(t *testing.T) {
	result := &garminAIResult{
		Answer:           strings.Repeat("é", garminAIMaxContent),
		ToolCalls:        1,
		Skills:           map[string]struct{}{},
		ThinkingDuration: 1500 * time.Millisecond,
	}
	got := formatAndTruncateGarminAIResult(result)
	if len(got) > garminAIMaxContent {
		t.Fatalf("response length = %d", len(got))
	}
	if !strings.HasPrefix(got, "-# thought briefly\n-# used 1 tools\n") || !strings.HasSuffix(got, "...") {
		t.Fatalf("formatted response = %q", got)
	}
	if got := formatGarminAIUsage(&garminAIResult{ThinkingDuration: 2500 * time.Millisecond}); got != "-# thought for 3s\n" {
		t.Fatalf("rounded thought duration = %q", got)
	}
}

func TestNormalizeGarminAIAnswer(t *testing.T) {
	got := normalizeGarminAIAnswer("-# used 9 tools\nThat is active — not abandoned.")
	if got != "That is active, not abandoned." {
		t.Fatalf("normalizeGarminAIAnswer() = %q", got)
	}
}

func TestNormalizeGarminAIAnswerStripsWakePhrase(t *testing.T) {
	for _, answer := range []string{
		"garmin, you triggered me, what's up?",
		"Garmin: what's up?",
		"GARMIN - what's up?",
	} {
		got := normalizeGarminAIAnswer(answer)
		if strings.HasPrefix(strings.ToLower(got), "garmin") {
			t.Errorf("normalizeGarminAIAnswer(%q) = %q", answer, got)
		}
	}
}

func TestNormalizeGarminAIAnswerStripsInternalMarkupAndMemoryOffers(t *testing.T) {
	tests := map[string]string{
		"<|mask_start|>\n\nbased on what i know, your favorite artist is camellia.\n\ndo you want me to save that as a permanent preference?\n\n<|mask_end|>": "based on what i know, your favorite artist is camellia.",
		"<function=remember_user_info>\n<parameter=category>\ninterest\n</parameter>\n<parameter=content>\ni like beans on toast\n</parameter>\n</function>":  "got it.",
		"useful. i can store that in your profile.": "useful.",
		"i can save that for future replies.":       "got it.",
		"i could save that for later.":              "got it.",
		"would you like this saved?":                "got it.",
		"let me save that for later.":               "got it.",
		"the app can save battery.":                 "the app can save battery.",
	}
	for input, want := range tests {
		if got := normalizeGarminAIAnswer(input); got != want {
			t.Errorf("normalizeGarminAIAnswer(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRunGarminAIAmbientModeCanReplyReactOrStaySilent(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{garminMemory: memory, garminAIContexts: make(map[string]garminAIContext), garminAIUserContexts: make(map[string]garminAIContext)}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !strings.Contains(request.Context, "active Metrobot conversation") {
			t.Fatal("ambient decision instruction was not supplied")
		}
		if got, want := garminToolNames(request.Tools), []string{"do_not_respond", "calculate_math", "load_skill", "load_support_guide"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ambient request tools = %v, want %v", got, want)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
			ID: "silent", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "do_not_respond", Arguments: `{}`},
		}}}}, nil
	})
	message := &discord.Message{GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000056")}, Content: "maybe never mind"}
	bot.rememberGarminAIContext("previous", message, []cmd.GarminAIMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hey"}})
	ambientToken := bot.tryBeginGarminAIAmbient(message)
	defer bot.endGarminAIAmbient(message, ambientToken)
	result, err := bot.runGarminAIWithMode(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "maybe never mind"}}, true, ambientToken)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Silent {
		t.Fatalf("ambient do_not_respond result = %#v", result)
	}
}

func garminDiscoveryTestBot(t *testing.T) *Bot {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	return &Bot{
		DB: database, Notes: &cmd.NotesHandler{DB: database}, garminMemory: memory,
		Config:          &config.Config{DiscordGuildID: "100000000000000012"},
		garminGitHub:    gh.NewAssistantClient("", "MetrolistGroup", "Metrolist"),
		garminFirecrawl: firecrawl.NewClient([]string{"fake"}), gsmarena: gsmarena.New(database),
		nanoreview: nanoreview.New(database), technicalCity: technicalcity.New(database),
	}
}

func TestRunGarminAIDiscoversAllAuthorizedToolsWithoutKeywords(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("discovery forced an unrelated lookup: %s", request.URL)
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
		t.Fatal("discovery fetched history")
		return "[]", nil
	})
	for _, prompt := range []string{"hey", "yes it does", "and?", "let's play 20 questions", "metrolist playback keeps stopping", "what is the latest Metrolist release?", "who owns NanoReview?", "the GSMArena website is interesting", "the GitHub user interface is interesting", "the GitHub projects interface is interesting", "we are planning offline music projects"} {
		t.Run(prompt, func(t *testing.T) {
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				want := garminToolNames(withoutGarminTools(garminAITools, "remember", "do_not_respond"))
				if got := garminToolNames(request.Tools); !reflect.DeepEqual(got, want) {
					t.Fatalf("keyword-free discovery = %v, want %v", got, want)
				}
				if strings.Count(request.SystemPrompt, "# Metrolist project reference") != 1 || strings.Contains(request.SystemPrompt, "# Metrolist support triage") {
					t.Fatal("normal chat must retain only its always-on project reference")
				}
				for _, name := range []string{"rpc", "login", "scrobbling", "playlists", "discovery"} {
					guide, _ := loadGarminSupportGuide(name)
					if strings.Contains(request.SystemPrompt, guide) {
						t.Fatalf("keyword-preloaded %s guide", name)
					}
				}
				for _, forced := range []string{"Call one of the supplied repository tools", "calculator tool before answering", "source tool before answering", "GSMArena tool before answering"} {
					if strings.Contains(request.Context, forced) {
						t.Fatalf("availability forced unrelated routing: %s", forced)
					}
				}
				if len(request.Messages) != 1 {
					t.Fatal("discovery ran a preflight")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "answer"}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			if err != nil || result.Answer != "answer" || result.ToolCalls != 0 || len(result.Skills) != 0 {
				t.Fatalf("discovery result=%+v, error=%v", result, err)
			}
		})
	}
}

func TestGarminPublicLookupsRejectSensitiveInputs(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("sensitive input reached a public lookup")
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	for _, test := range []struct{ request, query string }{
		{"login fails; token=private-canary", "private-canary"},
		{"login fails; password: private-canary", "login failure"},
		{"login fails; refresh_token is private-canary", "login failure"},
		{"My password hunter2 stopped working", "password hunter2"},
		{"my password private-canary stopped working", "password private-canary"},
		{"my password private-canary stopped working", "login failure"},
		{"my API key private-canary stopped working", "login failure"},
		{"my refresh token private-canary stopped working", "login failure"},
		{"my client_secret private-canary stopped working", "login failure"},
		{"my passphrase private-canary stopped working", "login failure"},
		{"my cookie private-canary stopped working", "login failure"},
		{"my credentials private-canary stopped working", "login failure"},
		{"my session key private-canary stopped working", "login failure"},
		{"my password swordfish stopped working", "login failure"},
		{"my password, swordfish, stopped working", "login help"},
		{"my API key;swordfish stopped working", "login help"},
		{"my client secret/swordfish stopped working", "login help"},
		{"my refresh token(swordfish) stopped working", "login help"},
		{"my access token-swordfish stopped working", "login help"},
		{"my id token.swordfish stopped working", "login help"},
		{"my session key|swordfish stopped working", "login help"},
		{"my passwd:swordfish stopped working", "login help"},
		{"my passphrase!swordfish stopped working", "login help"},
		{"login fails", "password,swordfish"},
		{"login fails", "api%20key%2Cswordfish"},
		{"password swordfish stopped working", "password swordfish"},
		{"login help; Bearer swordfish", "login help"},
		{"login help bearer swordfish", "bearer swordfish"},
		{"ring bearer duties", "ring bearer duties"}, // Ambiguous Bearer labels also fail closed.
		{"my cookie, swordfish", "login help"},
		{"my secret swordfish", "login help"},
		{"my-secret swordfish", "my-secret swordfish"},
		{"my_secret swordfish", "my_secret swordfish"},
		{"my.secret swordfish", "login help"},
		{"my\u200bsecret swordfish", "login help"},
		{"MY_APP_SECRET=swordfish", "login help"},
		{"My Secret Santa gift ideas", "gift ideas"}, // Possessive credential labels fail closed too.
		{"api key orchid stopped working", "api key orchid"},
		{"access token violet stopped working", "access token violet"},
		{"password reset instructions", "password reset instructions"}, // Ambiguous label/value forms fail closed.
		{"my API key swordfish stopped working", "login failure"},
		{"login fails", "password private-canary"},
		{"login fails", "password%20private-canary"},
		{"login fails", "password hunter2"},
		{"login fails", "password%20hunter2"},
		{"login fails", `password "swordfish"`},
		{"login fails", "Bearer tiny"},
		{"login fails", "Authorization Bearer tiny"},
		{"login fails", "Bearer hunter2"},
		{"login fails", `Bearer "tiny"`},
		{"login fails", "Basic dXNlcjpwYXNz"},
		{"login fails", "Basic dTo="},
		{"login fails", "Basic dTo"},
		{"login fails", `Basic "dXNlcjpwYXNz"`},
		{"login fails; my email is person@example.test", "login failure"},
		{"login fails", "Authorization: Bearer private-canary"},
		{"login fails", "https://example.test/callback?code=private-canary"},
		{"login fails", "token%3Dprivate-canary"},
	} {
		for _, name := range []string{"search_web", "search_metrolist_issues", "search_github_repositories", "get_github_user", "get_github_repository", "get_github_commits", "get_github_file", "get_gsmarena_phone", "get_nanoreview_device", "get_technicalcity_device"} {
			t.Run(name+"/"+test.request+"/"+test.query, func(t *testing.T) {
				message := garminHistoryTestMessage()
				message.Content = "garmin, " + test.request
				call := garminHistoryTestCall(name, mustJSON(map[string]string{
					"query": test.query, "username": test.query, "repository": "public/" + test.query, "path": test.query,
				})).Message.ToolCalls[0]
				output, _, _ := b.executeGarminAITool(context.Background(), nil, message, call)
				if !strings.Contains(garminToolResultError(output), "non-sensitive public subject") || strings.Contains(output, "private-canary") {
					t.Fatalf("public lookup was not safely rejected: %s", output)
				}
			})
		}
	}
	for _, explicit := range []bool{false, true} {
		message := garminHistoryTestMessage()
		prompt := "login fails; token=private-canary"
		if explicit {
			prompt = "search the web for " + prompt
		}
		message.Content = "garmin, " + prompt
		b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			if explicit {
				t.Fatal("credential-bearing explicit search was not rejected before synthesis")
			}
			if !garminToolAvailable(request.Tools, "search_web") {
				t.Fatal("safety validation must not restore keyword discovery gating")
			}
			return garminHistoryTestCall("search_web", `{"query":"private-canary"}`), nil
		})
		result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
		if err != nil || result.Answer != "web search failed just now." {
			t.Fatalf("sensitive search result=%+v, error=%v", result, err)
		}
	}
}

func TestGarminOrdinaryCredentialReportCannotReachGitHub(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("an ordinary credential report reached GitHub")
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !garminToolAvailable(request.Tools, "search_metrolist_issues") {
			t.Fatal("credential safety must not keyword-gate discovery")
		}
		return garminHistoryTestCall("search_metrolist_issues", `{"query":"password hunter2"}`), nil
	})
	message := garminHistoryTestMessage()
	message.Content = "garmin, My password hunter2 stopped working"
	result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "My password hunter2 stopped working"}})
	if err != nil || result.ToolCalls != 1 || result.Answer != "github lookup failed just now." {
		t.Fatalf("credential report was not safely rejected: result=%+v, error=%v", result, err)
	}
	if output := result.Conversation[len(result.Conversation)-1].Content; !strings.Contains(garminToolResultError(output), "non-sensitive public subject") || strings.Contains(output, "hunter2") {
		t.Fatalf("credential rejection lost its safe tool error: %s", output)
	}
}

func TestGarminOrdinaryPublicSubjectsRemainSearchable(t *testing.T) {
	for _, subject := range []string{"Secret Santa gift ideas", "Secret Garden book reviews", "basic arithmetic examples", "basic authentication documentation", "cookie recipes", "token ring networks"} {
		t.Run(subject, func(t *testing.T) {
			prompt := "search the web for " + subject
			searches := 0
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var args garminToolArgs
				if err := json.NewDecoder(request.Body).Decode(&args); err != nil || args.Query != prompt {
					t.Fatalf("unexpected public query: %q, error=%v", args.Query, err)
				}
				searches++
				body := `{"success":true,"data":{"web":[{"url":"https://example.org/source","markdown":"Public subject evidence"}]}}`
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Public subject evidence") {
					t.Fatal("ordinary subject lost its public lookup evidence")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "A sourced public answer."}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			if err != nil || searches != 1 || result.ToolCalls != 1 || result.Answer != "A sourced public answer." {
				t.Fatalf("ordinary subject was rejected: result=%+v, searches=%d, error=%v", result, searches, err)
			}
		})
	}
}

func TestGarminPublicLookupAllowsSafeGitHubForms(t *testing.T) {
	for _, test := range []struct {
		name, tool, source string
		args               garminToolArgs
		want               bool
	}{
		{"open issues", "search_metrolist_issues", "find open issues about playback", garminToolArgs{Query: "playback is:open"}, true},
		{"closed issues", "search_metrolist_issues", "find closed playback issues", garminToolArgs{Query: "is:closed playback"}, true},
		{"language qualifier", "search_github_repositories", "find Kotlin music repositories", garminToolArgs{Query: "music language:kotlin"}, true},
		{"account qualifier", "search_github_repositories", "find music repositories from example", garminToolArgs{Query: "music user:example"}, true},
		{"quoted label", "search_metrolist_issues", "find issues about playback failure", garminToolArgs{Query: `label:"playback failure" is:open`}, true},
		{"unclosed quote", "search_metrolist_issues", "find issues about playback failure", garminToolArgs{Query: `label:"playback failure is:open`}, false},
		{"private subject", "search_metrolist_issues", "yes", garminToolArgs{Query: "annex meeting is:open"}, false},
		{"appended private subject", "search_metrolist_issues", "find open playback issues", garminToolArgs{Query: "playback is:open annex"}, false},
		{"private account", "search_metrolist_issues", "find open playback issues", garminToolArgs{Query: "playback author:hidden-person is:open"}, false},
		{"unknown qualifier", "search_metrolist_issues", "find open playback issues", garminToolArgs{Query: "playback hidden-person:open"}, false},
		{"empty qualifier value", "search_metrolist_issues", "find open playback issues", garminToolArgs{Query: "playback author:"}, false},
		{"web remains literal", "search_web", "find open issues about playback", garminToolArgs{Query: "playback is:open"}, false},
		{"README shorthand", "get_github_file", "Read the README in owner/repo", garminToolArgs{Repository: "owner/repo", Path: "README.md"}, true},
		{"unknown file", "get_github_file", "Read the README in owner/repo", garminToolArgs{Repository: "owner/repo", Path: ".env"}, false},
		{"unknown subdirectory", "get_github_file", "Read the README in owner/repo", garminToolArgs{Repository: "owner/repo", Path: "hidden/README.md"}, false},
		{"unknown repository", "get_github_file", "Read the README in owner/repo", garminToolArgs{Repository: "hidden/repo", Path: "README.md"}, false},
		{"unknown ref", "get_github_file", "Read the README in owner/repo", garminToolArgs{Repository: "owner/repo", Path: "README.md", Ref: "hidden-branch"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := garminPublicLookupArgumentsAllowed(test.tool, test.args, []string{test.source}); got != test.want {
				t.Fatalf("authorized=%v, want %v", got, test.want)
			}
		})
	}
}

func TestRunGarminAIDerivedGitHubArgumentsReachLookup(t *testing.T) {
	for _, test := range []struct{ prompt, tool, args, evidence string }{
		{"find open issues about playback", "search_metrolist_issues", `{"query":"playback is:open"}`, "Playback issue"},
		{"Read the README in owner/repo", "get_github_file", `{"repository":"owner/repo","path":"README.md"}`, "# README"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			requests := 0
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				var body string
				switch request.URL.Path {
				case "/search/issues":
					if !strings.Contains(request.URL.Query().Get("q"), "playback is:open") {
						t.Fatal("validated GitHub qualifier was removed from the actual lookup")
					}
					body = `{"items":[{"title":"Playback issue","repository_url":"https://api.github.com/repos/MetrolistGroup/Metrolist"}]}`
				case "/repos/MetrolistGroup/Metrolist", "/repos/owner/repo":
					body = mustJSON(map[string]any{"full_name": strings.TrimPrefix(request.URL.Path, "/repos/"), "private": false})
				case "/repos/owner/repo/contents/README.md":
					body = `{"type":"file","name":"README.md","encoding":"base64","content":"IyBSRUFETUU="}`
				default:
					t.Fatalf("unexpected public lookup: %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls == 1 {
					return garminHistoryTestCall(test.tool, test.args), nil
				}
				last := request.Messages[len(request.Messages)-1]
				if garminToolResultError(last.Content) != "" || !strings.Contains(last.Content, test.evidence) {
					t.Fatalf("derived lookup was rejected or lost its evidence: %s", last.Content)
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Found the requested source."}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + test.prompt
			messages := []cmd.GarminAIMessage{{Role: "assistant", Content: "private annex meeting notes"}, {Role: "user", Content: test.prompt}}
			result, err := b.runGarminAI(context.Background(), nil, message, messages)
			if err != nil || calls != 2 || requests == 0 || result.ToolCalls != 1 || result.Answer != "Found the requested source." {
				t.Fatalf("derived lookup failed: result=%+v, requests=%d, error=%v", result, requests, err)
			}
		})
	}
}

func TestRunGarminAIDeviceSpecificationsRequireSourceEvidence(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("device discovery must not prefetch")
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, available := range []bool{false, true} {
		for _, test := range []struct{ prompt, tool string }{
			{"what are the Redmi 6A specs?", "get_gsmarena_phone"},
			{"show me the GSMArena page for Pixel 10", "get_gsmarena_phone"},
			{"show NanoReview specs for OnePlus 15", "get_nanoreview_device"},
			{"how much VRAM does RTX 5090 have?", "get_technicalcity_device"},
			{"compare RTX 4090 and RX 7900", "get_technicalcity_device"},
			{"compare Snapdragon 8 Elite and Dimensity 9400", "get_nanoreview_device"},
		} {
			t.Run(strconv.FormatBool(available)+"/"+test.tool, func(t *testing.T) {
				b := &Bot{}
				if available {
					b = garminDiscoveryTestBot(t)
				}
				calls := 0
				b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					calls++
					if calls == 1 && len(request.Messages) != 1 {
						t.Fatal("device data was fetched before model tool choice")
					}
					if garminToolAvailable(request.Tools, test.tool) != available {
						t.Fatal("device source availability did not match the configured service")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "INVENTED_SPEC: this device has 999 GB of memory."}}, nil
				})
				message := garminHistoryTestMessage()
				message.Content = "garmin, " + test.prompt
				result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: test.prompt}})
				if err != nil || result.Answer != "i couldn't verify those device specifications from a source." || result.ToolCalls != 0 {
					t.Fatalf("unsourced device answer accepted: result=%+v, error=%v", result, err)
				}
				for _, turn := range result.Conversation {
					if strings.Contains(turn.Content, "INVENTED_SPEC") {
						t.Fatal("rejected device draft survived in the conversation")
					}
				}
			})
		}
	}
}

func TestRunGarminAILoadsSkillsOnShortFollowup(t *testing.T) {
	for _, appSupport := range []bool{false, true} {
		t.Run(strconv.FormatBool(appSupport), func(t *testing.T) {
			full, err := loadGarminSkill("support")
			if err != nil {
				t.Fatal(err)
			}
			b := &Bot{}
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if !garminToolAvailable(request.Tools, "load_skill") || !garminToolAvailable(request.Tools, "load_support_guide") {
					t.Fatal("project reference or dedicated support core removed skill fallback")
				}
				if calls == 1 {
					return garminHistoryTestCall("load_skill", `{"name":"metrolist"}`), nil
				}
				if calls == 2 {
					project, _ := loadGarminSkill("metrolist")
					if request.Messages[len(request.Messages)-1].Content != project {
						t.Fatal("first skill was not actually loaded")
					}
					return garminHistoryTestCall("load_skill", `{"name":"support"}`), nil
				}
				if request.Messages[len(request.Messages)-1].Content != full {
					t.Fatal("on-demand support skill lost guide text or scope")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Which app version are you using?"}}, nil
			})
			message := garminHistoryTestMessage()
			if appSupport {
				message.ChannelID = snowflakeID(garminAppSupportID)
			}
			message.Content = "yes it does"
			messages := []cmd.GarminAIMessage{{Role: "user", Content: "help me set it up"}, {Role: "assistant", Content: "That isn't supported."}, {Role: "user", Content: message.Content}}
			result, err := b.runGarminAI(context.Background(), nil, message, messages)
			if err != nil || result.Silent || result.ToolCalls != 2 || calls != 3 {
				t.Fatalf("skill fallback result=%+v, calls=%d, error=%v", result, calls, err)
			}
			for _, name := range []string{"metrolist", "support"} {
				if _, ok := result.Skills[name]; !ok {
					t.Fatalf("loaded skill %s was not recorded", name)
				}
			}
		})
	}
}

func TestRunGarminAIDoesNotPrefetchChannelHistory(t *testing.T) {
	session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
		t.Fatal("casual chat fetched channel history before the model requested it")
		return "[]", nil
	})
	b := &Bot{}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !garminToolAvailable(request.Tools, "read_channel_history") || strings.Contains(request.Context, "Recent channel conversation") {
			t.Fatal("history should be available on demand, not prefetched")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "hey"}}, nil
	})
	message := garminHistoryTestMessage()
	message.Content = "garmin, hey"
	if _, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{garminAIUserMessage(message, "hey")}); err != nil {
		t.Fatal(err)
	}
}

func TestRunGarminAIStopsRepositoryToolLoop(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bot := &Bot{garminMemory: memory, garminGitHub: gh.NewAssistantClient("", "MetrolistGroup", "Metrolist")}
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		names := garminToolNames(request.Tools)
		if slices.Contains(names, "do_not_respond") || !slices.Contains(names, "get_github_commits") || !strings.Contains(request.Context, "Call one of the supplied repository tools") {
			t.Fatalf("repository turn tools = %v, context = %q", names, request.Context)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{
			ID: "repo", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "get_github_commits", Arguments: `{"repository":"MetrolistGroup/Metrolist"}`},
		}}}}, nil
	})
	message := &discord.Message{
		ID: snowflakeID("1"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "garmin, what changes were last made in the MetrolistGroup/Metrolist repo", Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "what changes were last made in the MetrolistGroup/Metrolist repo"}})
	if err != nil || calls != 1 || result.Answer != "github lookup failed just now." || result.ToolCalls != 1 {
		t.Fatalf("repository run = %#v, calls %d, error %v", result, calls, err)
	}
}

func TestRunGarminAIMakesWebSearchAvailableForAnswers(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{garminMemory: memory, garminFirecrawl: firecrawl.NewClient([]string{"key"})}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if got, want := garminToolNames(request.Tools), []string{"calculate_math", "search_web", "load_skill", "load_support_guide"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("general answer tools = %v, want %v", got, want)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "a useful short answer."}}, nil
	})
	message := &discord.Message{
		ID: snowflakeID("1"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "garmin, who coined the term quark?", Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "who coined the term quark?"}})
	if err != nil || result.Answer != "a useful short answer." {
		t.Fatalf("general answer = %#v, error %v", result, err)
	}
}

func TestRunGarminAIDirectlyExecutesExplicitWebSearch(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Query   string   `json:"query"`
			Limit   int      `json:"limit"`
			Sources []string `json:"sources"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode Firecrawl request: %v", err)
		}
		if payload.Query != "look up the latest story from the guardian" || payload.Limit != 5 || !reflect.DeepEqual(payload.Sources, []string{"news"}) {
			t.Fatalf("Firecrawl request = %#v", payload)
		}
		body := `{"success":true,"data":{"web":[{"url":"https://www.theguardian.com/example","title":"Latest story","markdown":"Story text"}]}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	calls := 0
	bot := &Bot{garminMemory: memory, garminFirecrawl: firecrawl.NewClient([]string{"key"})}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if !request.DisableReasoning || !garminToolAvailable(request.Tools, "search_web") || garminToolAvailable(request.Tools, "read_channel_history") || !strings.Contains(request.Context, "A live web search succeeded") {
			t.Fatalf("synthesis tools = %#v, context = %q", request.Tools, request.Context)
		}
		if len(request.Messages) < 3 || request.Messages[len(request.Messages)-2].Role != "assistant" || request.Messages[len(request.Messages)-1].Role != "tool" || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "https://www.theguardian.com/example") {
			t.Fatalf("synthesis messages = %#v", request.Messages)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "The latest story is sourced."}}, nil
	})
	message := &discord.Message{
		ID: snowflakeID("1"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "look up the latest story from the guardian", Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "look up the latest story from the guardian"}})
	if err != nil || calls != 1 || result.Answer != "The latest story is sourced." || result.ToolCalls != 1 {
		t.Fatalf("web run = %#v, calls %d, error %v", result, calls, err)
	}
}

func TestGarminPublicLookupInvocationMention(t *testing.T) {
	attempts := 0
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	session := garminHistoryTestSession(t, func(*http.Request) (string, error) { t.Fatal("lookup fetched Discord history"); return "[]", nil })
	const botID = "123456789012345678"
	session.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: snowflakeID(botID)}})
	for _, invocation := range []string{"<@" + botID + ">", "<@!" + botID + ">,", "garmin, <@" + botID + ">"} {
		for _, name := range []string{"search_web", "get_github_repository", "get_gsmarena_phone"} {
			message := garminHistoryTestMessage()
			message.Content = invocation + " search the web for Warsaw weather"
			before := attempts
			output, _, _ := b.executeGarminAITool(context.Background(), session, message, garminHistoryTestCall(name, `{"query":"Warsaw weather","repository":"example/player"}`).Message.ToolCalls[0])
			if attempts == before || strings.Contains(output, "non-sensitive public subject") {
				t.Fatalf("invocation %q blocked %s: %s", invocation, name, output)
			}
		}
	}
	message := garminHistoryTestMessage()
	message.Content = "<@" + botID + "> search for <@100000000000000056>"
	before := attempts
	output, _, _ := b.executeGarminAITool(context.Background(), session, message, garminHistoryTestCall("search_web", `{"query":"Warsaw weather"}`).Message.ToolCalls[0])
	if attempts != before || !strings.Contains(garminToolResultError(output), "non-sensitive public subject") {
		t.Fatalf("another user's mention was exempted: %s", output)
	}
	message.Content = "<@" + botID + "> search the web for Warsaw weather"
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		t.Fatal("failing explicit search should stop before synthesis")
		return nil, nil
	})
	result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: "search the web for Warsaw weather"}})
	if err != nil || result.Answer != "web search failed just now." || attempts == before {
		t.Fatalf("mention invocation did not reach the public search: result=%+v, error=%v", result, err)
	}
}

func TestRunGarminAIModelChoosesPhoneLookup(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetGSMArenaCache("index", []byte(`{"brands":{"1":"Xiaomi"},"records":[{"brand_id":1,"id":3,"model":"Redmi 6A","display":"Redmi 6A"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := database.SetGSMArenaCache("phone:3", []byte(`{"brand":"Xiaomi","id":3,"name":"Xiaomi Redmi 6A","slug":"xiaomi_redmi_6a-9217","url":"https://www.gsmarena.com/xiaomi_redmi_6a-9217.php","specs":[{"name":"Platform","items":[{"name":"Chipset","values":["Mediatek MT6761 Helio A22"]}]}]}`)); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		prompt     string
		draftFirst bool
	}{
		{"what are the specs of the redmi 6a?", false},
		{"what are the specs of the redmi 6a?", true},
		{"show me the GSMArena page for Redmi 6A", true},
	} {
		t.Run(test.prompt+"/"+strconv.FormatBool(test.draftFirst), func(t *testing.T) {
			calls, lookupRound := 0, 1
			if test.draftFirst {
				lookupRound = 2
			}
			answer := "The Redmi 6A uses a Helio A22."
			if strings.Contains(test.prompt, "GSMArena page") {
				answer = "https://www.gsmarena.com/xiaomi_redmi_6a-9217.php"
			}
			bot := &Bot{garminMemory: memory, gsmarena: gsmarena.New(database)}
			bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls == 1 && (!garminToolAvailable(request.Tools, "get_gsmarena_phone") || len(request.Messages) != 1) {
					t.Fatal("phone discovery prefetched data or omitted the tool")
				}
				if test.draftFirst && calls == 1 {
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "INVENTED_SPEC"}}, nil
				}
				for _, turn := range request.Messages {
					if strings.Contains(turn.Content, "INVENTED_SPEC") {
						t.Fatal("unsupported draft anchored the source retry")
					}
				}
				if calls == lookupRound {
					return garminHistoryTestCall("get_gsmarena_phone", `{"query":"Redmi 6A"}`), nil
				}
				if !request.DisableReasoning {
					t.Fatal("phone synthesis did not disable reasoning")
				}
				last := request.Messages[len(request.Messages)-1]
				if last.Role != "tool" || !strings.Contains(last.Content, "Xiaomi Redmi 6A") {
					t.Fatalf("phone synthesis messages = %#v", request.Messages)
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + test.prompt
			result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: test.prompt}})
			if err != nil || calls != lookupRound+1 || result.Answer != answer || result.ToolCalls != 1 {
				t.Fatalf("phone run = %#v, calls %d, error %v", result, calls, err)
			}
		})
	}
}

func TestGarminGSMArenaQueryPreservesFollowupSubject(t *testing.T) {
	messages := []cmd.GarminAIMessage{{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: "get_gsmarena_phone", Arguments: `{"query":"OnePlus 15"}`}}}}, {Role: "tool", Content: `{"source":"GSMArena"}`}, {Role: "assistant", Content: "The OnePlus 15 has a 7300 mAh battery."}, {Role: "user", Content: "what about its cameras?"}}
	if got := garminGSMArenaQuery(messages); got != "OnePlus 15 what about its cameras?" {
		t.Fatalf("follow-up query = %q", got)
	}
}

func TestRunGarminAIStopsAfterWebSearchFailure(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"success":false,"error":"credits exhausted"}`
		return &http.Response{StatusCode: http.StatusPaymentRequired, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	calls := 0
	bot := &Bot{garminMemory: memory, garminFirecrawl: firecrawl.NewClient([]string{"key"})}
	bot.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "invented result"}}, nil
	})
	message := &discord.Message{
		ID: snowflakeID("1"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "garmin, look up today's news", Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "look up today's news"}})
	if err != nil || calls != 0 || result.Answer != "web search failed just now." || result.ToolCalls != 1 {
		t.Fatalf("web failure = %#v, calls %d, error %v", result, calls, err)
	}
}

func TestRunGarminAITextualGitHubSearchRespectsAvailability(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, available := range []bool{false, true} {
		t.Run(strconv.FormatBool(available), func(t *testing.T) {
			calls := 0
			b := &Bot{}
			if available {
				b.garminGitHub = gh.NewAssistantClient("", "MetrolistGroup", "Metrolist")
			}
			b.garminAI = garminAITestFunc(func(_ context.Context, _ cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "got it. searching github for metrolist forks now. ```json\n{\n  \"tool\": \"github_search\",\n  \"query\": \"MetrolistGroup Metrolist forks\"\n}\n```"}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "do a github search for MetrolistGroup Metrolist forks"
			result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: message.Content}})
			if err != nil || calls != 1 {
				t.Fatalf("textual search=%+v, calls=%d, error=%v", result, calls, err)
			}
			if available {
				if result.ToolCalls != 1 || result.Answer != "github lookup failed just now." {
					t.Fatalf("available textual search was not executed: %+v", result)
				}
			} else if result.ToolCalls != 0 || !result.Silent || result.Answer != "" {
				t.Fatalf("unavailable textual search leaked or executed: %+v", result)
			}
		})
	}
}

func TestRunGarminAIUsesCalculatorBeforeAnswering(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bot := &Bot{garminMemory: memory}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls == 1 {
			if names := garminToolNames(request.Tools); !reflect.DeepEqual(names, []string{"calculate_math", "load_skill", "load_support_guide"}) || !strings.Contains(request.Context, "calculator tool") {
				t.Fatalf("calculator request = %#v", request)
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{
				ID: "math", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "calculate_math", Arguments: `{"expression":"3^3"}`},
			}}}}, nil
		}
		if !garminToolAvailable(request.Tools, "load_skill") || !strings.Contains(request.Messages[len(request.Messages)-1].Content, `"result":"27"`) {
			t.Fatalf("calculator result request = %#v", request)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "3³ is 27."}}, nil
	})
	message := &discord.Message{
		ID: snowflakeID("1"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "garmin, what's ³3", Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "what's ³3"}})
	if err != nil || calls != 2 || result.Answer != "3³ is 27." || result.ToolCalls != 1 {
		t.Fatalf("calculator run = %#v, calls %d, error %v", result, calls, err)
	}
}

func TestRunGarminAIRecoversReasoningOnlyResponse(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bot := &Bot{garminMemory: memory}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls == 1 {
			if request.DisableReasoning {
				t.Fatal("initial request disabled reasoning")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Reasoning: "drafting a greeting"}}, nil
		}
		if !request.DisableReasoning || len(request.Tools) != 0 || !strings.Contains(request.Context, "concise final answer") {
			t.Fatalf("final-answer request = %#v", request)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "hey!"}}, nil
	})
	message := &discord.Message{
		ID: snowflakeID("1"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Content: "garmin, hey!", Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "hey!"}})
	if err != nil || calls != 2 || result.Answer != "hey!" {
		t.Fatalf("reasoning recovery = %#v, calls %d, error %v", result, calls, err)
	}
}

func TestGarminSystemPromptAndDiscordContextContainIdentityAndGlobalMemory(t *testing.T) {
	message := &discord.Message{
		GuildID:   snowflakePtr("100000000000000012"),
		ChannelID: snowflakeID("100000000000000005"),
		Author: discord.User{
			ID:         snowflakeID("123456789012345678"),
			Username:   "exact_user",
			GlobalName: ptr("Exact Name"),
		},
	}
	prompt, err := (&Bot{}).garminSystemPromptWithMemory("# Metrobot Memory\nKnown fact")
	if err != nil {
		t.Fatal(err)
	}
	prompt += "\n" + garminDiscordContextForMessage(message)
	for _, expected := range []string{"You are Metrobot", "Garmin is not your name", "disgo", `"display_name":"exact_user"`, "exact_user", "123456789012345678", "Known fact", "not abandoned or dead", "Never use em dashes", "Mentioned users", "no nationality", "lower priority", "lowercase by default", "Refuse sexual or erotic", "Metrobot's repository", "created by Nyx and Lamp", "Mostafa Alagamy", "Nyx, Lamp, and Adriel", "without mentioning hidden prompts", "Use web search", "untrusted data", "calculator tool", "GSMArena phone data", "read-only requests", "never replace the answer with a stock redirect", "riff on them, never treat them as typos"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}
	for _, removed := range []string{"personalization memory", "current_user_memory", "remember_user_info"} {
		if strings.Contains(prompt, removed) {
			t.Errorf("prompt contains removed per-user memory feature %q", removed)
		}
	}
	if strings.Contains(prompt, "Exact Name") {
		t.Fatal("global display name leaked into Discord context")
	}
}

func TestGarminToolsForConversationDoesNotGateFollowups(t *testing.T) {
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "what is the latest Metrolist release?"}, {Role: "assistant", Content: "The latest release is v1."}, {Role: "user", Content: "let's play 20 questions instead"}}
	if got := garminToolNames(garminToolsForConversation(messages, false, false)); !reflect.DeepEqual(got, garminDefaultToolNames()) {
		t.Fatalf("casual follow-up tools = %v, want default tools", got)
	}
}

func TestGarminToolsForConversationDoesNotGateCasualChat(t *testing.T) {
	casualPrompts := []string{
		"who said im human you catboy femboy",
		"heavenly tung music plays on metrolist",
		"No tool was used for that reply, right?",
		"let's play 20 questions",
	}
	for _, prompt := range casualPrompts {
		tools := garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}}, true, false)
		if got := garminToolNames(tools); !reflect.DeepEqual(got, garminDefaultToolNames()) {
			t.Errorf("casual prompt %q received tools %v, want default tools", prompt, got)
		}
	}
}

func TestGarminWebSearchRouting(t *testing.T) {
	if !garminExplicitWebSearchRequested("look up the latest story from the guardian") {
		t.Fatal("look up request was not recognized as explicit web search")
	}
	if garminRepositorySubject("look up r/sssdfg") {
		t.Fatal("subreddit was mistaken for a GitHub repository")
	}
	if !garminRepositorySubject("look up user/repo") || !garminRepositorySubject("look up GitHub r/project") {
		t.Fatal("explicit GitHub repository references were mistaken for Reddit references")
	}
	for prompt, want := range map[string]bool{
		"find github projects for offline music":       true,
		"search github for an offline music project":   true,
		"the github projects interface is interesting": false,
		"find offline music projects":                  false,
	} {
		if got := garminRepositorySubject(prompt); got != want {
			t.Errorf("repository subject for %q = %v, want %v", prompt, got, want)
		}
	}
	for prompt, want := range map[string]string{
		"look up the latest story from the guardian":      "news",
		"give me the first moai meme image you find":      "images",
		"find the Kotlin Multiplatform guide":             "web",
		"give me a tenor link for a moai sunglasses meme": "web",
	} {
		if got := garminWebSearchSource(prompt); got != want {
			t.Errorf("source for %q = %q, want %q", prompt, got, want)
		}
	}
	query := garminWebSearchQuery([]cmd.GarminAIMessage{{Role: "user", Content: "give me a tenor link for a moai meme"}})
	if !strings.HasPrefix(query, "site:tenor.com ") {
		t.Fatalf("Tenor query = %q", query)
	}
	if !garminExplicitWebSearchRequested("look it up") {
		t.Fatal("look it up was not recognized as explicit search")
	}
	query = garminWebSearchQuery([]cmd.GarminAIMessage{{Role: "assistant", Content: "Install the Kotlin Multiplatform plugin."}, {Role: "user", Content: "give me a link to that plugin"}})
	if query != "" {
		t.Fatalf("subjectless plugin lookup should ask for a subject: %q", query)
	}
	query = garminWebSearchQuery([]cmd.GarminAIMessage{{Role: "user", Content: "get me an image of a moai"}, {Role: "assistant", Content: "I cannot search."}, {Role: "user", Content: "use google images yourself"}})
	if query != "" {
		t.Fatalf("subjectless image lookup should ask for a subject: %q", query)
	}
	if query := garminWebSearchQuery([]cmd.GarminAIMessage{{Role: "assistant", Content: "private channel details"}, {Role: "user", Content: "look this up"}}); query != "" {
		t.Fatalf("context-only lookup should ask for a public subject: %q", query)
	}
	if !garminWebSearchRequested("search the web for Discord messages API documentation") || garminWebSearchRequested("summarize the last 100 messages") {
		t.Fatal("explicit public search was confused with channel history")
	}
}

func TestGarminWebSearchQueryRequiresNamedSubject(t *testing.T) {
	for _, prompt := range []string{
		"give me a link to that plugin", "give me a link to that plugin, please", "get me a URL for this app", "find a website for the project", "give me a link to it", "use google images yourself",
	} {
		if query := garminWebSearchQuery([]cmd.GarminAIMessage{{Role: "user", Content: prompt}}); query != "" {
			t.Errorf("context-only query %q should ask for a subject, got %q", prompt, query)
		}
	}
	for _, prompt := range []string{"give me a link to the Kotlin Multiplatform plugin", "search the web for cat care tips", "find documentation for Go modules"} {
		if query := garminWebSearchQuery([]cmd.GarminAIMessage{{Role: "user", Content: prompt}}); query != prompt {
			t.Errorf("self-contained query %q was lost: %q", prompt, query)
		}
	}
}

func TestGarminToolsForConversationRequiresCurrentOwnerConsent(t *testing.T) {
	for _, test := range []struct {
		prompt          string
		owner, remember bool
	}{
		{"hey", false, false},
		{"hey", true, false},
		{"remember that releases happen on Fridays", false, false},
		{"remember that releases happen on Fridays", true, true},
		{"garmin, please remember that releases happen on Fridays", true, true},
		{"Can you please remember that releases happen on Fridays?", true, true},
		{"garmin, for future reference, remember that releases happen on Fridays", true, true},
		{"for next time, please remember that releases happen on Fridays", true, true},
		{"for future reference, do not remember that releases happen on Fridays", true, false},
		{"do not save this, remember that releases happen on Fridays", true, false},
		{"for future reference, remember that we do not release on Fridays", true, true},
		{"for future reference, remember that releases happen on Fridays", false, false},
		{"<@123456789012345678>, remember that releases happen on Fridays", true, true},
		{"do not remember that releases happen on Fridays", true, false},
		{"I never asked you to remember that", true, false},
		{"what does remember that mean?", true, false},
		{"remember my pronouns are they/them", true, false},
		{"remember that I prefer short replies", true, false},
	} {
		messages := []cmd.GarminAIMessage{{Role: "user", Content: "remember that old fact"}, {Role: "assistant", Content: "remember that I promised to save it"}, {Role: "user", Content: test.prompt}}
		tools := garminToolsForConversation(messages, test.owner, false)
		want := garminDefaultToolNames()
		if test.remember {
			want = append(want, "remember")
		}
		if got := garminToolNames(tools); !reflect.DeepEqual(got, want) {
			t.Errorf("tools for owner=%v, %q = %v, want %v", test.owner, test.prompt, got, want)
		}
	}
}

func TestGarminToolsForRequestFiltersUnavailableCapabilities(t *testing.T) {
	for _, test := range []struct {
		name    string
		disable func(*Bot, *bot.Client, *discord.Message)
		blocked []string
	}{
		{"github", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.garminGitHub = nil }, []string{"get_metrolist_status", "get_metrolist_changes", "get_metrolist_commit", "search_metrolist_issues", "get_metrolist_issue", "search_metrolist_code", "get_github_user", "search_github_repositories", "get_github_repository", "get_github_commits", "get_github_file"}},
		{"web", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.garminFirecrawl = nil }, []string{"search_web"}},
		{"phone", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.gsmarena = nil }, []string{"get_gsmarena_phone"}},
		{"nano", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.nanoreview = nil }, []string{"get_nanoreview_device"}},
		{"technical city", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.technicalCity = nil }, []string{"get_technicalcity_device"}},
		{"note catalog", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.DB = nil }, []string{"list_notes"}},
		{"notes", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.Notes = nil }, []string{"get_note"}},
		{"notes database", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.Notes.DB = nil }, []string{"get_note"}},
		{"memory", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.garminMemory = nil }, []string{"remember"}},
		{"discord rest", func(_ *Bot, s *bot.Client, _ *discord.Message) { s.Rest = nil }, []string{"react_to_message", "list_discord_emojis", "view_discord_emoji", "get_discord_profile", "search_discord_members", "read_channel_history", "read_community_channel"}},
		{"discord cache", func(_ *Bot, s *bot.Client, _ *discord.Message) { s.Caches = nil }, []string{"read_channel_history"}},
		{"guild config", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.Config = nil }, []string{"get_discord_profile", "search_discord_members"}},
		{"invalid guild config", func(b *Bot, _ *bot.Client, _ *discord.Message) { b.Config.DiscordGuildID = "invalid" }, []string{"get_discord_profile", "search_discord_members"}},
		{"no consent", func(_ *Bot, _ *bot.Client, m *discord.Message) { m.Content = "hey" }, []string{"remember"}},
		{"not an owner", func(_ *Bot, _ *bot.Client, m *discord.Message) { m.Author.ID = snowflakeID("100000000000000056") }, []string{"remember"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := garminDiscoveryTestBot(t)
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) { t.Fatal("discovery fetched history"); return "[]", nil })
			message := garminHistoryTestMessage()
			message.Author.ID = snowflakeID(garminNyxID)
			message.Content = "for future reference, remember that releases happen on Fridays"
			messages := []cmd.GarminAIMessage{{Role: "user", Content: message.Content}}
			test.disable(b, session, message)
			want := garminToolNames(withoutGarminTools(garminAITools, test.blocked...))
			if got := garminToolNames(b.garminToolsForRequest(session, message, messages, false)); !reflect.DeepEqual(got, want) {
				t.Fatalf("capability filter = %v, want %v", got, want)
			}
		})
	}
}

func TestRunGarminAIInvalidDiscordSessionKeepsOnlyUsableTools(t *testing.T) {
	for _, session := range []*bot.Client{nil, {}} {
		b := &Bot{garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			want := []string{"calculate_math", "load_skill", "load_support_guide"}
			if got := garminToolNames(request.Tools); !reflect.DeepEqual(got, want) {
				t.Fatalf("unusable capabilities advertised: %v, want %v", got, want)
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "hey"}}, nil
		})}
		message := garminHistoryTestMessage()
		message.Content = "garmin, hey"
		if _, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: "hey"}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGarminAppSupportCapabilityAllowlist(t *testing.T) {
	b := garminDiscoveryTestBot(t)
	session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
		t.Fatal("app support performed a keyword-triggered community lookup")
		return "[]", nil
	})
	message := garminHistoryTestMessage()
	message.ChannelID = snowflakeID(garminAppSupportID)
	message.Author.ID = snowflakeID(garminNyxID)
	message.Content = "remember that minky is in coolchannel; what is the KMP preview status?"
	messages := []cmd.GarminAIMessage{{Role: "user", Content: message.Content}}
	want := []string{"do_not_respond", "get_metrolist_status", "get_metrolist_changes", "get_metrolist_commit", "search_metrolist_issues", "get_metrolist_issue", "search_metrolist_code", "list_notes", "get_note", "load_skill", "load_support_guide"}
	if got := garminToolNames(b.garminToolsForRequest(session, message, messages, false)); !reflect.DeepEqual(got, want) {
		t.Fatalf("support capability scope = %v, want %v", got, want)
	}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if len(request.Messages) != 1 {
			t.Fatal("app support prefetched a disallowed source")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: `react_to_message emoji="👍"`}}, nil
	})
	result, err := b.runGarminAI(context.Background(), session, message, messages)
	if err != nil || !result.Silent || result.ToolCalls != 0 || result.Interacted {
		t.Fatalf("support text action bypassed scope: result=%+v, error=%v", result, err)
	}
	for _, name := range []string{"search_web", "read_channel_history", "read_community_channel", "get_discord_profile", "search_discord_members", "remember"} {
		output, _, updated := b.executeGarminAITool(context.Background(), session, message, garminHistoryTestCall(name, `{}`).Message.ToolCalls[0])
		if !strings.Contains(garminToolResultError(output), "unavailable to the app-support agent") || updated {
			t.Fatalf("direct support %s bypassed scope: %s", name, output)
		}
	}
}

func TestGarminGSMArenaToolFollowsPhoneSpecContext(t *testing.T) {
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "what are the OnePlus 15 specs?"}, {Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: "get_gsmarena_phone"}}}}, {Role: "tool", Content: `{"phone":{"name":"OnePlus 15"}}`}, {Role: "assistant", Content: "It uses a Snapdragon chipset."}, {Role: "user", Content: "what about battery?"}}
	want := garminDefaultToolNames()
	if got := garminToolNames(garminToolsForConversation(messages, false, false)); !reflect.DeepEqual(got, want) {
		t.Fatalf("GSMArena follow-up tools = %v, want %v", got, want)
	}
}

func TestReadGarminChannelBacklogIncludesImagesAndHonorsCutoff(t *testing.T) {
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}, Roles: []discord.Role{{ID: snowflakeID("100000000000000036"), Name: "she/her"}}}})
	session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("limit") != "20" || request.URL.Query().Get("before") != "200" {
			t.Fatalf("backlog query = %s",
				request.URL.RawQuery)
		}
		body := `[{"id":"199","channel_id":"100000000000000005","content":"new context","author":{"id":"100000000000000056","username":"user"},"member":{"nick":"Server User","roles":["100000000000000036"]},"attachments":[{"id":"100000000000000016","filename":"context.png","content_type":"image/png","url":"https://cdn.example/context.png"}]},{"id":"149","channel_id":"100000000000000005","content":"old secret","author":{"id":"100000000000000028","username":"old"}}]`

		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})

	bot := &Bot{Config: &config.Config{DiscordGuildID: "100000000000000012"}, garminContextCutoffs: map[string]string{"100000000000000005": "150"}}
	output, err := bot.readGarminChannelMessages(session, "100000000000000005", "200", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "new context") || !strings.Contains(output, `"display_name":"Server User"`) || !strings.Contains(output, `"pronouns":["she/her"]`) || strings.Contains(output, "old secret") {
		t.Fatalf("identity-rich cutoff-aware backlog = %s", output)
	}
	images := garminAIToolImageURLs("read_community_channel", output)
	if !reflect.DeepEqual(images, []string{"https://cdn.example/context.png"}) {
		t.Fatalf("backlog images = %v", images)
	}
}

func TestGarminReadableChannelForConversation(t *testing.T) {
	tests := map[string]string{
		"do you think metrolist kmp was fake all along?": "sneak-peeks",
		"what was posted in sneak-peeks?":                "sneak-peeks",
		"what are maintainers saying in coolchannel?":    "coolchannel",
		"what is the latest poll about app design?":      "polls",
		"show me a picture of minky":                     "minky",
		"hello":                                          "",
	}
	for prompt, want := range tests {
		got := garminReadableChannelForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}})
		if got != want {
			t.Errorf("channel for %q = %q, want %q", prompt, got, want)
		}
	}
}

func garminDefaultToolNames() []string {
	return []string{"react_to_message", "list_discord_emojis", "view_discord_emoji", "do_not_respond", "calculate_math", "get_nanoreview_device", "get_technicalcity_device", "get_gsmarena_phone", "search_web", "get_metrolist_status", "get_metrolist_changes", "get_metrolist_commit", "search_metrolist_issues", "get_metrolist_issue", "search_metrolist_code", "get_github_user", "search_github_repositories", "get_github_repository", "get_github_commits", "get_github_file", "list_notes", "get_note", "get_discord_profile", "search_discord_members", "read_channel_history", "read_community_channel", "load_skill", "load_support_guide"}
}

func garminToolNames(tools []cmd.GarminAITool) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

func TestGarminSystemPromptUsesNicknameWithoutMemberUser(t *testing.T) {
	message := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"),
		Author: discord.User{ID: snowflakeID("123456789012345678"), Username: "n7e3", GlobalName: ptr("Nyx")},
		Member: &discord.Member{Nick: ptr("Nyxie")},
	}
	prompt := garminDiscordContextForMessage(message)
	if !strings.Contains(prompt, `"display_name":"Nyxie"`) {
		t.Fatalf("prompt did not use nickname: %s", prompt)
	}
}

func TestGarminDiscordContextIncludesRepliedMessage(t *testing.T) {
	message := &discord.Message{
		Author: discord.User{ID: snowflakeID("100000000000000048"), Username: "speaker"},
		ReferencedMessage: &discord.Message{
			ID: snowflakeID("100000000000000038"), Content: "the thing we were discussing", Author: discord.User{ID: snowflakeID("100000000000000002"), Username: "metrobot"},
		},
	}
	context := garminDiscordContextForMessage(message)
	if !strings.Contains(context, "the thing we were discussing") {
		t.Fatalf("reply context missing message content: %s", context)
	}
}

func TestGarminDiscordContextIncludesChannelRolesAndPronouns(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")},
		Roles: []discord.Role{{ID: snowflakeID("100000000000000042"), Name: "they/them"}, {ID: snowflakeID("100000000000000043"), Name: "team"}}},
		Members: []discord.Member{{
			User: discord.User{ID: snowflakeID("876543210987654321"), Username: "history_user"}, Nick: ptr("History User"), RoleIDs: []snowflake.ID{snowflakeID("100000000000000043")},
		}},
	})
	state.Caches.AddChannel(testChannel(t, map[string]any{"id": garminGeneralID, "guild_id": "100000000000000012", "name": "general", "topic": "community chat"}))

	session := state
	message := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID(garminGeneralID),
		Author: discord.User{ID: snowflakeID("123456789012345678"), Username: "speaker"},
		Member: &discord.Member{RoleIDs: []snowflake.ID{snowflakeID("100000000000000042"), snowflakeID("100000000000000043")}},
	}
	context := (&Bot{}).garminDiscordContextForConversation(session, message, []cmd.GarminAIMessage{{Role: "assistant", Name: garminAIConversationSummaryName, Content: "earlier conversation" + garminAIConversationParticipantsPrefix + "discord_876543210987654321"}, {Role: "user", Name: "discord_123456789012345678", Content: "current message"}})
	for _, expected := range []string{`"name":"general"`, `"name":"they/them"`, `"pronouns":["they/them"]`, `"tracked_conversation_users"`, `"discord_876543210987654321"`, `"display_name":"History User"`, "brief, direct, useful", "continued bot chat", "#bots"} {
		if !strings.Contains(context, expected) {
			t.Errorf("context missing %q: %s", expected, context)
		}
	}
}

func TestGarminAIToolImageURLs(t *testing.T) {
	output := `{"messages":[{"attachments":[{"filename":"preview.png","content_type":"image/png","url":"https://cdn.discordapp.com/preview.png"},{"filename":"notes.txt","content_type":"text/plain","url":"https://cdn.discordapp.com/notes.txt"}]}]}`
	got := garminAIToolImageURLs("read_community_channel", output)
	want := []string{"https://cdn.discordapp.com/preview.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("garminAIToolImageURLs() = %v, want %v", got, want)
	}
}

func TestGarminAISilentAnswer(t *testing.T) {
	for _, answer := range []string{"do_not_respond", "`do_not_respond`", "do not respond"} {
		if !garminAISilentAnswer(answer) {
			t.Errorf("garminAISilentAnswer(%q) = false", answer)
		}
	}
	if garminAISilentAnswer("i do not respond to spam") {
		t.Fatal("ordinary sentence was treated as silence sentinel")
	}
}

func TestGarminSkillsLoad(t *testing.T) {
	for _, name := range []string{"metrolist", "support"} {
		content, err := loadGarminSkill(name)
		if err != nil || content == "" {
			t.Fatalf("loadGarminSkill(%q) = %q, %v", name, content, err)
		}
		if name == "support" && (!strings.Contains(content, "Firefox") || !strings.Contains(content, "Huorong")) {
			t.Fatalf("support skill is missing verified app-support answers: %q", content)
		}
	}
	if _, err := loadGarminSkill("unknown"); err == nil {
		t.Fatal("loadGarminSkill(unknown) error = nil")
	}
}

func TestDiscordMemberToolResultKeepsNameFieldsSeparate(t *testing.T) {
	member := &discord.Member{
		Nick: ptr("Server Name"),
		User: discord.User{
			ID:         snowflakeID("123456789012345678"),
			Username:   "login_name",
			GlobalName: ptr("Global Name"),
		},
	}
	result := discordMemberToolResult(member)
	if result["username"] != "login_name" || result["server_nickname"] != "Server Name" || result["display_name"] != "Server Name" {
		t.Fatalf("discordMemberToolResult() = %#v", result)
	}
	if _, present := result["global_name"]; present {
		t.Fatalf("discordMemberToolResult() exposed global name: %#v", result)
	}
}

func TestGarminToolSchemasAreValidJSON(t *testing.T) {
	for _, tool := range garminAITools {
		if !json.Valid(tool.Function.Parameters) {
			t.Errorf("tool %s has invalid schema %q", tool.Function.Name, tool.Function.Parameters)
		}
		if tool.Function.Name == "remember_user_info" || tool.Function.Name == "forget_user_info" {
			t.Errorf("removed per-user memory tool %q is still registered", tool.Function.Name)
		}
	}
}

func TestParseGarminTextReactions(t *testing.T) {
	content := "react_to_message message_id=1538281518466867410 reaction=\"👍\"\n" +
		"react_to_message message_id=1538281518466867410 reaction=\"❤️\"\n" +
		"react_to_message emoji=\"speed\""
	if got, want := parseGarminTextReactions(content), []string{"👍", "❤️", "speed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("text reactions = %v, want %v", got, want)
	}
}

func TestParseGarminTextRepositoryToolCall(t *testing.T) {
	for name, test := range map[string]struct {
		content  string
		toolName string
		argument string
		value    string
	}{
		"plain": {
			content: `search_github_repositories query="android music"`, toolName: "search_github_repositories", argument: "query", value: "android music",
		},
		"function markup": {
			content:  "<function=get_github_repository><parameter=repository>MetrolistGroup/Metrolist</parameter></function>",
			toolName: "get_github_repository", argument: "repository", value: "MetrolistGroup/Metrolist",
		},
		"tool-call json": {
			content:  `<tool_call>{"name":"search_github_repositories","arguments":{"query":"kotlin player"}}</tool_call>`,
			toolName: "search_github_repositories", argument: "query", value: "kotlin player",
		},
	} {
		t.Run(name, func(t *testing.T) {
			call, ok := parseGarminTextRepositoryToolCall(test.content, "fallback")
			if !ok || call.Function.Name != test.toolName {
				t.Fatalf("parsed call = %#v, %v", call, ok)
			}
			var arguments map[string]string
			if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil || arguments[test.argument] != test.value {
				t.Fatalf("arguments = %#v, %v", arguments, err)
			}
		})
	}
}

func TestSanitizeGarminInternalToolDisclosure(t *testing.T) {
	answer := "it means do not respond. i use it for spam, bait, or messages that genuinely need no reply"
	got := sanitizeGarminInternalToolDisclosure("what does dnr mean", answer)
	want := `usually, DNR means "do not resuscitate," a medical instruction. context can change what the acronym means.`
	if got != want {
		t.Fatalf("sanitized disclosure = %q, want %q", got, want)
	}
}

func TestGarminDiscoveryToolTextIsNotDisclosed(t *testing.T) {
	for _, name := range []string{"load_skill", "load_support_guide", "list_notes", "get_note", "get_discord_profile", "search_discord_members", "read_community_channel"} {
		if got := sanitizeGarminInternalToolDisclosure("which tool?", "i use "+name); strings.Contains(got, name) {
			t.Errorf("internal tool disclosure escaped: %s", got)
		}
		if got := normalizeGarminAIAnswer(name + ` name="support"`); strings.Contains(got, name) {
			t.Errorf("tool syntax escaped: %s", got)
		}
	}
}

func TestListNotesToolIncludesDescriptions(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote("playback", "Playback stops unexpectedly", "Restart the app."); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: database}
	output, _, _ := bot.executeGarminAITool(context.Background(), nil, &discord.Message{}, cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{
		Name: "list_notes", Arguments: `{}`,
	}})
	if !strings.Contains(output, `"name":"playback"`) || !strings.Contains(output, `"short_desc":"Playback stops unexpectedly"`) {
		t.Fatalf("list_notes output = %s", output)
	}
}

func TestRememberToolRequiresOwner(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{
		DB:           database,
		Config:       &config.Config{PermaAdminDiscordIDs: []string{"admin"}},
		garminMemory: memory,
	}
	call := cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{
		Name:      "remember",
		Arguments: `{"content":"durable fact"}`,
	}}

	request := func(userID, content string) *discord.Message {
		return &discord.Message{Author: discord.User{ID: snowflakeID(userID)}, Content: content}
	}
	output, _, updated := bot.executeGarminAITool(context.Background(), nil, request("100000000000000056", "garmin, remember that durable fact"), call)
	if updated || !strings.Contains(output, "only Nyx and Lamp") {
		t.Fatalf("non-owner remember = (%q, %v)", output, updated)
	}
	content, _ := memory.Read()
	if strings.Contains(content, "durable fact") {
		t.Fatal("non-admin updated memory")
	}

	for _, content := range []string{"garmin, hello", "garmin, do not remember that durable fact", "garmin, I never asked you to remember that", "garmin, for future reference, do not remember that durable fact"} {
		output, _, updated = bot.executeGarminAITool(context.Background(), nil, request(garminNyxID, content), call)
		if updated || !strings.Contains(output, "explicit request") {
			t.Fatalf("implicit or denied owner consent %q = (%q, %v)", content, output, updated)
		}
	}

	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request(garminNyxID, "garmin, remember that I like music"), call)
	if updated || !strings.Contains(output, "non-personal") {
		t.Fatalf("personal memory = (%q, %v)", output, updated)
	}

	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request(garminNyxID, "garmin, for future reference, remember that durable fact"), call)
	if !updated || !strings.Contains(output, `"saved":true`) {
		t.Fatalf("admin remember = (%q, %v)", output, updated)
	}
	content, _ = memory.Read()
	if !strings.Contains(content, "durable fact") {
		t.Fatal("admin memory update was not saved")
	}
}

func TestGarminPronounsFromRoles(t *testing.T) {
	roles := []map[string]string{{"id": "1", "name": "she/her"}, {"id": "2", "name": "maintainer"}}
	if got := garminPronounsFromRoles(roles); !reflect.DeepEqual(got, []string{"she/her"}) {
		t.Fatalf("garminPronounsFromRoles() = %v", got)
	}
}

func TestGarminChannelDescriptionsUseResolvedIDs(t *testing.T) {
	for channelID, expected := range map[string]string{
		garminGeneralID:    "guide continued bot chat to #bots",
		garminBotsID:       "preferred channel",
		garminPollsID:      "polls",
		garminMinkyID:      "Minky",
		garminAppSupportID: "verified support-skill answers",
	} {
		if got := garminChannelDescription(channelID); !strings.Contains(got, expected) {
			t.Errorf("garminChannelDescription(%q) = %q, want substring %q", channelID, got, expected)
		}
	}
}
