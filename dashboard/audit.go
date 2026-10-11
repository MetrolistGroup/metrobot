package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/MetrolistGroup/metrobot/auditsearch"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/snowflake/v2"
)

type caseItem struct {
	CaseNumber  int64  `json:"case_number"`
	Platform    string `json:"platform"`
	ActionType  string `json:"action_type"`
	TargetID    string `json:"target_id"`
	ModeratorID string `json:"moderator_id"`
	Reason      string `json:"reason"`
	Timestamp   string `json:"timestamp"`
}

type casesResponse struct {
	Items      []caseItem `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

func auditDates(values url.Values, required bool) (from, until time.Time, err error) {
	for key, value := range values {
		if len(value) != 1 || (key != "from" && key != "to" && key != "q" && key != "before" && key != "source") {
			return from, until, errors.New("Invalid or repeated query parameter.")
		}
	}
	if len(values.Get("q")) > 4096 {
		return from, until, errors.New("Search text must be at most 4096 bytes.")
	}
	for _, name := range []string{"from", "to"} {
		value, exists := values[name]
		if !exists {
			if required {
				return from, until, errors.New("Both from and to dates are required (YYYY-MM-DD, UTC).")
			}
			continue
		}
		date, parseErr := time.Parse("2006-01-02", value[0])
		if parseErr != nil || date.Year() < 1 || date.Format("2006-01-02") != value[0] {
			return from, until, errors.New("Dates must be valid YYYY-MM-DD UTC days with year 0001 or later.")
		}
		if name == "from" {
			from = date
		} else {
			until = date.AddDate(0, 0, 1)
		}
	}
	if !from.IsZero() && !until.IsZero() && !from.Before(until) {
		return from, until, errors.New("The from date must not be after the to date.")
	}
	return from, until, nil
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, http.StatusBadRequest, "Malformed search query.")
		return
	}
	logs := r.URL.Path == "/api/logs"
	from, until, err := auditDates(values, logs)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !logs {
		h.cases(w, r, values, from, until)
		return
	}
	if !auditsearch.ValidSource(values.Get("source")) || !auditsearch.ValidRange(from, until) {
		fail(w, http.StatusBadRequest, "Choose a valid log source and a date range within Discord's supported dates (2015 onward).")
		return
	}
	var before snowflake.ID
	if value, exists := values["before"]; exists {
		if !validSnowflake(value[0]) {
			fail(w, http.StatusBadRequest, "Invalid log cursor.")
			return
		}
		before, _ = snowflake.Parse(value[0])
		if before > snowflake.New(until) || before < snowflake.New(from) {
			fail(w, http.StatusBadRequest, "Log cursor is outside the requested date range.")
			return
		}
	}
	if h.AuditLogs == nil {
		fail(w, http.StatusServiceUnavailable, auditsearch.ErrUnavailable.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), auditsearch.SearchTimeout)
	defer cancel()
	result, err := h.AuditLogs.Search(ctx, auditsearch.Query{Source: values.Get("source"), From: from, Until: until, Text: values.Get("q"), Before: before})
	if err != nil {
		status, message := http.StatusBadGateway, auditsearch.ErrFetch.Error()
		if errors.Is(err, auditsearch.ErrUnavailable) {
			status, message = http.StatusServiceUnavailable, auditsearch.ErrUnavailable.Error()
		}
		if errors.Is(err, auditsearch.ErrBusy) {
			status, message = http.StatusServiceUnavailable, auditsearch.ErrBusy.Error()
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status, message = http.StatusGatewayTimeout, "Discord log search timed out or was canceled. Try again."
		}
		fail(w, status, message)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) cases(w http.ResponseWriter, r *http.Request, values url.Values, from, until time.Time) {
	if _, exists := values["source"]; exists {
		fail(w, http.StatusBadRequest, "Cases include all platforms; source is only supported for logs.")
		return
	}
	var before int64
	if value, exists := values["before"]; exists {
		n, err := strconv.ParseInt(value[0], 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value[0] {
			fail(w, http.StatusBadRequest, "Invalid case cursor.")
			return
		}
		before = n
	}
	items, next, err := h.db.SearchCases(r.Context(), db.CaseSearch{From: from, Until: until, Text: values.Get("q"), Before: before})
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to read stored cases.")
		return
	}
	response := casesResponse{Items: []caseItem{}, NextCursor: next}
	for _, c := range items {
		response.Items = append(response.Items, caseItem{CaseNumber: c.CaseNumber, Platform: c.Platform, ActionType: c.ActionType, TargetID: c.TargetID, ModeratorID: c.ModeratorID, Reason: c.Reason, Timestamp: time.Unix(c.Timestamp, 0).UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, response)
}
