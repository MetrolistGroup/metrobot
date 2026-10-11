package auditsearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

type historyMock struct {
	messages []discord.Message
	cursors  []snowflake.ID
	err      error
}

func (h *historyMock) Channel(ctx context.Context, source string) (Channel, error) {
	return Channel{GuildID: 1, ID: 2}, ctx.Err()
}
func (h *historyMock) Messages(ctx context.Context, channel, before snowflake.ID, limit int) ([]discord.Message, error) {
	h.cursors = append(h.cursors, before)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if h.err != nil {
		return nil, h.err
	}
	items := []discord.Message{}
	for _, m := range h.messages {
		if m.ID < before {
			items = append(items, m)
			if len(items) == limit {
				break
			}
		}
	}
	return items, nil
}

func dayQuery() Query {
	from := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	return Query{Source: "message", From: from, Until: from.AddDate(0, 0, 1)}
}

func logMessage(at time.Time, content string) discord.Message {
	return discord.Message{ID: snowflake.New(at), CreatedAt: at, Content: content, ChannelID: 2}
}

func TestUTCExactDayJumpsToNextMidnight(t *testing.T) {
	q := dayQuery()
	h := &historyMock{messages: []discord.Message{
		logMessage(q.Until, "next day"), logMessage(q.Until.Add(-time.Millisecond), "last instant"),
		logMessage(q.From, "first instant"), logMessage(q.From.Add(-time.Millisecond), "previous day"),
	}}
	result, err := New(h).Search(context.Background(), q)
	if err != nil || len(result.Items) != 2 || !result.Complete || result.NextCursor != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(h.cursors) != 1 || h.cursors[0] != snowflake.New(q.Until) {
		t.Fatalf("wrong date jump: %v", h.cursors)
	}
	if result.Items[1].Timestamp != "2026-03-29T00:00:00Z" || result.Items[0].URL != fmt.Sprintf("https://discord.com/channels/1/2/%s", result.Items[0].ID) {
		t.Fatalf("bad item: %+v", result.Items)
	}
}

func TestNoMatchContinuationAndResultLimitNeverSkipFetchedMessages(t *testing.T) {
	for _, text := range []string{"", "absent"} {
		t.Run(text, func(t *testing.T) {
			q := dayQuery()
			q.Text = text
			h := &historyMock{}
			for i := 1; i <= 225; i++ {
				h.messages = append(h.messages, logMessage(q.Until.Add(-time.Duration(i)*time.Second), "entry"))
			}
			s := New(h)
			count, scanned, pages := 0, 0, 0
			seen := map[string]bool{}
			for {
				result, err := s.Search(context.Background(), q)
				if err != nil {
					t.Fatal(err)
				}
				pages++
				scanned += result.Scanned
				if result.Scanned > ScanLimit || len(result.Items) > PageSize {
					t.Fatalf("unbounded result: %+v", result)
				}
				for _, item := range result.Items {
					if seen[item.ID] {
						t.Fatal("duplicate item")
					}
					seen[item.ID] = true
					count++
				}
				if result.NextCursor == "" {
					if !result.Complete {
						t.Fatalf("incomplete final page: %+v", result)
					}
					break
				}
				if result.Complete {
					t.Fatal("claimed complete with unscanned range")
				}
				q.Before = snowflake.MustParse(result.NextCursor)
			}
			if scanned != 225 {
				t.Fatalf("scanned=%d", scanned)
			}
			if text == "" && (count != 225 || pages != 5) {
				t.Fatalf("skipped items count=%d pages=%d", count, pages)
			}
			if text != "" && (count != 0 || pages != 2) {
				t.Fatalf("empty matches didn't paginate: count=%d pages=%d", count, pages)
			}
		})
	}
}

func TestBodyEmbedsAndNestedComponentsLiteralSearch(t *testing.T) {
	q := dayQuery()
	m := logMessage(q.From.Add(time.Hour), "Plain body")
	m.Embeds = []discord.Embed{{Title: "Case #42", Description: "Description", Author: &discord.EmbedAuthor{Name: "Moderator"}, Fields: []discord.EmbedField{{Name: "Reason", Value: "100%_literal"}}, Footer: &discord.EmbedFooter{Text: "Footer text"}}}
	m.Components = []discord.LayoutComponent{discord.ContainerComponent{Components: []discord.ContainerSubComponent{discord.TextDisplayComponent{Content: "Nested component needle"}}}}
	for _, text := range []string{"PLAIN BODY", "case #42", "Description", "moderator", "Reason: 100%_literal", "footer TEXT", "nested component"} {
		q.Text = text
		result, err := New(&historyMock{messages: []discord.Message{m}}).Search(context.Background(), q)
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("%q: %+v %v", text, result, err)
		}
	}
	q.Text = ".*"
	result, err := New(&historyMock{messages: []discord.Message{m}}).Search(context.Background(), q)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("treated literal query as regex: %+v %v", result, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAttachmentContentSearchAndPartialWarnings(t *testing.T) {
	for _, scenario := range []string{"hit", "expired", "oversized-metadata", "oversized-response", "budget", "unsafe", "redirect", "binary", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			q := dayQuery()
			q.Text = "ATTACHMENT ONLY HIT"
			m := logMessage(q.From.Add(time.Hour), "no body match")
			m.Attachments = []discord.Attachment{{Filename: "audit.txt", URL: "https://cdn.discordapp.com/attachments/123/456/audit.txt?ex=signed&hm=signature"}}
			if scenario == "unsafe" {
				m.Attachments[0].URL = "https://127.0.0.1/attachments/123/456/audit.txt"
			}
			if scenario == "oversized-metadata" {
				m.Attachments[0].Size = maxAttachmentBytes + 1
			}
			if scenario == "budget" {
				for range maxAttachmentDownloads {
					m.Attachments = append(m.Attachments, m.Attachments[0])
				}
			}
			s := New(&historyMock{messages: []discord.Message{m}})
			calls := 0
			s.attachments.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "" || r.Method != http.MethodGet {
					t.Fatal("unsafe attachment request")
				}
				if scenario == "timeout" {
					return nil, context.DeadlineExceeded
				}
				status, body := http.StatusOK, "attachment only hit"
				switch scenario {
				case "expired":
					status = http.StatusForbidden
				case "redirect":
					status = http.StatusFound
				case "binary":
					body = "\x00\xff"
				case "oversized-response":
					body += strings.Repeat("x", maxAttachmentBytes)
				}
				header := http.Header{"Location": {"http://127.0.0.1/private"}}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			result, err := s.Search(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "hit" {
				if len(result.Items) != 1 || !strings.Contains(result.Items[0].Content, "attachment only hit") || !result.Complete || len(result.Warnings) != 1 {
					t.Fatalf("missed content: %+v", result)
				}
			} else if result.Complete || len(result.Warnings) < 2 || result.NextCursor != "" {
				t.Fatalf("failed to flag partial attachment search: %+v", result)
			}
			if (scenario == "unsafe" || scenario == "oversized-metadata") && calls != 0 {
				t.Fatal("downloaded unsafe/oversized attachment")
			}
			if scenario == "redirect" && calls != 1 {
				t.Fatal("followed redirect")
			}
			if calls > maxAttachmentDownloads {
				t.Fatal("exceeded download budget")
			}
		})
	}
}

func TestSlowAttachmentsPreservePagination(t *testing.T) {
	for _, test := range []struct {
		name        string
		messages    int
		attachments int
		text        string
		timeout     time.Duration
	}{
		{"one-post-no-match", 1, 8, "later needle", SearchTimeout},
		{"one-post-body-hit", 1, 8, "body", SearchTimeout},
		{"multiple-posts-no-match", 8, 1, "later needle", SearchTimeout},
		{"shorter-request-deadline", 1, 8, "later needle", 4 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := dayQuery()
				q.Text = test.text
				h := &historyMock{}
				for i := 1; i <= test.messages; i++ {
					message := logMessage(q.Until.Add(-time.Duration(i)*time.Second), "body")
					for range test.attachments {
						message.Attachments = append(message.Attachments, discord.Attachment{Filename: "audit.txt", URL: "https://cdn.discordapp.com/attachments/1/2/audit.txt"})
					}
					h.messages = append(h.messages, message)
				}
				later := logMessage(q.Until.Add(-time.Duration(test.messages+1)*time.Second), "body later needle")
				h.messages = append(h.messages, later)
				s := New(h)
				calls := 0
				s.attachments.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					<-r.Context().Done()
					return nil, r.Context().Err()
				})
				ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
				defer cancel()
				started := time.Now()
				result, err := s.Search(ctx, q)
				if err != nil || ctx.Err() != nil || time.Since(started) >= test.timeout {
					t.Fatalf("attachment stalls consumed the page deadline: result=%+v err=%v elapsed=%v", result, err, time.Since(started))
				}
				if result.Complete || result.Scanned == 0 || result.NextCursor != h.messages[result.Scanned-1].ID.String() || result.Scanned >= len(h.messages) {
					t.Fatalf("lost continuation: %+v", result)
				}
				if !strings.Contains(strings.Join(result.Warnings, "\n"), "attachment time budget") {
					t.Fatalf("missing time-budget warning: %+v", result)
				}
				if test.text == "body" {
					if len(result.Items) != 1 || len(result.Items[0].Attachments) != test.attachments {
						t.Fatalf("lost current body match or attachment metadata: %+v", result)
					}
				} else if len(result.Items) != 0 {
					t.Fatalf("unexpected body match: %+v", result)
				}
				if calls > 6 || calls == 0 {
					t.Fatalf("did not stop serial three-second timeouts before the search deadline: calls=%d", calls)
				}
				cancel()
				q.Before = snowflake.MustParse(result.NextCursor)
				result, err = s.Search(context.Background(), q)
				if err != nil || result.NextCursor != "" || len(result.Items) != 1 || result.Items[0].ID != later.ID.String() {
					t.Fatalf("later log was unreachable: result=%+v err=%v", result, err)
				}
			})
		})
	}
}

func TestCancellationDuringAttachmentStillAbortsSearch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := dayQuery()
		message := logMessage(q.From, "body")
		message.Attachments = []discord.Attachment{{Filename: "audit.txt", URL: "https://cdn.discordapp.com/attachments/1/2/audit.txt"}}
		s := New(&historyMock{messages: []discord.Message{message}})
		s.attachments.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { time.Sleep(time.Second); cancel() }()
		if _, err := s.Search(ctx, q); !errors.Is(err, context.Canceled) {
			t.Fatalf("ignored caller cancellation: %v", err)
		}
	})
}

func TestSafeAttachmentURLsAndPublicIPs(t *testing.T) {
	for _, raw := range []string{
		"http://cdn.discordapp.com/attachments/1/2/a.txt", "https://cdn.discordapp.com.evil/attachments/1/2/a.txt",
		"https://user@cdn.discordapp.com/attachments/1/2/a.txt", "https://cdn.discordapp.com:443/attachments/1/2/a.txt",
		"https://cdn.discordapp.com/icons/1/2/a.txt", "https://cdn.discordapp.com/attachments/1/2/../a.txt",
		"https://cdn.discordapp.com/attachments/1/2/%2fprivate", "https://cdn.discordapp.com/attachments/1/2/%2e%2e",
		"https://cdn.discordapp.com/attachments/0/2/a.txt", "https://cdn.discordapp.com/attachments/1/2/a.txt#fragment",
	} {
		if safeAttachmentURL(raw) {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, host := range []string{"cdn.discordapp.com", "media.discordapp.net"} {
		if !safeAttachmentURL("https://" + host + "/attachments/1/2/audit%20log.txt?hm=signature") {
			t.Errorf("rejected %s", host)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "::ffff:127.0.0.1", "0.0.0.0"} {
		if publicIP(netip.MustParseAddr(ip).Unmap()) {
			t.Errorf("accepted private IP %s", ip)
		}
	}
}

func TestRealHTTPRedirectIsNotFollowed(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "http://127.0.0.1/private")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	s := New(nil)
	s.attachments.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u := *r.URL
		original := r.URL
		u.Host = strings.TrimPrefix(server.URL, "https://")
		r.URL = &u
		response, err := server.Client().Transport.RoundTrip(r)
		r.URL = original
		return response, err
	})
	_, warning := s.readAttachment(context.Background(), discord.Attachment{Filename: "audit.txt", URL: "https://cdn.discordapp.com/attachments/1/2/audit.txt"}, &attachmentBudget{bytes: maxAttachmentBudget})
	if warning == "" || calls != 1 {
		t.Fatalf("followed redirect calls=%d warning=%q", calls, warning)
	}
}

func TestAttachmentAggregateBudgetAndNontext(t *testing.T) {
	s := New(nil)
	calls := 0
	s.attachments.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxAttachmentBytes)))}, nil
	})
	budget := attachmentBudget{bytes: maxAttachmentBudget}
	a := discord.Attachment{Filename: "audit.txt", URL: "https://cdn.discordapp.com/attachments/1/2/audit.txt"}
	for range maxAttachmentBudget / maxAttachmentBytes {
		text, warning := s.readAttachment(context.Background(), a, &budget)
		if len(text) != maxAttachmentBytes || warning != "" {
			t.Fatalf("text=%d warning=%q", len(text), warning)
		}
	}
	if _, warning := s.readAttachment(context.Background(), a, &budget); warning == "" || budget.bytes != 0 || calls != 8 {
		t.Fatalf("budget=%+v calls=%d warning=%q", budget, calls, warning)
	}
	budget = attachmentBudget{bytes: 4}
	text, warning := s.readAttachment(context.Background(), a, &budget)
	if text != "xxxx" || warning == "" {
		t.Fatalf("did not bound aggregate remainder: %q %q", text, warning)
	}
	q := dayQuery()
	m := logMessage(q.From, "body")
	m.Attachments = []discord.Attachment{{Filename: "image.png", URL: a.URL}, {Filename: "archive.zip", URL: a.URL}}
	s.history = &historyMock{messages: []discord.Message{m}}
	previous := calls
	result, err := s.Search(context.Background(), q)
	if err != nil || calls != previous || !result.Complete || len(result.Items[0].Attachments) != 2 {
		t.Fatalf("downloaded nontext files: result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestConcurrentSearchLimit(t *testing.T) {
	s := New(&historyMock{})
	for range cap(s.slots) {
		s.slots <- struct{}{}
	}
	if _, err := s.Search(context.Background(), dayQuery()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v", err)
	}
	<-s.slots
	if _, err := s.Search(context.Background(), dayQuery()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryErrorUnavailableInvalidAndCanceled(t *testing.T) {
	q := dayQuery()
	if _, err := New(nil).Search(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := New(&historyMock{err: errors.New("private-bot-token")}).Search(context.Background(), q); !errors.Is(err, ErrFetch) || strings.Contains(err.Error(), "private-bot-token") {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(&historyMock{}).Search(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	for _, invalid := range []Query{{}, {Source: "other", From: q.From, Until: q.Until}, {Source: "server", From: q.Until, Until: q.From}, {Source: "server", From: time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), Until: q.Until}} {
		if _, err := New(&historyMock{}).Search(context.Background(), invalid); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
}
