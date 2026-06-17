package cmd

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

const releaseCooldown = 10 * time.Minute

var releaseDatePattern = regexp.MustCompile(`(?i)` +
	`(when\b[^.]{0,80}\b(?:update|release|version|app)\b)` +
	`|(\beta\b[^.]{0,80}\b(?:update|release|version)\b)` +
	`|(\bstill\s+(?:no|waiting)\b[^.]{0,80}\b(?:update|release|version|news)\b)` +
	`|(\b(?:waiting|longing|hoping)\s+for\b[^.]{0,80}\b(?:update|release|version)\b)` +
	`|(\bany\s+(?:update|release|version|news|eta)\b)` +
	`|(\bwhere\s+(?:is|are)\b[^.]{0,80}\b(?:update|release|version|app)\b)` +
	`|(\brelease\s+date\b)` +
	`|(\bis\s+there\s+a\s+(?:new\s+)?(?:update|release|version)\b)` +
	`|(\bwhat'?s\s+the\s+(?:eta|status|release\s+date)\b)` +
	`|(\b(?:update|release|version)\s+when\b)` +
	`|(\bwhen\s+will\s+you\b)` +
	`|(\bhow\s+(?:long|much)\s+(?:until|before|till)\b[^.]{0,80}\b(?:update|release|version)\b)` +
	`|(\bis\s+it\s+(?:out|released|available)\b[^.]{0,20}\b(?:yet|already)\b)` +
	`|(\bdid\s+it\s+(?:release|come\s+out)\b)`)

type ReleaseCounterHandler struct {
	DB              *db.DB
	mu              sync.Mutex
	lastTriggeredAt time.Time
}

func (h *ReleaseCounterHandler) Increment(isTelegram bool) (string, error) {
	h.mu.Lock()
	if time.Since(h.lastTriggeredAt) < releaseCooldown {
		h.mu.Unlock()
		return "", nil
	}
	h.mu.Unlock()

	count, err := h.DB.IncrementReleaseCounter()
	if err != nil {
		return "", fmt.Errorf("incrementing release counter: %w", err)
	}

	h.mu.Lock()
	h.lastTriggeredAt = time.Now()
	h.mu.Unlock()

	return formatCounter(count, isTelegram), nil
}

func (h *ReleaseCounterHandler) Get(isTelegram bool) (string, error) {
	count, err := h.DB.GetReleaseCounter()
	if err != nil {
		return "", fmt.Errorf("getting release counter: %w", err)
	}
	return formatCounter(count, isTelegram), nil
}

func formatCounter(count int, isTelegram bool) string {
	msg := fmt.Sprintf("Release date question counter: %d", count)
	if isTelegram {
		return msg
	}
	return msg
}

func MatchReleaseQuestion(content string) bool {
	return releaseDatePattern.MatchString(content)
}
