package discord

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func TestGarminRequiresEveryNamedDeviceSource(t *testing.T) {
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "show GSMArena, NanoReview and Technical City pages for these devices"}}
	want := []string{"get_gsmarena_phone", "get_nanoreview_device", "get_technicalcity_device"}
	if got := garminRequiredDeviceTools(messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("required device sources = %v, want %v", got, want)
	}
}

func TestGarminDeviceSourceExclusions(t *testing.T) {
	for _, test := range []struct{ prompt, tool, query, evidence string }{
		{"show the NanoReview page for OnePlus 15, not Technical City", "get_nanoreview_device", "OnePlus 15", "7300 mAh"},
		{"not NanoReview; show the Technical City page for RTX 4090", "get_technicalcity_device", "RTX 4090", "24 GB"},
		{"show Redmi 6A specs without NanoReview", "get_gsmarena_phone", "Redmi 6A", "3000 mAh"},
		{"show the NanoReview page for OnePlus 15 because GSMArena is outdated", "get_nanoreview_device", "OnePlus 15", "7300 mAh"},
		{"show the GSMArena page for Redmi 6A because NanoReview is outdated", "get_gsmarena_phone", "Redmi 6A", "3000 mAh"},
		{"show NanoReview page for OnePlus 15, do not use GSMArena or Technical City", "get_nanoreview_device", "OnePlus 15", "7300 mAh"},
	} {
		t.Run(test.prompt, func(t *testing.T) {
			messages := []cmd.GarminAIMessage{{Role: "user", Content: test.prompt}}
			if got := garminRequiredDeviceTools(messages); !reflect.DeepEqual(got, []string{test.tool}) {
				t.Fatalf("required sources = %v, want only %s", got, test.tool)
			}
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				t.Fatalf("excluded source made an outbound lookup: %s", request.URL)
				return nil, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			cacheGarminNamedSourceFixtures(t, b)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				for _, tool := range []string{"get_nanoreview_device", "get_technicalcity_device", "get_gsmarena_phone"} {
					if !garminToolAvailable(request.Tools, tool) {
						t.Fatal("source requirements changed configured tool discovery")
					}
				}
				if calls == 1 {
					return garminHistoryTestCall(test.tool, mustJSON(map[string]string{"query": test.query})), nil
				}
				if !strings.Contains(request.Messages[len(request.Messages)-1].Content, test.evidence) {
					t.Fatal("requested source evidence was lost")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Found the requested source."}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + test.prompt
			result, err := b.runGarminAI(context.Background(), nil, message, messages)
			if err != nil || calls != 2 || result.ToolCalls != 1 || result.Answer != "Found the requested source." {
				t.Fatalf("excluded source was still required: result=%+v, calls=%d, error=%v", result, calls, err)
			}
		})
	}
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "show not only NanoReview but also Technical City pages"}}
	if got := garminRequiredDeviceTools(messages); !reflect.DeepEqual(got, []string{"get_nanoreview_device", "get_technicalcity_device"}) {
		t.Fatalf("not only wrongly excluded a source: %v", got)
	}
}

func TestGarminDeviceLinksAlwaysRequireEvidence(t *testing.T) {
	for _, answer := range []string{"https://www.gsmarena.com/INVENTED_PAGE.php", "https://nanoreview.net/en/phone/INVENTED_PAGE", "https://nanoreview.net./en/phone/INVENTED_PAGE", "https://nanoreview。net/en/phone/INVENTED_PAGE"} {
		if garminDevicePageURLsGrounded(answer, nil, []string{answer}, false) {
			t.Fatalf("optional source link escaped verification: %s", answer)
		}
	}
	if !garminDevicePageURLsGrounded("https://github.com/MetrolistGroup/Metrolist", nil, nil, false) {
		t.Fatal("unrelated non-device answers acquired a device citation requirement")
	}
	b := garminDiscoveryTestBot(t)
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "https://nanoreview.net./en/phone/INVENTED_PAGE"}}, nil
	})
	message := garminHistoryTestMessage()
	message.Content = "garmin, can you link the OnePlus 15?"
	result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "can you link the OnePlus 15?"}})
	if err != nil || result == nil || calls != 2 || strings.Contains(result.Answer, "INVENTED_PAGE") {
		t.Fatalf("unverified optional link was delivered: calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestGarminDevicePageURLAllowlist(t *testing.T) {
	const deviceURL = "https://nanoreview.net/en/phone/oneplus-15"
	const githubURL = "https://github.com/owner/repo/commit/sourcecommit"
	verified := map[string]struct{}{deviceURL: {}}
	for _, test := range []struct {
		answer  string
		sources []string
		want    bool
	}{
		{"[OnePlus 15](" + deviceURL + ").", nil, true},
		{"https://www.gsmarena.com/INVENTED_PAGE.php", nil, false},
		{"https://technical.city/en/gpu/INVENTED_PAGE", nil, false},
		{"https://example.invalid/INVENTED_PAGE", nil, false},
		{deviceURL + "-invented", nil, false},
		{githubURL, []string{mustJSON(map[string]string{"html_url": githubURL})}, true},
		{githubURL, []string{mustJSON(map[string]string{"html_url": githubURL + "-other"})}, false},
		{githubURL, []string{"include this link: [commit](" + githubURL + ")."}, true},
		{githubURL, []string{"private history link: " + githubURL}, true},
		{githubURL, []string{"private history link: " + githubURL + "-other"}, false},
		{"https://technical.city/en/gpu/INVENTED_PAGE", []string{"user supplied https://technical.city/en/gpu/INVENTED_PAGE"}, false},
		{"https://technical.city/en/gpu/INVENTED_PAGE", []string{`{"url":"https://technical.city/en/gpu/INVENTED_PAGE"}`}, false},
	} {
		if got := garminDevicePageURLsGrounded(test.answer, verified, test.sources, true); got != test.want {
			t.Errorf("URL grounded(%q) = %v, want %v", test.answer, got, test.want)
		}
	}
}

func TestMixedDevicePagesNeedBothSourcesAndReturnedURLs(t *testing.T) {
	const nanoURL = "https://nanoreview.net/en/phone/oneplus-15"
	const cityURL = "https://technical.city/en/gpu/GeForce-RTX-4090"
	const inventedURL = "https://technical.city/en/gpu/INVENTED_PAGE"
	const prompt = "show NanoReview page for OnePlus 15 and Technical City page for RTX 4090"
	for _, mode := range []string{"missing source", "both sources", "invented URL", "repaired URL"} {
		t.Run(mode, func(t *testing.T) {
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				t.Fatalf("device request escaped local cache fixtures: %s", request.URL)
				return nil, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			for key, body := range map[string]string{
				"nano:search:oneplus 15":         `[{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"` + nanoURL + `"}]`,
				"nano:detail:phone/oneplus-15":   `{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"` + nanoURL + `","specs":[{"name":"Battery","items":[{"name":"Capacity","values":["7300 mAh"]}]}]}`,
				"tc:search:rtx 4090":             `[{"name":"GeForce RTX 4090","slug":"gpu/GeForce-RTX-4090","url":"` + cityURL + `"}]`,
				"tc:device:gpu/GeForce-RTX-4090": `{"name":"GeForce RTX 4090","slug":"gpu/GeForce-RTX-4090","url":"` + cityURL + `","specs":[{"name":"Memory","items":[{"name":"Capacity","values":["24 GB"]}]}]}`,
			} {
				if err := b.DB.SetGSMArenaCache(key, []byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls == 1 {
					for _, tool := range []string{"get_nanoreview_device", "get_technicalcity_device"} {
						if !garminToolAvailable(request.Tools, tool) || !strings.Contains(request.Context, tool) {
							t.Fatalf("missing required device source %s", tool)
						}
					}
					completion := garminHistoryTestCall("get_nanoreview_device", `{"query":"OnePlus 15"}`)
					if mode == "invented URL" || mode == "repaired URL" {
						completion.Message.ToolCalls = append(completion.Message.ToolCalls, garminHistoryTestCall("get_technicalcity_device", `{"query":"RTX 4090"}`).Message.ToolCalls...)
					}
					return completion, nil
				}
				if mode == "both sources" && calls == 3 {
					return garminHistoryTestCall("get_technicalcity_device", `{"query":"RTX 4090"}`), nil
				}
				nanoFound, cityFound := false, false
				for _, turn := range request.Messages {
					nanoFound = nanoFound || (turn.Role == "tool" && strings.Contains(turn.Content, "7300 mAh"))
					cityFound = cityFound || (turn.Role == "tool" && strings.Contains(turn.Content, "24 GB"))
					if strings.Contains(turn.Content, inventedURL) {
						t.Fatal("unsupported device-page draft survived a retry")
					}
				}
				if !nanoFound || ((mode == "invented URL" || mode == "repaired URL" || calls == 4) && !cityFound) {
					t.Fatal("device source result was lost before synthesis")
				}
				answer := nanoURL + "\n" + inventedURL
				if (mode == "both sources" && calls == 4) || (mode == "repaired URL" && calls == 3) {
					answer = "[OnePlus 15](" + nanoURL + ")\n[RTX 4090](" + cityURL + ")"
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
			})
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			wantCalls := 3
			if mode == "both sources" {
				wantCalls = 4
			}
			if err != nil || calls != wantCalls || strings.Contains(result.Answer, inventedURL) {
				t.Fatalf("device-page grounding failed: result=%+v, calls=%d, error=%v", result, calls, err)
			}
			if mode == "missing source" || mode == "invented URL" {
				if !result.Silent || result.Answer != "" {
					t.Fatalf("unsupported device page was accepted: %s", result.Answer)
				}
			} else if !strings.Contains(result.Answer, nanoURL) || !strings.Contains(result.Answer, cityURL) || result.ToolCalls != 2 {
				t.Fatalf("verified device pages were lost: %+v", result)
			}
		})
	}
}

func cacheGarminNamedSourceFixtures(t *testing.T, b *Bot) {
	t.Helper()
	for key, body := range map[string]string{
		"index":                          `{"brands":{"1":"Xiaomi"},"records":[{"brand_id":1,"id":3,"model":"Redmi 6A","display":"Redmi 6A"}]}`,
		"phone:3":                        `{"id":3,"name":"Xiaomi Redmi 6A","slug":"xiaomi_redmi_6a-9217","url":"https://www.gsmarena.com/xiaomi_redmi_6a-9217.php","specs":[{"name":"Battery","items":[{"name":"Capacity","values":["3000 mAh"]}]}]}`,
		"nano:search:oneplus 15":         `[{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15"}]`,
		"nano:detail:phone/oneplus-15":   `{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15","specs":[{"name":"Battery","items":[{"name":"Capacity","values":["7300 mAh"]}]}]}`,
		"tc:search:rtx 4090":             `[{"name":"GeForce RTX 4090","slug":"gpu/GeForce-RTX-4090","url":"https://technical.city/en/gpu/GeForce-RTX-4090"}]`,
		"tc:device:gpu/GeForce-RTX-4090": `{"name":"GeForce RTX 4090","slug":"gpu/GeForce-RTX-4090","url":"https://technical.city/en/gpu/GeForce-RTX-4090","specs":[{"name":"Memory","items":[{"name":"Capacity","values":["24 GB"]}]}]}`,
	} {
		if err := b.DB.SetGSMArenaCache(key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGarminDeviceAnswerAllowsSuppliedNonDeviceLinks(t *testing.T) {
	const deviceURL = "https://nanoreview.net/en/phone/oneplus-15"
	const photoURL = "https://cdn.discordapp.com/attachments/123/456/photo.jpg?ex=123&is=456"
	for _, source := range []string{"request", "attachment", "referenced attachment", "automatic community", "deferred community", "current history", "automatic permalink", "current permalink"} {
		t.Run(source, func(t *testing.T) {
			hasAttachment := strings.HasSuffix(source, "attachment")
			currentOnly := source == "request" || hasAttachment
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				t.Fatalf("mixed answer made an unexpected public request: %s", request.URL.Host)
				return nil, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			cacheGarminNamedSourceFixtures(t, b)
			historyRead := false
			historyBody := func(channelID string) string {
				historyRead = true
				messages := garminHistoryTestMessages(1, "Recent private photo")
				messages[0]["channel_id"] = channelID
				messages[0]["attachments"] = []map[string]any{{"filename": "minky.jpg", "content_type": "image/jpeg", "url": photoURL}}
				return mustJSON(messages)
			}
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) { return historyBody(garminHistoryTestChannel), nil })
			transport := session.Rest.HTTPClient().Transport
			session.Rest.HTTPClient().Transport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/channels/"+garminMinkyID+"/messages") {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(historyBody(garminMinkyID))), Request: request}, nil
				}
				return transport.RoundTrip(request)
			})
			answerURL := photoURL
			if strings.HasSuffix(source, "permalink") {
				channelID := garminMinkyID
				if source == "current permalink" {
					channelID = garminHistoryTestChannel
				}
				answerURL = discordMessageURL(b.Config.DiscordGuildID, channelID, "999")
			}
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls == 1 {
					if historyRead {
						t.Fatal("private history preceded the device source")
					}
					if hasAttachment {
						if !reflect.DeepEqual(request.Messages[0].Images, []string{photoURL}) || strings.Contains(request.Messages[0].Content, photoURL) {
							t.Fatal("fixture must supply the current photo through Images, not text")
						}
						return garminHistoryTestCall("get_nanoreview_device", mustJSON(map[string]string{"query": photoURL})), nil
					}
					return garminHistoryTestCall("get_nanoreview_device", `{"query":"OnePlus 15"}`), nil
				}
				if hasAttachment && calls == 2 {
					if !strings.Contains(garminToolResultError(request.Messages[len(request.Messages)-1].Content), "current user request") {
						t.Fatal("answer attachment provenance was reused for a public query")
					}
					return garminHistoryTestCall("get_nanoreview_device", `{"query":"OnePlus 15"}`), nil
				}
				if calls == 2 && !currentOnly {
					if historyRead || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "7300 mAh") {
						t.Fatal("device evidence was lost or private history read too early")
					}
					switch source {
					case "current history", "current permalink":
						return garminHistoryTestCall("read_channel_history", `{"limit":20}`), nil
					case "deferred community":
						return garminHistoryTestCall("read_community_channel", `{"channel":"minky"}`), nil
					default:
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
					}
				}
				if !currentOnly {
					if !historyRead && calls == 3 && !strings.HasPrefix(source, "automatic") {
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
					}
					if !historyRead {
						t.Fatal("private photo was not retrieved")
					}
					for _, tool := range request.Tools {
						if garminPublicQueryToolName(tool.Function.Name) {
							t.Fatal("private links reopened public lookups")
						}
					}
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: deviceURL + "\n" + answerURL}}, nil
			})
			prompt := "show NanoReview page for OnePlus 15 and a recent photo"
			switch source {
			case "request":
				prompt += " using this photo link " + photoURL
			case "automatic community", "automatic permalink":
				prompt += " from Minky"
			case "current history", "current permalink":
				prompt += " from the last 20 messages"
			}
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			if hasAttachment {
				attachments := []discord.Attachment{{Filename: "photo.jpg", ContentType: ptr("image/jpeg"), URL: photoURL}}
				if source == "referenced attachment" {
					message.ReferencedMessage = &discord.Message{ID: snowflakeID("999"), ChannelID: message.ChannelID, GuildID: message.GuildID, Attachments: attachments}
				} else {
					message.Attachments = attachments
				}
			}
			result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{b.garminAIUserMessage(message, prompt)})
			wantCalls, wantTools := 4, 2
			if currentOnly {
				wantCalls, wantTools = 2, 1
				if hasAttachment {
					wantCalls = 3
				}
			} else if strings.HasPrefix(source, "automatic") {
				wantCalls = 3
			}
			if err != nil || calls != wantCalls || result.ToolCalls != wantTools || result.Answer != deviceURL+"\n"+answerURL {
				t.Fatalf("supplied link was rejected: result=%+v, calls=%d, error=%v", result, calls, err)
			}
		})
	}
}

func TestGarminSequentialRequiredSourcesFitRoundBudget(t *testing.T) {
	const answer = "https://www.gsmarena.com/xiaomi_redmi_6a-9217.php\nhttps://nanoreview.net/en/phone/oneplus-15\nhttps://technical.city/en/gpu/GeForce-RTX-4090\nhttps://github.com/owner/repo/commit/sourcecommit"
	for _, mode := range []string{"sources only", "draft and community", "draft and current history", "budget exhausted"} {
		t.Run(mode, func(t *testing.T) {
			original := http.DefaultTransport
			http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body string
				switch request.URL.Path {
				case "/repos/owner/repo":
					body = `{"full_name":"owner/repo","private":false}`
				case "/repos/owner/repo/commits":
					body = `[{"sha":"sourcecommit","html_url":"https://github.com/owner/repo/commit/sourcecommit","commit":{"message":"Public commit evidence"}}]`
				default:
					t.Fatalf("unexpected public lookup: %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			b := garminDiscoveryTestBot(t)
			cacheGarminNamedSourceFixtures(t, b)
			lookups := []struct{ tool, args, evidence string }{
				{"get_gsmarena_phone", `{"query":"Redmi 6A"}`, "3000 mAh"},
				{"get_nanoreview_device", `{"query":"OnePlus 15"}`, "7300 mAh"},
				{"get_technicalcity_device", `{"query":"RTX 4090"}`, "24 GB"},
				{"get_github_commits", `{"repository":"owner/repo"}`, "Public commit evidence"},
			}
			calls := 0
			historyRead := false
			historyBody := func(channelID string) string {
				if calls <= len(lookups)+1 || mode == "budget exhausted" {
					t.Fatal("private history preceded all required public sources")
				}
				historyRead = true
				messages := garminHistoryTestMessages(1, "Private history evidence")
				messages[0]["channel_id"] = channelID
				return mustJSON(messages)
			}
			session := garminHistoryTestSession(t, func(*http.Request) (string, error) { return historyBody(garminHistoryTestChannel), nil })
			transport := session.Rest.HTTPClient().Transport
			session.Rest.HTTPClient().Transport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/channels/"+garminMinkyID+"/messages") {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(historyBody(garminMinkyID))), Request: request}, nil
				}
				return transport.RoundTrip(request)
			})
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if mode == "budget exhausted" {
					if calls > 6 {
						t.Fatal("required sources allowed unbounded tool rounds")
					}
					return garminHistoryTestCall("calculate_math", `{"expression":"3^3"}`), nil
				}
				step := calls
				if mode != "sources only" {
					if calls == 1 {
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
					}
					step--
				}
				if step <= len(lookups) {
					lookup := lookups[step-1]
					if historyRead || !garminToolAvailable(request.Tools, lookup.tool) {
						t.Fatalf("round %d lost required source %s", calls, lookup.tool)
					}
					return garminHistoryTestCall(lookup.tool, lookup.args), nil
				}
				for _, lookup := range lookups {
					found := false
					for _, turn := range request.Messages {
						found = found || (turn.Role == "tool" && strings.Contains(turn.Content, lookup.evidence))
						if turn.Content == "UNSUPPORTED_DRAFT" || turn.Content == "UNDISPATCHED_DRAFT" {
							t.Fatal("unsupported draft survived into synthesis")
						}
					}
					if !found {
						t.Fatalf("synthesis lost %s evidence", lookup.tool)
					}
				}
				if step == len(lookups)+1 && mode != "sources only" {
					if mode == "draft and current history" {
						return garminHistoryTestCall("read_channel_history", `{"limit":20}`), nil
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNDISPATCHED_DRAFT"}}, nil
				}
				if mode != "sources only" {
					privateFound := strings.Contains(request.Context, "Private history evidence")
					for _, turn := range request.Messages {
						privateFound = privateFound || strings.Contains(turn.Content, "Private history evidence")
					}
					if !historyRead || !privateFound || len(request.Tools) != 0 || !request.DisableReasoning {
						t.Fatal("source retrieval consumed private-history or final synthesis rounds")
					}
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
			})
			prompt := "show GSMArena page for Redmi 6A, NanoReview page for OnePlus 15, Technical City page for RTX 4090 and recent commits in owner/repo"
			if mode == "draft and current history" {
				prompt += " and summarize the last 20 messages"
			} else if mode != "sources only" {
				prompt += " and recent Minky updates"
			}
			message := garminHistoryTestMessage()
			message.Content = "garmin, " + prompt
			result, err := b.runGarminAI(context.Background(), session, message, []cmd.GarminAIMessage{{Role: "user", Content: prompt}})
			wantCalls, wantTools, wantAnswer := 7, 5, answer
			if mode == "sources only" {
				wantCalls, wantTools = 5, 4
			} else if mode == "budget exhausted" {
				wantCalls, wantTools, wantAnswer = 6, 6, ""
			}
			if err != nil || calls != wantCalls || result.ToolCalls != wantTools || result.Answer != wantAnswer || result.Silent != (mode == "budget exhausted") {
				t.Fatalf("required sources exceeded budget: result=%+v, calls=%d, error=%v", result, calls, err)
			}
		})
	}
}

func TestNamedDevicePageCannotBeInventedWithoutLookup(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected automatic lookup: %s", r.URL)
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, test := range []struct{ prompt, tool string }{
		{"show me the NanoReview page for OnePlus 15", "get_nanoreview_device"},
		{"show me the Technical City page for RTX 4090", "get_technicalcity_device"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			b := garminDiscoveryTestBot(t)
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, r cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if !garminToolAvailable(r.Tools, test.tool) || !strings.Contains(r.Context, test.tool) {
					t.Fatal("named source was not available and required")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "https://example.invalid/INVENTED_PAGE"}}, nil
			})
			m := garminHistoryTestMessage()
			m.Content = "garmin, " + test.prompt
			result, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: test.prompt}})
			if err != nil || result == nil || calls != 2 || result.Answer != "" || !result.Silent {
				t.Fatalf("unsourced page escaped: calls=%d result=%+v err=%v", calls, result, err)
			}
		})
	}
}
