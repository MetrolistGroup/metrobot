// Package auditsearch reads bounded pages of existing Discord log history. It stores nothing.
package auditsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

var (
	ErrUnavailable = errors.New("Discord log search is unavailable; check bot access and configured log channels (moderation requires discord_log_channel)")
	ErrFetch       = errors.New("Unable to read Discord log history; check bot permissions or try again later")
	ErrBusy        = errors.New("Discord log search is busy; try again later")
)

const (
	PageSize      = 50
	ScanLimit     = 200
	SearchTimeout = 20 * time.Second
)

type Channel struct{ GuildID, ID snowflake.ID }

type History interface {
	Channel(context.Context, string) (Channel, error)
	Messages(context.Context, snowflake.ID, snowflake.ID, int) ([]discord.Message, error)
}

type Query struct {
	Source      string
	From, Until time.Time // UTC lower inclusive and upper exclusive bounds.
	Text        string
	Before      snowflake.ID
}

type Attachment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Item struct {
	ID          string       `json:"id"`
	Timestamp   string       `json:"timestamp"`
	Source      string       `json:"source"`
	Content     string       `json:"content"`
	URL         string       `json:"url"`
	Attachments []Attachment `json:"attachments"`
}

type Result struct {
	Items      []Item   `json:"items"`
	NextCursor string   `json:"next_cursor"`
	Scanned    int      `json:"scanned"`
	Complete   bool     `json:"complete"`
	Warnings   []string `json:"warnings"`
}

type Searcher interface {
	Search(context.Context, Query) (Result, error)
}

type Service struct {
	history     History
	attachments *http.Client
	slots       chan struct{}
}

func New(history History) *Service {
	return &Service{history: history, attachments: attachmentClient(), slots: make(chan struct{}, 4)}
}

func ValidSource(source string) bool {
	return source == "message" || source == "reaction" || source == "server" || source == "moderation"
}

// ValidRange prevents overflow in Discord's 42-bit millisecond timestamp.
func ValidRange(from, until time.Time) bool {
	return !from.IsZero() && from.Before(until) && from.UnixMilli() >= snowflake.Epoch && until.UnixMilli()-snowflake.Epoch <= (1<<42)-1
}

func (s *Service) Search(ctx context.Context, q Query) (Result, error) {
	result := Result{Items: []Item{}, Warnings: []string{"Dates match the posted log message in UTC, not necessarily the original event or message date."}}
	if !ValidSource(q.Source) || !ValidRange(q.From, q.Until) {
		return result, errors.New("Invalid log source or date range")
	}
	if s.history == nil {
		return result, ErrUnavailable
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return result, ctx.Err()
	default:
		return result, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()
	channel, err := s.history.Channel(ctx, q.Source)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, err
	}
	if channel.ID == 0 || channel.GuildID == 0 {
		return result, ErrUnavailable
	}
	before := snowflake.New(q.Until)
	if q.Before != 0 && q.Before < before {
		before = q.Before
	}
	lower := snowflake.New(q.From)
	budget := attachmentBudget{bytes: maxAttachmentBudget}
	needle := strings.ToLower(q.Text)
	partial := false
	warn := func(message string) {
		partial = true
		for _, existing := range result.Warnings {
			if existing == message {
				return
			}
		}
		result.Warnings = append(result.Warnings, message)
	}
	finish := func(exhausted bool) (Result, error) {
		result.Complete = exhausted && !partial
		if !exhausted {
			result.NextCursor = before.String()
		}
		return result, nil
	}
	for before > lower && result.Scanned < ScanLimit {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		limit := min(100, ScanLimit-result.Scanned)
		messages, err := s.history.Messages(ctx, channel.ID, before, limit)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, ErrFetch
		}
		if len(messages) > limit {
			return result, ErrFetch
		}
		sort.Slice(messages, func(i, j int) bool { return messages[i].ID > messages[j].ID })
		if len(messages) == 0 {
			return finish(true)
		}
		for i, message := range messages {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if message.ID == 0 || message.ID >= before || message.ChannelID != 0 && message.ChannelID != channel.ID {
				return result, ErrFetch
			}
			before = message.ID
			result.Scanned++
			if message.ID < lower || message.CreatedAt.Before(q.From) {
				return finish(true)
			}
			if !message.CreatedAt.Before(q.Until) {
				continue
			}
			content := flatten(message)
			attachments := []Attachment{}
			for _, a := range message.Attachments {
				if err := ctx.Err(); err != nil {
					return result, err
				}
				if safeAttachmentURL(a.URL) {
					attachments = append(attachments, Attachment{Name: a.Filename, URL: a.URL})
				}
				if !textAttachment(a) {
					continue
				}
				text, warning := s.readAttachment(ctx, a, &budget)
				if warning != "" {
					warn(warning)
				}
				if text != "" {
					content += "\n\nAttachment: " + a.Filename + "\n" + text
				}
			}
			if strings.Contains(strings.ToLower(content), needle) {
				result.Items = append(result.Items, Item{ID: message.ID.String(), Timestamp: message.CreatedAt.UTC().Format(time.RFC3339Nano), Source: q.Source, Content: content, URL: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", channel.GuildID, channel.ID, message.ID), Attachments: attachments})
			}
			if len(result.Items) == PageSize {
				// The cursor follows the last processed message, never the end of a fetched batch.
				return finish(before <= lower || i == len(messages)-1 && len(messages) < limit)
			}
		}
		if len(messages) < limit {
			return finish(true)
		}
	}
	return finish(before <= lower)
}

func flatten(message discord.Message) string {
	parts := []string{message.Content}
	for _, e := range message.Embeds {
		parts = append(parts, e.Title, e.Description, e.URL)
		if e.Author != nil {
			parts = append(parts, e.Author.Name)
		}
		for _, field := range e.Fields {
			parts = append(parts, field.Name+": "+field.Value)
		}
		if e.Footer != nil {
			parts = append(parts, e.Footer.Text)
		}
	}
	// Extract human-readable component text without exposing JSON syntax or custom IDs as matches.
	data, _ := json.Marshal(message.Components)
	var components any
	if json.Unmarshal(data, &components) == nil {
		componentText(components, &parts)
	}
	for _, a := range message.Attachments {
		parts = append(parts, "Attachment: "+a.Filename)
		if a.Description != nil {
			parts = append(parts, *a.Description)
		}
	}
	nonempty := parts[:0]
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, "\n")
}

func componentText(value any, parts *[]string) {
	switch v := value.(type) {
	case []any:
		for _, child := range v {
			componentText(child, parts)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if text, ok := v[key].(string); ok {
				switch key {
				case "content", "label", "title", "description", "placeholder", "name", "value":
					*parts = append(*parts, text)
				}
			} else {
				componentText(v[key], parts)
			}
		}
	}
}
