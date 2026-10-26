package discord

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestGarminPunctuationSeparatedCredentialsBlockExplicitSearch(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("credential-bearing request reached public lookup: %s", request.URL.Host)
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		t.Fatal("credential-bearing explicit search was not rejected before synthesis")
		return nil, nil
	})
	for _, label := range []string{"password", "passwd", "passphrase", "api key", "api_key", "api-key", "client secret", "access token", "refresh token", "id token", "session key"} {
		for _, separator := range []string{", ", ";", "/", "(", "|", "\n", ""} {
			t.Run(label+separator, func(t *testing.T) {
				prompt := "search the web for login help; my " + label + separator + ", swordfish, stopped working"
				message := garminHistoryTestMessage()
				message.Content = "garmin, " + prompt
				result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
				if err != nil || result.Answer != "web search failed just now." || result.ToolCalls != 1 {
					t.Fatalf("sensitive search was not rejected: result=%+v, error=%v", result, err)
				}
			})
		}
	}
}

func TestGarminGitHubSearchSubjectRequiresContiguousProvenance(t *testing.T) {
	for _, tool := range []string{"search_metrolist_issues", "search_github_repositories"} {
		for _, query := range []string{"annex meeting is:open", `"annex meeting" is:open`, `label:"annex meeting" is:open`, `topic:"annex meeting"`, "annex is:open meeting", "meeting annex", "annex -meeting"} {
			for _, sources := range [][]string{
				{"find annex repositories about a meeting"},
				{`{"description":"Public annex guide; a different meeting agenda"}`},
				{"annex", "meeting"},
			} {
				if garminPublicLookupArgumentsAllowed(tool, garminToolArgs{Query: query}, sources) {
					t.Errorf("%s authorized reconstructed subject %q from %v", tool, query, sources)
				}
			}
		}
		for _, query := range []string{"annex meeting is:open", `"annex meeting" is:open`, "is:open annex meeting is:issue"} {
			if !garminPublicLookupArgumentsAllowed(tool, garminToolArgs{Query: query}, []string{"find annex meeting issues"}) {
				t.Errorf("%s rejected evidenced subject %q", tool, query)
			}
		}
	}
}

func TestGarminFreshPublicWordsCannotReconstructRetainedPrivatePhrase(t *testing.T) {
	for _, tool := range []string{"search_metrolist_issues", "search_github_repositories"} {
		t.Run(tool, func(t *testing.T) {
			requests := 0
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.URL.Path != "/search/repositories" || request.URL.Query().Get("q") != "music is:public" {
					t.Fatalf("private subject made an outbound request: %s", request.URL)
				}
				body := `{"items":[{"full_name":"example/player","private":false,"description":"An annex guide and an unrelated meeting agenda"}]}`
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				switch calls {
				case 1:
					return garminHistoryTestCall("search_github_repositories", `{"query":"music"}`), nil
				case 2:
					last := request.Messages[len(request.Messages)-1].Content
					if !strings.Contains(last, "annex") || !strings.Contains(last, "meeting") || strings.Contains(last, "annex meeting") {
						t.Fatal("fixture did not return the public words separately")
					}
					completion := garminHistoryTestCall(tool, `{"query":"annex meeting is:open"}`)
					quoted := garminHistoryTestCall(tool, `{"query":"label:\"annex meeting\" is:open"}`).Message.ToolCalls[0]
					quoted.ID = "quoted-private-subject"
					completion.Message.ToolCalls = append(completion.Message.ToolCalls, quoted)
					return completion, nil
				default:
					for _, turn := range request.Messages[len(request.Messages)-2:] {
						if !strings.Contains(garminToolResultError(turn.Content), "current user request") {
							t.Fatal("private phrase was not rejected before execution")
						}
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Please name the public subject."}}, nil
				}
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, find music repositories"
			messages := []cmd.GarminAIMessage{{Role: "assistant", Content: "The private annex meeting moved to Thursday."}, {Role: "user", Content: "find music repositories"}}
			result, err := b.runGarminAI(context.Background(), nil, message, messages)
			if err != nil || calls != 3 || requests != 1 || result.ToolCalls != 1 || result.Answer != "Please name the public subject." {
				t.Fatalf("private phrase escaped: result=%+v, calls=%d, requests=%d, error=%v", result, calls, requests, err)
			}
		})
	}
}

func TestGarminHardwareComparisonRetriesWithBothSourceResults(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("comparison escaped local source fixtures: %s", request.URL)
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	for _, fixture := range []struct{ query, name, slug string }{
		{"RTX 4090", "GeForce RTX 4090", "gpu/GeForce-RTX-4090"},
		{"RX 7900", "Radeon RX 7900 XTX", "gpu/Radeon-RX-7900-XTX"},
	} {
		device := map[string]any{
			"name": fixture.name, "slug": fixture.slug, "url": "https://technical.city/en/" + fixture.slug,
			"specs": []map[string]any{{"name": "Memory", "items": []map[string]any{{"name": "Capacity", "values": []string{"24 GB"}}}}},
		}
		for key, body := range map[string]string{
			"tc:search:" + strings.ToLower(fixture.query): mustJSON([]any{device}),
			"tc:device:" + fixture.slug:                   mustJSON(device),
		} {
			if err := b.DB.SetGSMArenaCache(key, []byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls == 1 {
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "INVENTED_SPEC"}}, nil
		}
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "INVENTED_SPEC") {
				t.Fatal("unsourced comparison draft survived its source retry")
			}
		}
		if calls == 2 {
			completion := garminHistoryTestCall("get_technicalcity_device", `{"query":"RTX 4090"}`)
			other := garminHistoryTestCall("get_technicalcity_device", `{"query":"RX 7900"}`).Message.ToolCalls[0]
			other.ID = "second-comparison-source"
			completion.Message.ToolCalls = append(completion.Message.ToolCalls, other)
			return completion, nil
		}
		for index, name := range []string{"GeForce RTX 4090", "Radeon RX 7900 XTX"} {
			output := request.Messages[len(request.Messages)-2+index].Content
			if garminToolResultError(output) != "" || !strings.Contains(output, name) || !strings.Contains(output, "24 GB") {
				t.Fatalf("comparison lost a verified source: %s", output)
			}
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Both matched GPU sources list 24 GB of memory."}}, nil
	})
	message := garminHistoryTestMessage()
	message.Content = "garmin, compare RTX 4090 and RX 7900"
	result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "compare RTX 4090 and RX 7900"}})
	if err != nil || calls != 3 || result.ToolCalls != 2 || result.Answer != "Both matched GPU sources list 24 GB of memory." {
		t.Fatalf("comparison source retry failed: result=%+v, calls=%d, error=%v", result, calls, err)
	}
}

func TestGarminRequestedDeviceLookupPrecedesAutomaticCommunityRead(t *testing.T) {
	for _, test := range []struct {
		prompt, tool, query string
		draftFirst          bool
	}{
		{"show minky updates and Redmi 6A specs", "get_gsmarena_phone", "Redmi 6A", false},
		{"show minky updates and NanoReview specs for OnePlus 15", "get_nanoreview_device", "OnePlus 15", true},
	} {
		t.Run(test.tool, func(t *testing.T) {
			const privateText = "Private minky update: annex meeting on Thursday"
			var order []string
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected public request: %s", request.URL)
				return nil, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			for key, body := range map[string]string{
				"index":                        `{"brands":{"1":"Xiaomi"},"records":[{"brand_id":1,"id":3,"model":"Redmi 6A","display":"Redmi 6A"}]}`,
				"phone:3":                      `{"id":3,"name":"Xiaomi Redmi 6A","slug":"xiaomi_redmi_6a-9217","url":"https://www.gsmarena.com/xiaomi_redmi_6a-9217.php","specs":[{"name":"Battery","items":[{"name":"Capacity","values":["3000 mAh"]}]}]}`,
				"nano:search:oneplus 15":       `[{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15"}]`,
				"nano:detail:phone/oneplus-15": `{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15","specs":[{"name":"Battery","items":[{"name":"Capacity","values":["7300 mAh"]}]}]}`,
			} {
				if err := b.DB.SetGSMArenaCache(key, []byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
				t.Fatal("mixed request fetched unrelated current-channel history")
				return "[]", nil
			})
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/channels/"+garminMinkyID+"/messages") {
					t.Fatalf("unexpected community lookup: %s", request.URL)
				}
				order = append(order, "community")
				messages := garminHistoryTestMessages(1, privateText)
				messages[0]["channel_id"] = garminMinkyID
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(mustJSON(messages))), Request: request}, nil
			})})
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				step := calls
				if test.draftFirst {
					if calls == 1 {
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "INVENTED_SPEC"}}, nil
					}
					step--
				}
				if step == 1 {
					if len(order) != 0 || strings.Contains(request.Context, privateText) || !garminToolAvailable(request.Tools, test.tool) || garminToolAvailable(request.Tools, "read_community_channel") {
						t.Fatal("community retrieval prevented the requested public device lookup")
					}
					order = append(order, "device")
					completion := garminHistoryTestCall("read_community_channel", `{"channel":"minky"}`)
					private := garminHistoryTestCall(test.tool, `{"query":"annex meeting"}`).Message.ToolCalls[0]
					private.ID = "private-device-subject"
					device := garminHistoryTestCall(test.tool, mustJSON(map[string]string{"query": test.query})).Message.ToolCalls[0]
					completion.Message.ToolCalls = append(completion.Message.ToolCalls, private, device)
					return completion, nil
				}
				if step == 2 {
					if !reflect.DeepEqual(order, []string{"device"}) || strings.Contains(request.Context, privateText) || !garminToolAvailable(request.Tools, test.tool) {
						t.Fatal("community data closed public follow-up lookups too early")
					}
					if !strings.Contains(garminToolResultError(request.Messages[len(request.Messages)-3].Content), "unavailable for this request") || !strings.Contains(garminToolResultError(request.Messages[len(request.Messages)-2].Content), "current user request") {
						t.Fatal("premature history or private device subject was not rejected")
					}
					if last := request.Messages[len(request.Messages)-1].Content; garminToolResultError(last) != "" || !strings.Contains(last, "mAh") {
						t.Fatalf("device evidence missing: %s", last)
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
				}
				if !reflect.DeepEqual(order, []string{"device", "community"}) || !strings.Contains(request.Context, privateText) {
					t.Fatalf("missing or reordered sources: order=%v, context=%s", order, request.Context)
				}
				for _, turn := range request.Messages {
					if turn.Content == "UNDISPATCHED_DRAFT" || turn.Content == "INVENTED_SPEC" {
						t.Fatal("pre-community or unsupported draft survived into synthesis")
					}
				}
				for _, tool := range request.Tools {
					if garminPublicQueryToolName(tool.Function.Name) {
						t.Fatalf("%s available after private retrieval", tool.Function.Name)
					}
				}
				if step == 3 && !test.draftFirst {
					return garminHistoryTestCall("search_metrolist_issues", `{"query":"annex meeting"}`), nil
				}
				if !test.draftFirst && !strings.Contains(garminToolResultError(request.Messages[len(request.Messages)-1].Content), "unavailable for this request") {
					t.Fatal("private community text was allowed into public search")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Sourced battery specifications and the minky update."}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + test.prompt
			messages := []cmd.GarminAIMessage{{Role: "assistant", Content: "Private old subject: annex meeting"}, {Role: "user", Content: test.prompt}}
			result, err := b.runGarminAI(context.Background(), session, message, messages)
			if err != nil || calls != 4 || result.ToolCalls != 2 || result.Answer != "Sourced battery specifications and the minky update." {
				t.Fatalf("mixed request failed: result=%+v, calls=%d, error=%v", result, calls, err)
			}
		})
	}
}

func TestGarminRequestedPublicLookupsPrecedeCommunityRead(t *testing.T) {
	for _, test := range []struct {
		prompt string
		tools  []string
	}{
		{"show minky updates and identify GitHub user octocat", []string{"get_github_user"}},
		{"show minky updates and tell me who octocat is on GitHub", []string{"get_github_user"}},
		{"show minky updates and search GitHub issues about playback", []string{"search_metrolist_issues"}},
		{"show minky updates and check Metrolist playback reports", []string{"search_metrolist_issues"}},
		{"show minky updates, identify GitHub user octocat and search playback issues", []string{"get_github_user", "search_metrolist_issues"}},
		{"show minky updates", nil},
		{"show minky, identify GitHub user octocat, search playback issues and find music repositories", []string{"get_github_user", "search_metrolist_issues", "search_github_repositories"}},
	} {
		t.Run(test.prompt, func(t *testing.T) {
			const privateText = "Private community annex meeting"
			var order []string
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body string
				switch request.URL.Path {
				case "/users/octocat":
					order = append(order, "get_github_user")
					body = `{"login":"octocat","bio":"Public lookup evidence"}`
				case "/repos/MetrolistGroup/Metrolist":
					body = `{"full_name":"MetrolistGroup/Metrolist","private":false}`
				case "/search/issues":
					if query := request.URL.Query().Get("q"); !strings.HasSuffix(query, "playback is:open") {
						t.Fatalf("unexpected issue query: %s", query)
					}
					order = append(order, "search_metrolist_issues")
					body = `{"items":[{"title":"Public lookup evidence","repository_url":"https://api.github.com/repos/MetrolistGroup/Metrolist"}]}`
				case "/search/repositories":
					if query := request.URL.Query().Get("q"); query != "music is:public" {
						t.Fatalf("unexpected repository query: %s", query)
					}
					order = append(order, "search_github_repositories")
					body = `{"items":[{"full_name":"example/player","private":false,"description":"Public lookup evidence"}]}`
				default:
					t.Fatalf("unexpected or private public lookup: %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
				t.Fatal("mixed request read unrelated current-channel history")
				return "[]", nil
			})
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(request.URL.Path, "/channels/"+garminMinkyID+"/messages") || !reflect.DeepEqual(order, test.tools) {
					t.Errorf("community lookup preceded requested public evidence: path=%s, order=%v", request.URL.Path, order)
				}
				order = append(order, "community")
				messages := garminHistoryTestMessages(1, privateText)
				messages[0]["channel_id"] = garminMinkyID
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(mustJSON(messages))), Request: request}, nil
			})})
			b := garminDiscoveryTestBot(t)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls <= len(test.tools) {
					tool := test.tools[calls-1]
					if !garminToolAvailable(request.Tools, tool) || garminToolAvailable(request.Tools, "read_community_channel") || strings.Contains(request.Context, privateText) {
						t.Fatal("automatic community data blocked a requested public lookup")
					}
					args := `{"username":"octocat"}`
					switch tool {
					case "search_metrolist_issues":
						args = `{"query":"playback is:open"}`
					case "search_github_repositories":
						args = `{"query":"music"}`
					}
					completion := garminHistoryTestCall("read_community_channel", `{"channel":"minky"}`)
					completion.Message.ToolCalls = append(completion.Message.ToolCalls, garminHistoryTestCall(tool, args).Message.ToolCalls...)
					return completion, nil
				}
				if calls == len(test.tools)+1 && calls < garminAIMaxToolRounds {
					if strings.Contains(request.Context, privateText) || !garminToolAvailable(request.Tools, "get_github_user") {
						t.Fatal("community retrieval closed the public tool phase too early")
					}
					if len(test.tools) == 0 {
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Reasoning: "Ready to summarize the community data."}}, nil
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
				}
				for _, tool := range request.Tools {
					if garminPublicQueryToolName(tool.Function.Name) {
						t.Fatalf("%s remained available after private retrieval", tool.Function.Name)
					}
				}
				if !strings.Contains(request.Context, privateText) || (len(test.tools) > 0 && !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Public lookup evidence")) {
					t.Fatal("mixed request lost public or community evidence")
				}
				for _, turn := range request.Messages {
					if turn.Content == "UNDISPATCHED_DRAFT" {
						t.Fatal("pre-community draft survived into synthesis")
					}
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Public lookup evidence and minky updates."}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + test.prompt
			messages := []cmd.GarminAIMessage{{Role: "assistant", Content: "Private prior account: private-annex"}, {Role: "user", Content: test.prompt}}
			result, err := b.runGarminAI(context.Background(), session, message, messages)
			if err != nil || calls != min(len(test.tools)+2, garminAIMaxToolRounds) || result.ToolCalls != len(test.tools)+1 || result.Answer != "Public lookup evidence and minky updates." || !reflect.DeepEqual(order, append(append([]string(nil), test.tools...), "community")) {
				t.Fatalf("mixed lookup failed: result=%+v, calls=%d, order=%v, error=%v", result, calls, order, err)
			}
		})
	}
}

func TestGarminCommunityPreloadRechecksCancellationAndVisibility(t *testing.T) {
	for _, reset := range []bool{false, true} {
		b := &Bot{garminAI: garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			t.Fatal("cancelled or reset community data reached the provider")
			return nil, nil
		})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
			t.Fatal("community request fetched current-channel history")
			return "[]", nil
		})
		session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(request.URL.Path, "/channels/"+garminMinkyID+"/messages") {
				t.Fatalf("unexpected community lookup: %s", request.URL)
			}
			if reset {
				if err := b.resetGarminContext(garminHistoryTestChannel, "1001"); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`)), Request: request}, nil
		})})
		message := garminHistoryTestMessage()
		message.Content = "garmin, show minky updates"
		result, err := b.runGarminAI(ctx, session, message, []cmd.GarminAIMessage{{Role: "user", Content: "show minky updates"}})
		if reset {
			if err != nil || !result.Silent {
				t.Fatalf("reset request was not suppressed: result=%+v, error=%v", result, err)
			}
		} else if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled request returned %v", err)
		}
	}
}

func TestGarminRequestedPublicLookupsPrecedeOnDemandHistory(t *testing.T) {
	for _, lookup := range []struct{ prompt, tool, args string }{
		{"summarize the last 20 messages and find Metrolist issue reports about playback", "search_metrolist_issues", `{"query":"playback is:open"}`},
		{"summarize the last 20 messages and identify GitHub user octocat", "get_github_user", `{"username":"octocat"}`},
	} {
		for _, batch := range []bool{false, true} {
			t.Run(lookup.tool+map[bool]string{false: "/separate rounds", true: "/history-first batch"}[batch], func(t *testing.T) {
				const privateText = "Private channel annex meeting"
				var order []string
				original := http.DefaultTransport
				http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					var body string
					switch request.URL.Path {
					case "/users/octocat":
						order = append(order, "public")
						body = `{"login":"octocat","bio":"Public lookup evidence"}`
					case "/repos/MetrolistGroup/Metrolist":
						body = `{"full_name":"MetrolistGroup/Metrolist","private":false}`
					case "/search/issues":
						if !strings.HasSuffix(request.URL.Query().Get("q"), "playback is:open") {
							t.Fatalf("unexpected issue query: %s", request.URL)
						}
						order = append(order, "public")
						body = `{"items":[{"title":"Public lookup evidence","repository_url":"https://api.github.com/repos/MetrolistGroup/Metrolist"}]}`
					default:
						t.Fatalf("unexpected or private public lookup: %s", request.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})
				t.Cleanup(func() { http.DefaultTransport = original })
				session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
					if !reflect.DeepEqual(order, []string{"public"}) {
						t.Errorf("private history preceded the requested public lookup: %v", order)
					}
					order = append(order, "history")
					return mustJSON(garminHistoryTestMessages(1, privateText)), nil
				})
				b := garminDiscoveryTestBot(t)
				calls, readyRound := 0, 3
				if batch {
					readyRound = 2
				}
				b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					calls++
					if calls == 1 {
						if !garminToolAvailable(request.Tools, "read_channel_history") || !garminToolAvailable(request.Tools, lookup.tool) {
							t.Fatal("authorized tools must remain discoverable without keyword loading")
						}
						completion := garminHistoryTestCall("read_channel_history", `{"limit":20}`)
						if batch {
							completion.Message.ToolCalls = append(completion.Message.ToolCalls, garminHistoryTestCall(lookup.tool, lookup.args).Message.ToolCalls...)
						}
						return completion, nil
					}
					if calls < readyRound {
						if len(order) != 0 || !garminToolAvailable(request.Tools, lookup.tool) || !strings.Contains(request.Messages[len(request.Messages)-1].Content, `"deferred":true`) {
							t.Fatal("the first history call closed public lookups before their turn")
						}
						return garminHistoryTestCall(lookup.tool, lookup.args), nil
					}
					if calls == readyRound {
						if !reflect.DeepEqual(order, []string{"public"}) || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Public lookup evidence") {
							t.Fatal("public tool did not execute before private retrieval")
						}
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
					}
					for _, tool := range request.Tools {
						if garminPublicQueryToolName(tool.Function.Name) {
							t.Fatalf("%s remained available after actual private retrieval", tool.Function.Name)
						}
					}
					if !strings.Contains(request.Messages[len(request.Messages)-1].Content, privateText) || !reflect.DeepEqual(order, []string{"public", "history"}) {
						t.Fatal("deferred history was lost before synthesis")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Public lookup evidence and the history summary."}}, nil
				})
				message := garminHistoryTestMessage()
				message.Content = "garmin, " + lookup.prompt
				result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: lookup.prompt}})
				if err != nil || calls != readyRound+1 || result.ToolCalls != 2 || result.Answer != "Public lookup evidence and the history summary." {
					t.Fatalf("mixed on-demand history failed: result=%+v, calls=%d, order=%v, error=%v", result, calls, order, err)
				}
			})
		}
	}
}

func TestGarminRequestedCommitsPrecedePrivateHistory(t *testing.T) {
	for _, mode := range []string{"history first", "history first batch", "metadata before commits", "direct follow-up"} {
		t.Run(mode, func(t *testing.T) {
			const prompt = "summarize the last 20 messages and show recent commits in owner/repo"
			const privateText = "Private channel annex meeting"
			var order []string
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body string
				switch request.URL.Path {
				case "/repos/owner/repo":
					body = `{"full_name":"owner/repo","private":false}`
				case "/repos/owner/repo/commits":
					order = append(order, "commits")
					body = `[{"sha":"sourcecommit","commit":{"message":"Public commit evidence"}}]`
				default:
					t.Fatalf("unexpected or private public lookup: %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
				if !reflect.DeepEqual(order, []string{"commits"}) {
					t.Fatalf("private history preceded requested commits: %v", order)
				}
				order = append(order, "history")
				return mustJSON(garminHistoryTestMessages(1, privateText)), nil
			})
			b := garminDiscoveryTestBot(t)
			calls, historyRound := 0, 3
			if mode == "history first batch" {
				historyRound = 2
			}
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls < historyRound {
					if !garminToolAvailable(request.Tools, "get_github_commits") || garminToolAvailable(request.Tools, "read_channel_history") {
						t.Fatal("requested public data must precede private history")
					}
					if calls == 1 {
						completion := garminHistoryTestCall("read_channel_history", `{"limit":20}`)
						if mode == "metadata before commits" {
							metadata := garminHistoryTestCall("get_github_repository", `{"repository":"owner/repo"}`).Message.ToolCalls[0]
							completion.Message.ToolCalls = append([]cmd.GarminAIToolCall{metadata}, completion.Message.ToolCalls...)
						} else if mode == "history first batch" {
							commits := garminHistoryTestCall("get_github_commits", `{"repository":"owner/repo"}`).Message.ToolCalls[0]
							completion.Message.ToolCalls = append(completion.Message.ToolCalls, commits)
						}
						return completion, nil
					}
					if garminToolResultError(request.Messages[len(request.Messages)-1].Content) == "" {
						t.Fatal("premature history read was not rejected")
					}
					return garminHistoryTestCall("get_github_commits", `{"repository":"owner/repo"}`), nil
				}
				if calls == historyRound {
					if last := request.Messages[len(request.Messages)-1].Content; !strings.Contains(last, "Public commit evidence") || !garminToolAvailable(request.Tools, "read_channel_history") {
						t.Fatalf("requested commits did not unlock private retrieval: %s", last)
					}
					completion := garminHistoryTestCall("read_channel_history", `{"limit":20}`)
					private := garminHistoryTestCall("get_github_commits", `{"repository":"private/annex"}`).Message.ToolCalls[0]
					private.ID = "private-history-subject"
					completion.Message.ToolCalls = append(completion.Message.ToolCalls, private)
					return completion, nil
				}
				if mode == "history first batch" && calls == 3 {
					if !garminToolAvailable(request.Tools, "get_github_commits") || !reflect.DeepEqual(order, []string{"commits"}) {
						t.Fatal("queued history closed public lookups before the public phase finished")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
				}
				for _, tool := range request.Tools {
					if garminPublicQueryToolName(tool.Function.Name) {
						t.Fatalf("%s remained available after private history", tool.Function.Name)
					}
				}
				privateRejected := false
				for _, message := range request.Messages {
					if message.ToolCallID == "private-history-subject" && strings.Contains(garminToolResultError(message.Content), "current user request") {
						privateRejected = true
					}
				}
				if !privateRejected || !strings.Contains(request.Messages[len(request.Messages)-1].Content, privateText) {
					t.Fatal("private retrieval lost its data or public-query provenance guard")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Public commit evidence and the private history summary."}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			if mode == "direct follow-up" {
				message.Content = prompt
			}
			result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			wantTools := 2
			if mode == "metadata before commits" {
				wantTools++
			}
			if err != nil || calls != 4 || result.ToolCalls != wantTools || result.Answer != "Public commit evidence and the private history summary." || !reflect.DeepEqual(order, []string{"commits", "history"}) {
				t.Fatalf("mixed repository/history request failed: result=%+v, calls=%d, order=%v, error=%v", result, calls, order, err)
			}
		})
	}
}

func TestGarminDraftCannotCloseRequiredPublicPhase(t *testing.T) {
	for _, community := range []bool{false, true} {
		for _, reasoning := range []bool{false, true} {
			name := map[bool]string{false: "current history", true: "community"}[community] + map[bool]string{false: "/ordinary draft", true: "/reasoning draft"}[reasoning]
			t.Run(name, func(t *testing.T) {
				const privateText = "Private channel annex meeting"
				var order []string
				original := http.DefaultTransport
				http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					var body string
					switch request.URL.Path {
					case "/repos/owner/repo":
						body = `{"full_name":"owner/repo","private":false}`
					case "/repos/owner/repo/commits":
						if len(order) != 0 {
							t.Fatal("commits were fetched after private history")
						}
						order = append(order, "commits")
						body = `[{"sha":"sourcecommit","commit":{"message":"Public commit evidence"}}]`
					default:
						t.Fatalf("unexpected public lookup: %s", request.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})
				t.Cleanup(func() { http.DefaultTransport = original })
				historyBody := func() string {
					if !reflect.DeepEqual(order, []string{"commits"}) {
						t.Fatalf("private history preceded commits: %v", order)
					}
					order = append(order, "history")
					messages := garminHistoryTestMessages(1, privateText)
					if community {
						messages[0]["channel_id"] = garminMinkyID
					}
					return mustJSON(messages)
				}
				session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
					if community {
						t.Fatal("community request read unrelated current-channel history")
					}
					return historyBody(), nil
				})
				transport := session.Rest.HTTPClient().Transport
				session.Rest.HTTPClient().Transport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					if community && strings.HasSuffix(request.URL.Path, "/channels/"+garminMinkyID+"/messages") {
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(historyBody())), Request: request}, nil
					}
					return transport.RoundTrip(request)
				})
				b := garminDiscoveryTestBot(t)
				calls := 0
				b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					calls++
					if calls <= 2 {
						if len(order) != 0 || strings.Contains(request.Context, privateText) || !garminToolAvailable(request.Tools, "get_github_commits") || garminToolAvailable(request.Tools, "read_channel_history") {
							t.Fatal("draft closed the public phase before required commits")
						}
						if calls == 1 {
							message := cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}
							if reasoning {
								message = cmd.GarminAIMessage{ReasoningContent: "UNSUPPORTED_REASONING"}
							}
							return &cmd.GarminAICompletion{Message: message}, nil
						}
						return garminHistoryTestCall("get_github_commits", `{"repository":"owner/repo"}`), nil
					}
					for _, message := range request.Messages {
						if strings.Contains(message.Content, "UNSUPPORTED_DRAFT") || strings.Contains(message.ReasoningContent, "UNSUPPORTED_REASONING") {
							t.Fatal("unsupported draft survived into synthesis")
						}
					}
					if calls == 3 {
						if !reflect.DeepEqual(order, []string{"commits"}) || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Public commit evidence") || !garminToolAvailable(request.Tools, "get_github_commits") {
							t.Fatal("public phase lost its source evidence or closed early")
						}
						if !community {
							return garminHistoryTestCall("read_channel_history", `{"limit":20}`), nil
						}
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
					}
					if calls != garminAIMaxToolRounds || len(request.Tools) != 0 || !request.DisableReasoning || !reflect.DeepEqual(order, []string{"commits", "history"}) {
						t.Fatal("combined answer lost its final synthesis round")
					}
					publicFound, privateFound := false, strings.Contains(request.Context, privateText)
					for _, message := range request.Messages {
						publicFound = publicFound || strings.Contains(message.Content, "Public commit evidence")
						privateFound = privateFound || strings.Contains(message.Content, privateText)
						if message.Content == "UNDISPATCHED_DRAFT" {
							t.Fatal("pre-history draft survived into synthesis")
						}
					}
					if !publicFound || !privateFound {
						t.Fatal("combined answer lost a required source")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Public commit evidence and the history summary."}}, nil
				})
				prompt := "summarize the last 20 messages and show latest commits in owner/repo"
				if community {
					prompt = "show Minky updates and latest commits in owner/repo"
				}
				message := garminHistoryTestMessage()
				message.Content = "garmin, " + prompt
				result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
				if err != nil || calls != 4 || result.ToolCalls != 2 || result.Answer != "Public commit evidence and the history summary." {
					t.Fatalf("draft recovery failed: result=%+v, calls=%d, error=%v", result, calls, err)
				}
			})
		}
	}
}

func TestGarminUnavailableDeviceSourceCannotUnlockPrivateHistory(t *testing.T) {
	session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
		t.Fatal("unavailable device source unlocked private history")
		return "[]", nil
	})
	b := &Bot{garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if garminToolAvailable(request.Tools, "read_community_channel") || garminToolAvailable(request.Tools, "read_channel_history") {
			t.Fatal("private tools became available before required device evidence")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
	})}
	const prompt = "show Minky updates and NanoReview page for OnePlus 15"
	message := garminHistoryTestMessage()
	message.Content = "garmin, " + prompt
	result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
	if err != nil || result.ToolCalls != 0 || result.Answer != "i couldn't verify those device specifications from a source." {
		t.Fatalf("unavailable source did not fail closed: result=%+v, error=%v", result, err)
	}
}

func TestGarminMissingPublicEvidenceFailsClosedWithinBudget(t *testing.T) {
	for _, mode := range []string{"repeated draft", "reasoning only", "tool budget", "late draft"} {
		t.Run(mode, func(t *testing.T) {
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				t.Fatalf("missing-source request made an unexpected lookup: %s", request.URL)
				return nil, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) {
				t.Fatal("missing public evidence unlocked private history")
				return "[]", nil
			})
			b := garminDiscoveryTestBot(t)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if !garminToolAvailable(request.Tools, "get_github_commits") || garminToolAvailable(request.Tools, "read_community_channel") {
					t.Fatal("missing public evidence closed public tools or opened private tools")
				}
				if mode == "late draft" && calls < garminAIMaxToolRounds-1 {
					return garminHistoryTestCall("calculate_math", `{"expression":"3^3"}`), nil
				}
				switch mode {
				case "reasoning only":
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Reasoning: "UNSUPPORTED_REASONING"}}, nil
				case "tool budget":
					return garminHistoryTestCall("calculate_math", `{"expression":"3^3"}`), nil
				default:
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
				}
			})
			const prompt = "show Minky updates and latest commits in owner/repo"
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			wantCalls := 2
			if mode == "tool budget" || mode == "late draft" {
				wantCalls = garminAIMaxToolRounds - 1
			}
			if err != nil || calls != wantCalls || result.Answer != "i couldn't retrieve all requested public sources, so i can't verify the combined answer." {
				t.Fatalf("missing-source request did not fail closed: result=%+v, calls=%d, error=%v", result, calls, err)
			}
			for _, turn := range result.Conversation {
				if strings.Contains(turn.Content, "UNSUPPORTED_DRAFT") || strings.Contains(turn.Reasoning, "UNSUPPORTED_REASONING") {
					t.Fatal("unsupported draft survived the source failure")
				}
			}
		})
	}
}

func TestGarminExplicitGitHubProjectSearchRequiresLookup(t *testing.T) {
	searches := 0
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/search/repositories" || request.URL.Query().Get("q") != "offline music is:public" {
			t.Fatalf("unexpected public lookup: %s", request.URL)
		}
		searches++
		body := `{"items":[{"full_name":"example/offline-player","private":false,"description":"Offline music evidence"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b := garminDiscoveryTestBot(t)
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		for _, turn := range request.Messages {
			if turn.Content == "INVENTED_PROJECTS" {
				t.Fatal("unsupported project draft survived its retry")
			}
		}
		if calls == 1 {
			if len(request.Messages) != 1 || !strings.Contains(request.Context, "Call one of the supplied repository tools") {
				t.Fatal("explicit GitHub project search lost its lookup requirement or forced a preflight")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "INVENTED_PROJECTS"}}, nil
		}
		if calls == 2 {
			if !garminToolAvailable(request.Tools, "search_github_repositories") {
				t.Fatal("source retry removed repository search")
			}
			return garminHistoryTestCall("search_github_repositories", `{"query":"offline music"}`), nil
		}
		if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Offline music evidence") {
			t.Fatal("project search evidence was lost before synthesis")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Found example/offline-player."}}, nil
	})
	const prompt = "find GitHub projects for offline music"
	message := garminHistoryTestMessage()
	message.Content = "garmin, " + prompt
	result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
	if err != nil || calls != 3 || searches != 1 || result.ToolCalls != 1 || result.Answer != "Found example/offline-player." {
		t.Fatalf("explicit project search failed: result=%+v, calls=%d, searches=%d, error=%v", result, calls, searches, err)
	}
}

func TestGarminNamedGitHubCommitDiscoveryRequiresLookup(t *testing.T) {
	for _, mode := range []string{"source retry", "repeated draft"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				var body string
				switch request.URL.Path {
				case "/search/repositories":
					if request.URL.Query().Get("q") != "Metrolist is:public" {
						t.Fatalf("unexpected repository query: %s", request.URL)
					}
					body = `{"items":[{"full_name":"MetrolistGroup/Metrolist","private":false}]}`
				case "/repos/MetrolistGroup/Metrolist":
					body = `{"full_name":"MetrolistGroup/Metrolist","private":false}`
				case "/repos/MetrolistGroup/Metrolist/commits":
					body = `[{"sha":"sourcecommit","commit":{"message":"Public commit evidence"}}]`
				default:
					t.Fatalf("unexpected public request: %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				for _, turn := range request.Messages {
					if turn.Content == "INVENTED_COMMITS" {
						t.Fatal("unsupported commit draft survived its retry")
					}
				}
				if calls == 1 || mode == "repeated draft" {
					if !strings.Contains(request.Context, "Use get_github_commits") {
						t.Fatal("named GitHub commit discovery did not require current commit evidence")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "INVENTED_COMMITS"}}, nil
				}
				if calls == 2 {
					return garminHistoryTestCall("search_github_repositories", `{"query":"Metrolist"}`), nil
				}
				if calls == 3 {
					if !strings.Contains(request.Context, "Use get_github_commits") || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "MetrolistGroup/Metrolist") {
						t.Fatal("repository discovery satisfied or lost the pending commit requirement")
					}
					return garminHistoryTestCall("get_github_commits", `{"repository":"MetrolistGroup/Metrolist"}`), nil
				}
				if len(request.Tools) != 0 || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Public commit evidence") {
					t.Fatal("commit discovery lost its sourced final synthesis")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Public commit evidence."}}, nil
			})
			const prompt = "find the latest commits on GitHub for Metrolist"
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "repeated draft" {
				if calls != 2 || requests != 0 || result.ToolCalls != 0 || !strings.Contains(result.Answer, "couldn't retrieve") {
					t.Fatalf("unsourced commits were accepted: result=%+v, calls=%d, requests=%d", result, calls, requests)
				}
			} else if calls != 4 || requests == 0 || result.ToolCalls != 2 || result.Answer != "Public commit evidence." {
				t.Fatalf("named commit discovery failed: result=%+v, calls=%d, requests=%d", result, calls, requests)
			}
		})
	}
}

func TestGarminCalculatorCanPrecedeAnotherPermittedTool(t *testing.T) {
	b := garminDiscoveryTestBot(t)
	if err := b.Notes.AddNote("help", "Help note", "Local help instructions"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		switch calls {
		case 1:
			if !strings.Contains(request.Context, "calculator tool before answering") {
				t.Fatal("arithmetic lost its calculator requirement")
			}
			return garminHistoryTestCall("calculate_math", `{"expression":"3^3"}`), nil
		case 2:
			if !garminToolAvailable(request.Tools, "get_note") || !garminToolAvailable(request.Tools, "load_skill") || !strings.Contains(request.Messages[len(request.Messages)-1].Content, `"result":"27"`) {
				t.Fatal("calculator removed unrelated capabilities or lost the calculated result")
			}
			return garminHistoryTestCall("get_note", `{"name":"help"}`), nil
		default:
			if garminToolResultError(request.Messages[len(request.Messages)-1].Content) != "" || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Local help instructions") {
				t.Fatal("permitted tool was not actually executed after calculation")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "3 cubed is 27. Local help instructions."}}, nil
		}
	})
	message := garminHistoryTestMessage()
	message.Content = "garmin, calculate 3^3 then read the help note"
	result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "calculate 3^3 then read the help note"}})
	if err != nil || calls != 3 || result.ToolCalls != 2 || result.Answer != "3 cubed is 27. Local help instructions." {
		t.Fatalf("calculator continuation failed: result=%+v, calls=%d, error=%v", result, calls, err)
	}
}
