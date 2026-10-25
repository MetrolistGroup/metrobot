package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
)

// TestSupportDecisionsLive is opt-in, incurs API charges, and never connects to Discord or opens the production DB.
func TestSupportDecisionsLive(t *testing.T) {
	configPath := os.Getenv("METROBOT_DECISIONS_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("set METROBOT_DECISIONS_LIVE_CONFIG, _NOTES, _CASES and _REPORT to run a paid API replay")
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
		Keys  []string `json:"openrouter_api_keys"`
		Model string   `json:"openrouter_decision_model"`
	}
	read(configPath, &config)
	if override := os.Getenv("METROBOT_DECISIONS_LIVE_MODEL"); override != "" {
		config.Model = override
	}
	if len(config.Keys) == 0 || config.Model == "" {
		t.Fatal("replay needs configured keys and a decision model")
	}
	var notes []struct {
		Name        string `json:"name"`
		Description string `json:"short_desc"`
		Content     string `json:"content"`
	}
	read(os.Getenv("METROBOT_DECISIONS_LIVE_NOTES"), &notes)
	database, err := db.Open(filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	b := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}}
	for _, note := range notes {
		description := note.Description
		if strings.TrimSpace(description) == "" {
			description = "Manual note, excluded from automatic routing."
		}
		if _, _, _, err := cmd.ValidateNote(note.Name, description, note.Content); err != nil {
			t.Fatalf("invalid replay note %s: %v", note.Name, err)
		}
		if err := database.AddNote(note.Name, note.Description, note.Content); err != nil {
			t.Fatalf("note %s: %v", note.Name, err)
		}
	}
	var cases []struct {
		ID           string                `json:"id"`
		Message      string                `json:"message"`
		Parent       string                `json:"parent,omitempty"`
		Expected     []string              `json:"expected"`
		Conversation []supportDecisionTurn `json:"conversation,omitempty"`
	}
	read(os.Getenv("METROBOT_DECISIONS_LIVE_CASES"), &cases)
	if len(cases) == 0 || len(cases) > 500 {
		t.Fatal("replay requires 1 to 500 labeled cases")
	}
	type result struct {
		ID           string   `json:"id"`
		Action       string   `json:"action"`
		Expected     []string `json:"expected"`
		Pass         bool     `json:"pass"`
		Error        bool     `json:"error,omitempty"`
		ShouldAnswer float64  `json:"should_answer"`
		Top          float64  `json:"top"`
		Margin       float64  `json:"margin"`
		Route        string   `json:"route"`
		MS           int64    `json:"ms"`
		Cost         float64  `json:"cost"`
	}
	var results []result
	client := cmd.NewDecisionsClient(config.Keys, config.Model)
	for i, c := range cases {
		m := supportTestMessage(100 + i)
		m.Content = c.Message
		if c.Parent != "" {
			m.ReferencedMessage = supportTestMessage(99)
			m.ReferencedMessage.Content = c.Parent
		}
		var messages []cmd.GarminAIMessage
		for _, turn := range c.Conversation {
			messages = append(messages, cmd.GarminAIMessage{Role: turn.Role, Content: turn.Content})
		}
		messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: c.Message})
		request, catalog, err := b.supportDecisionRequest(m, messages)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		response, err := client.Decide(context.Background(), request)
		r := result{ID: c.ID, Expected: c.Expected, Action: "none", MS: time.Since(start).Milliseconds()}
		if err != nil {
			r.Error = true
		} else {
			r.ShouldAnswer = *response.Answers["should_answer"].Noul
			route := response.Answers["route"]
			r.Route = route.Choice
			r.Top = *route.Probabilities[route.Choice]
			second := 0.0
			for option, p := range route.Probabilities {
				if option != route.Choice && *p > second {
					second = *p
				}
			}
			r.Margin = r.Top - second
			note := catalog[route.Choice].Name
			if note != "" {
				r.Route = note
			}
			r.Action = supportDecisionAction(r.ShouldAnswer, r.Top, r.Margin, route.Choice, note, true)
			if r.Action == "note" {
				r.Action = note
			}
			if response.Usage.Cost != nil {
				r.Cost = *response.Usage.Cost
			}
		}
		for _, expected := range c.Expected {
			if r.Action == expected && !r.Error {
				r.Pass = true
			}
		}
		results = append(results, r)
	}
	report, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("METROBOT_DECISIONS_LIVE_REPORT"), report, 0600); err != nil {
		t.Fatal(err)
	}
	var times []int64
	passed, errors := 0, 0
	cost := 0.0
	for _, r := range results {
		times = append(times, r.MS)
		cost += r.Cost
		if r.Pass {
			passed++
		}
		if r.Error {
			errors++
		}
		if !r.Pass {
			t.Logf("case=%s action=%s expected=%v should=%.3f top=%.3f margin=%.3f error=%v", r.ID, r.Action, r.Expected, r.ShouldAnswer, r.Top, r.Margin, r.Error)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Log(fmt.Sprintf("model=%s cases=%d matched=%d api_errors=%d p50_ms=%d p95_ms=%d cost_usd=%.8f", config.Model, len(results), passed, errors, times[len(times)/2], times[(len(times)-1)*95/100], cost))
	// Report misses without retrying them or changing labels to make a benchmark pass.
}
