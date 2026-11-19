package discord

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

// shortcut: answers like "time in tokyo" can go stale within the TTL; shorten it if that bites.
const wolframCacheTTL = time.Hour

var wolframHTTP = &http.Client{Timeout: 10 * time.Second}

// wolframLocal answers plain arithmetic without touching the API.
func wolframLocal(query string) (string, bool) {
	answer, err := cmd.Calculate(query)
	if err != nil {
		return "", false
	}
	value, ok := new(big.Rat).SetString(answer)
	if !ok || value.IsInt() {
		return answer, true
	}
	decimal := strings.TrimRight(strings.TrimRight(value.FloatString(10), "0"), ".")
	return answer + " ≈ " + decimal, true
}

func (b *Bot) handleWolfram(s *bot.Client, m *discord.Message, query string) {
	if query == "" {
		b.kudosText(s, m, "Usage: `>>wa <question>`")
		return
	}
	if answer, ok := wolframLocal(query); ok {
		b.kudosText(s, m, answer)
		return
	}
	answer, err := b.wolframAnswer(query)
	if err != nil {
		b.Logger.Warn("wolfram query failed", zap.String("query", query), zap.Error(err))
		answer = "Wolfram|Alpha didn't answer, try again later."
	}
	b.kudosReply(s, m, discord.MessageCreate{Content: answer, Flags: discord.MessageFlagSuppressEmbeds})
}

// wolframAnswer uses the Short Answers API, caching results (including "no answer") in the shared page cache table.
func (b *Bot) wolframAnswer(query string) (string, error) {
	if b.Config.WolframAppID == "" {
		return "Only plain arithmetic works here until a Wolfram|Alpha app ID is configured.", nil
	}
	key := "wolfram:" + strings.ToLower(strings.Join(strings.Fields(query), " "))
	if body, ok, err := b.DB.GetGSMArenaCache(key, wolframCacheTTL); err == nil && ok {
		return string(body), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "https://api.wolframalpha.com/v1/result?" + url.Values{"appid": {b.Config.WolframAppID}, "i": {query}, "units": {"metric"}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := wolframHTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4000))
	if err != nil {
		return "", err
	}
	var answer string
	switch response.StatusCode {
	case http.StatusOK:
		answer = strings.TrimSpace(string(body))
	case http.StatusNotImplemented, http.StatusBadRequest:
		answer = "Wolfram|Alpha has no short answer for that. Try rephrasing."
	default:
		return "", fmt.Errorf("wolfram status %d", response.StatusCode)
	}
	if err := b.DB.SetGSMArenaCache(key, []byte(answer)); err != nil {
		b.Logger.Warn("failed to cache wolfram answer", zap.Error(err))
	}
	return answer, nil
}
