package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/auditsearch"
	"github.com/disgoorg/snowflake/v2"
)

type searchMock struct {
	query auditsearch.Query
	calls int
	err   error
}

func (m *searchMock) Search(ctx context.Context, q auditsearch.Query) (auditsearch.Result, error) {
	m.calls++
	m.query = q
	if _, ok := ctx.Deadline(); !ok {
		return auditsearch.Result{}, errors.New("missing search timeout")
	}
	return auditsearch.Result{Items: []auditsearch.Item{}, NextCursor: "123", Scanned: 200, Warnings: []string{"date warning"}}, m.err
}

func TestAuditAuthenticationRevocationMethodsAndOrigin(t *testing.T) {
	for _, path := range []string{"/api/cases", "/api/logs?source=message&from=2026-03-29&to=2026-03-29"} {
		h := newTestHandler(t, nil)
		mock := &searchMock{}
		h.AuditLogs = mock
		checkStatus(t, request(t, h, http.MethodGet, path, "", nil, ""), http.StatusUnauthorized)
		cookie, _ := seedSession(t, h, regularID)
		if err := h.db.AddAdmin("discord", regularID, permanentID); err != nil {
			t.Fatal(err)
		}
		checkStatus(t, request(t, h, http.MethodPost, path, "", cookie, ""), http.StatusMethodNotAllowed)
		checkStatus(t, request(t, h, http.MethodHead, path, "", cookie, ""), http.StatusMethodNotAllowed)
		r := httptest.NewRequest(http.MethodGet, h.cfg.Dashboard.BaseURL+path, nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		checkStatus(t, w, http.StatusForbidden)
		checkStatus(t, request(t, h, http.MethodGet, path, "", cookie, ""), http.StatusOK)
		if err := h.db.RemoveAdmin("discord", regularID); err != nil {
			t.Fatal(err)
		}
		checkStatus(t, request(t, h, http.MethodGet, path, "", cookie, ""), http.StatusForbidden)
	}
}

func TestAuditMalformedQueries(t *testing.T) {
	h := newTestHandler(t, nil)
	mock := &searchMock{}
	h.AuditLogs = mock
	cookie, _ := seedSession(t, h, permanentID)
	for _, path := range []string{
		"/api/cases?from=2026-02-30", "/api/cases?from=2026-3-29", "/api/cases?to=", "/api/cases?from=2026-03-30&to=2026-03-29",
		"/api/cases?before=0", "/api/cases?before=-1", "/api/cases?before=01", "/api/cases?before=+2", "/api/cases?before=9223372036854775808",
		"/api/cases?source=discord", "/api/cases?guild=123", "/api/cases?from=2026-03-29&from=2026-03-30", "/api/cases?q=%ZZ", "/api/cases?q=a;b", "/api/cases?q=" + strings.Repeat("x", 4097),
		"/api/logs", "/api/logs?source=message&from=2026-03-29", "/api/logs?source=message&from=&to=2026-03-29",
		"/api/logs?source=other&from=2026-03-29&to=2026-03-29", "/api/logs?source=server&from=2014-12-31&to=2015-01-01",
		"/api/logs?source=server&from=9999-01-01&to=9999-01-01", "/api/logs?source=server&from=2026-03-29&to=2026-03-29&before=18446744073709551616",
		"/api/logs?source=server&from=2026-03-29&to=2026-03-29&before=18446744073709551615",
		"/api/logs?source=server&from=2026-03-29&to=2026-03-29&before=1",
		"/api/logs?source=server&from=2026-03-29&to=2026-03-29&before=abc", "/api/logs?source=server&from=2026-03-29&to=2026-03-29&channel=123",
	} {
		t.Run(path, func(t *testing.T) {
			checkStatus(t, request(t, h, http.MethodGet, path, "", cookie, ""), http.StatusBadRequest)
		})
	}
	if mock.calls != 0 {
		t.Fatal("malformed query reached Discord")
	}
}

func TestLogsQueryUTCContractAndSanitizedErrors(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, _ := seedSession(t, h, permanentID)
	mock := &searchMock{}
	h.AuditLogs = mock
	from := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	before := snowflake.New(from.Add(time.Hour))
	path := fmt.Sprintf("/api/logs?source=moderation&from=2026-03-29&to=2026-03-29&q=literal%%25_&before=%s", before)
	w := request(t, h, http.MethodGet, path, "", cookie, "")
	checkStatus(t, w, http.StatusOK)
	if mock.query.From != from || mock.query.Until != from.AddDate(0, 0, 1) || mock.query.Text != "literal%_" || mock.query.Before != before || mock.query.Source != "moderation" {
		t.Fatalf("query=%+v", mock.query)
	}
	var result auditsearch.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Complete || result.NextCursor != "123" || result.Scanned != 200 || result.Items == nil || result.Warnings == nil {
		t.Fatalf("contract=%s err=%v", w.Body, err)
	}
	for _, test := range []struct {
		err    error
		status int
	}{
		{errors.New("private-bot-token: network detail"), http.StatusBadGateway},
		{fmt.Errorf("private-bot-token: %w", auditsearch.ErrUnavailable), http.StatusServiceUnavailable},
		{fmt.Errorf("private-bot-token: %w", auditsearch.ErrBusy), http.StatusServiceUnavailable},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
	} {
		mock.err = test.err
		w := request(t, h, http.MethodGet, path, "", cookie, "")
		checkStatus(t, w, test.status)
		if strings.Contains(w.Body.String(), "private-bot-token") {
			t.Fatal("leaked upstream details")
		}
	}
	h.AuditLogs = nil
	checkStatus(t, request(t, h, http.MethodGet, path, "", cookie, ""), http.StatusServiceUnavailable)
	checkStatus(t, request(t, h, http.MethodGet, "/api/cases", "", cookie, ""), http.StatusOK)
}

func TestCasesAPIAllPlatformsPaginationUTCAndFailure(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, _ := seedSession(t, h, permanentID)
	for i := 0; i < 53; i++ {
		platform := "discord"
		if i%2 == 0 {
			platform = "telegram"
		}
		if _, err := h.db.CreateCase(platform, "warn", "target", "moderator", "Literal 100%_reason"); err != nil {
			t.Fatal(err)
		}
	}
	w := request(t, h, http.MethodGet, "/api/cases?q=100%25_", "", cookie, "")
	checkStatus(t, w, http.StatusOK)
	var page casesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 || page.Items[0].CaseNumber != 53 || page.NextCursor != "4" || page.Items[0].Platform != "telegram" || page.Items[1].Platform != "discord" || !strings.HasSuffix(page.Items[0].Timestamp, "Z") {
		t.Fatalf("page=%+v", page)
	}
	w = request(t, h, http.MethodGet, "/api/cases?before="+page.NextCursor, "", cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.NextCursor != "" || page.Items[2].CaseNumber != 1 {
		t.Fatalf("final page=%+v", page)
	}
	for _, query := range []string{"?from=2000-01-01", "?to=9999-12-30"} {
		checkStatus(t, request(t, h, http.MethodGet, "/api/cases"+query, "", cookie, ""), http.StatusOK)
	}
	w = request(t, h, http.MethodGet, "/api/cases?from=2000-01-01&to=2000-01-01", "", cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 0 || page.Items == nil {
		t.Fatalf("empty contract=%s %v", w.Body, err)
	}
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	checkStatus(t, request(t, h, http.MethodGet, "/api/cases", "", cookie, ""), http.StatusInternalServerError)
}
