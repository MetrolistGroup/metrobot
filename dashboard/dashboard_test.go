package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
)

const permanentID = "100000000000000001"
const regularID = "100000000000000002"
const channelID = "100000000000000003"

func newTestHandler(t *testing.T, memory *cmd.GarminMemory) *Handler {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := &config.Config{
		DiscordGuildID: "100000000000000004", PermaAdminDiscordIDs: []string{permanentID},
		Dashboard: config.DashboardConfig{Enabled: true, BaseURL: "http://127.0.0.1:8080", ClientID: "client-id", ClientSecret: "private-client-secret"},
	}
	h, err := New(cfg, database, memory)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(t *testing.T, h *Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, h.cfg.Dashboard.BaseURL+path, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func checkStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, want, response.Body.String())
	}
	if want >= 400 {
		var result map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["error"] == "" {
			t.Fatalf("missing JSON error: %s", response.Body.String())
		}
	}
}

func seedSession(t *testing.T, h *Handler, id string) (*http.Cookie, string) {
	t.Helper()
	key, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	h.sessions[key] = session{id: id, name: "Admin <script>", csrf: csrf, expires: time.Now().Add(sessionTTL)}
	return &http.Cookie{Name: sessionCookie, Value: key}, csrf
}

func beginLogin(t *testing.T, h *Handler) (string, *http.Cookie) {
	t.Helper()
	w := request(t, h, http.MethodGet, "/auth/login", "", nil, "")
	checkStatus(t, w, http.StatusFound)
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("scope") != "identify" || u.Query().Get("redirect_uri") != h.cfg.Dashboard.BaseURL+"/auth/callback" || u.Query().Get("state") == "" {
		t.Fatalf("incorrect OAuth authorization: %s", u)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe state cookies: %+v", cookies)
	}
	return u.Query().Get("state"), cookies[0]
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func discordMock(t *testing.T, h *Handler, userID string) *int {
	t.Helper()
	calls := new(int)
	h.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		(*calls)++
		var body string
		switch r.URL.String() {
		case h.tokenURL:
			if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("client_secret") != h.cfg.Dashboard.ClientSecret || r.Form.Get("redirect_uri") != h.cfg.Dashboard.BaseURL+"/auth/callback" {
				t.Error("incorrect token exchange")
			}
			body = `{"access_token":"private-access-token","token_type":"Bearer"}`
		case h.userURL:
			if r.Header.Get("Authorization") != "Bearer private-access-token" {
				t.Error("missing OAuth bearer")
			}
			body = fmt.Sprintf(`{"id":%q,"username":"admin","global_name":"Admin <script>"}`, userID)
		default:
			t.Fatalf("unexpected network request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return calls
}

func TestOAuthStateMismatchReplayExpiryAndError(t *testing.T) {
	for _, scenario := range []string{"mismatch", "missing-cookie", "expired", "denied", "missing-code"} {
		t.Run(scenario, func(t *testing.T) {
			h := newTestHandler(t, nil)
			calls := discordMock(t, h, permanentID)
			state, cookie := beginLogin(t, h)
			query := "?state=" + url.QueryEscape(state) + "&code=code"
			switch scenario {
			case "mismatch":
				cookie.Value = "another-browser"
			case "missing-cookie":
				cookie = nil
			case "expired":
				pending := h.states[state]
				pending.expires = time.Now().Add(-time.Second)
				h.states[state] = pending
			case "denied":
				query = "?state=" + state + "&error=access_denied"
			case "missing-code":
				query = "?state=" + state
			}
			checkStatus(t, request(t, h, http.MethodGet, "/auth/callback"+query, "", cookie, ""), http.StatusBadRequest)
			checkStatus(t, request(t, h, http.MethodGet, "/auth/callback?state="+state+"&code=code", "", cookie, ""), http.StatusBadRequest)
			if *calls != 0 || len(h.sessions) != 0 {
				t.Fatal("invalid state contacted Discord or created a session")
			}
		})
	}
}

func TestOAuthSuccessRotationAndNonadmin(t *testing.T) {
	for _, scenario := range []string{"permanent", "db-admin", "nonadmin"} {
		t.Run(scenario, func(t *testing.T) {
			h := newTestHandler(t, nil)
			id := permanentID
			if scenario != "permanent" {
				id = regularID
			}
			if scenario == "db-admin" {
				if err := h.db.AddAdmin("discord", id, permanentID); err != nil {
					t.Fatal(err)
				}
			}
			calls := discordMock(t, h, id)
			oldCookie, _ := seedSession(t, h, permanentID)
			state, browser := beginLogin(t, h)
			r := httptest.NewRequest(http.MethodGet, h.cfg.Dashboard.BaseURL+"/auth/callback?state="+state+"&code=code", nil)
			r.AddCookie(browser)
			r.AddCookie(oldCookie)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if scenario == "nonadmin" {
				checkStatus(t, w, http.StatusForbidden)
				return
			}
			checkStatus(t, w, http.StatusFound)
			if *calls != 2 || w.Header().Get("Location") != "/" {
				t.Fatal("login did not finish")
			}
			if _, ok := h.sessions[oldCookie.Value]; ok {
				t.Fatal("old session was not rotated")
			}
			var fresh *http.Cookie
			for _, cookie := range w.Result().Cookies() {
				if cookie.Name == sessionCookie {
					fresh = cookie
				}
			}
			if fresh == nil || !fresh.HttpOnly || fresh.SameSite != http.SameSiteLaxMode || fresh.Value == oldCookie.Value || fresh.Secure {
				t.Fatalf("incorrect local session cookie: %+v", fresh)
			}
			checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", fresh, ""), http.StatusOK)
			checkStatus(t, request(t, h, http.MethodGet, "/auth/callback?state="+state+"&code=code", "", browser, ""), http.StatusBadRequest)
		})
	}
}

func TestOAuthFailsClosedWithoutLeakingCredentials(t *testing.T) {
	for _, scenario := range []string{"network", "status", "invalid-json", "oversized", "bad-token", "identity-error"} {
		t.Run(scenario, func(t *testing.T) {
			h := newTestHandler(t, nil)
			h.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, `{"access_token":"private-access-token","token_type":"Bearer"}`
				switch scenario {
				case "network":
					return nil, fmt.Errorf("private-client-secret")
				case "status":
					status = http.StatusUnauthorized
				case "invalid-json":
					body = "private-client-secret"
				case "oversized":
					body = strings.Repeat("x", 64*1024+1)
				case "bad-token":
					body = `{"access_token":"private-access-token","token_type":"Unknown"}`
				case "identity-error":
					if r.URL.String() == h.userURL {
						status = http.StatusForbidden
					}
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			state, cookie := beginLogin(t, h)
			w := request(t, h, http.MethodGet, "/auth/callback?state="+state+"&code=code", "", cookie, "")
			checkStatus(t, w, http.StatusBadGateway)
			if len(h.sessions) != 0 || strings.Contains(w.Body.String(), "private-") {
				t.Fatal("OAuth failure created session or leaked credentials")
			}
		})
	}
}

func TestOAuthDoesNotFollowRedirects(t *testing.T) {
	h := newTestHandler(t, nil)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/unexpected", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	h.tokenURL = upstream.URL + "/token"
	state, cookie := beginLogin(t, h)
	checkStatus(t, request(t, h, http.MethodGet, "/auth/callback?state="+state+"&code=code", "", cookie, ""), http.StatusBadGateway)
	if calls.Load() != 1 || len(h.sessions) != 0 {
		t.Fatal("OAuth followed a credential-bearing redirect")
	}
}

func TestAuthenticationCSRFLogoutAndRevocation(t *testing.T) {
	h := newTestHandler(t, nil)
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", nil, ""), http.StatusUnauthorized)
	if err := h.db.AddAdmin("discord", regularID, permanentID); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := seedSession(t, h, regularID)
	for _, path := range []string{"/auth/logout", "/api/channels/add"} {
		checkStatus(t, request(t, h, http.MethodPost, path, `{"id":"`+channelID+`"}`, cookie, ""), http.StatusForbidden)
		checkStatus(t, request(t, h, http.MethodPost, path, `{}`, cookie, "wrong"), http.StatusForbidden)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/auth/logout", strings.Repeat("x", maxBody+1), cookie, csrf), http.StatusRequestEntityTooLarge)
	checkStatus(t, request(t, h, http.MethodPost, "/auth/logout", `{}`, cookie, csrf), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", cookie, ""), http.StatusUnauthorized)
	cookie, csrf = seedSession(t, h, regularID)
	if err := h.db.RemoveAdmin("discord", regularID); err != nil {
		t.Fatal(err)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/channels/add", `{"id":"`+channelID+`"}`, cookie, csrf), http.StatusForbidden)
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", cookie, ""), http.StatusUnauthorized)
	cookie, _ = seedSession(t, h, permanentID)
	s := h.sessions[cookie.Value]
	s.expires = time.Now().Add(-time.Second)
	h.sessions[cookie.Value] = s
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", cookie, ""), http.StatusUnauthorized)
}

func readState(t *testing.T, h *Handler, cookie *http.Cookie) stateResponse {
	t.Helper()
	w := request(t, h, http.MethodGet, "/api/state", "", cookie, "")
	checkStatus(t, w, http.StatusOK)
	var state stateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Notes == nil || state.ExcludedChannels == nil || state.Admins == nil {
		t.Fatal("state contains null arrays")
	}
	if strings.Contains(w.Body.String(), "private-client-secret") {
		t.Fatal("state exposes configuration")
	}
	return state
}

func TestPermanentAdminControlsAndChannelScope(t *testing.T) {
	h := newTestHandler(t, nil)
	permanent, permanentCSRF := seedSession(t, h, permanentID)
	regular, regularCSRF := seedSession(t, h, regularID)
	checkStatus(t, request(t, h, http.MethodPost, "/api/admins/add", `{"id":"`+regularID+`"}`, permanent, permanentCSRF), http.StatusOK)
	if !h.db.IsAdmin("discord", regularID, h.cfg) || h.db.IsAdmin("telegram", regularID, h.cfg) {
		t.Fatal("admin mutation affected the wrong platform")
	}
	for _, path := range []string{"/api/admins/add", "/api/admins/remove"} {
		checkStatus(t, request(t, h, http.MethodPost, path, `{"id":"`+channelID+`"}`, regular, regularCSRF), http.StatusForbidden)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/admins/remove", `{"id":"`+permanentID+`"}`, permanent, permanentCSRF), http.StatusForbidden)
	if err := h.db.AddAdmin("discord", permanentID, permanentID); err != nil {
		t.Fatal(err)
	}
	state := readState(t, h, permanent)
	if !state.User.Permanent || len(state.Admins) != 2 || state.Admins[0] != (admin{ID: permanentID, Permanent: true}) {
		t.Fatalf("incorrect admin list: %+v", state)
	}
	if state := readState(t, h, regular); state.User.Permanent || len(state.Admins) != 0 {
		t.Fatal("regular admins can see admin management data")
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/channels/add", `{"id":"`+channelID+`"}`, regular, regularCSRF), http.StatusOK)
	if excluded, err := h.db.IsChannelExcluded(h.cfg.DiscordGuildID, channelID); err != nil || !excluded {
		t.Fatalf("channel mutation not visible in bot DB: %v, %v", excluded, err)
	}
	if excluded, err := h.db.IsChannelExcluded("another-guild", channelID); err != nil || excluded {
		t.Fatal("channel mutation escaped configured guild")
	}
	if state := readState(t, h, regular); len(state.ExcludedChannels) != 1 || state.ExcludedChannels[0] != channelID {
		t.Fatal("channel state not updated")
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/channels/remove", `{"id":"`+channelID+`"}`, regular, regularCSRF), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodPost, "/api/admins/remove", `{"id":"`+regularID+`"}`, permanent, permanentCSRF), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", regular, ""), http.StatusForbidden)
}

func TestNotesRawRoundtripAndValidation(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, csrf := seedSession(t, h, permanentID)
	content := "<script>alert('xss')</script>\nfirst\nsecond\nthird"
	body, _ := json.Marshal(mutation{Name: " DONATE ", ShortDesc: "<b>Support</b>", Content: content})
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/create", string(body), cookie, csrf), http.StatusOK)
	for range 5 {
		state := readState(t, h, cookie)
		if len(state.Notes) != 1 || state.Notes[0].Name != "donate" || state.Notes[0].Content != content || state.Notes[0].ShortDesc != "<b>Support</b>" {
			t.Fatalf("raw note changed: %+v", state.Notes)
		}
	}
	// Legacy raw escapes must not be normalized when the editor loads them.
	if err := h.db.AddNote("legacy", "Legacy", `literal\ntext`); err != nil {
		t.Fatal(err)
	}
	if state := readState(t, h, cookie); state.Notes[1].Content != `literal\ntext` {
		t.Fatal("editor normalized legacy note")
	}
	for _, input := range []mutation{
		{Name: "two words", ShortDesc: "Description", Content: "Text"},
		{Name: "name", ShortDesc: "", Content: "Text"},
		{Name: "name", ShortDesc: "two\nlines", Content: "Text"},
		{Name: "name", ShortDesc: strings.Repeat("a", 161), Content: "Text"},
		{Name: "name", ShortDesc: "Description", Content: " "},
	} {
		body, _ := json.Marshal(input)
		checkStatus(t, request(t, h, http.MethodPost, "/api/notes/create", string(body), cookie, csrf), http.StatusBadRequest)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/rename", `{"name":"donate","new_name":"support"}`, cookie, csrf), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/edit", `{"name":"support","short_desc":"New","content":"updated"}`, cookie, csrf), http.StatusOK)
	if stored, err := h.db.GetNote("support"); err != nil || stored != "updated" {
		t.Fatalf("bot cannot see note edit: %q, %v", stored, err)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/delete", `{"name":"support"}`, cookie, csrf), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/delete", `{"name":""}`, cookie, csrf), http.StatusBadRequest)
}

func TestNoteCreateAndEditPreserveRawContent(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, csrf := seedSession(t, h, permanentID)
	const content = "  <script>alert('xss')</script>\\nfirst\\r\\nsecond\r\nlast  "
	input := mutation{Name: " LEGACY ", ShortDesc: " Original description ", Content: content}
	body, _ := json.Marshal(input)
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/create", string(body), cookie, csrf), http.StatusOK)
	if stored, err := h.db.GetNote("legacy"); err != nil || stored != content {
		t.Fatalf("creation changed raw content: %q, %v", stored, err)
	}
	input.ShortDesc = " Updated description "
	input.Content = readState(t, h, cookie).Notes[0].Content
	body, _ = json.Marshal(input)
	checkStatus(t, request(t, h, http.MethodPost, "/api/notes/edit", string(body), cookie, csrf), http.StatusOK)
	if stored, err := h.db.GetNote("legacy"); err != nil || stored != content {
		t.Fatalf("description-only edit changed raw content: %q, %v", stored, err)
	}
	if state := readState(t, h, cookie); state.Notes[0].ShortDesc != "Updated description" || state.Notes[0].Content != content {
		t.Fatalf("edit did not roundtrip raw note: %+v", state.Notes[0])
	}
	for _, invalid := range []mutation{
		{Name: "two words", ShortDesc: "Description", Content: "Text"},
		{Name: "legacy", Content: "Text"},
		{Name: "legacy", ShortDesc: "two\nlines", Content: "Text"},
		{Name: "legacy", ShortDesc: strings.Repeat("a", 161), Content: "Text"},
		{Name: "legacy", ShortDesc: "Description", Content: " "},
	} {
		body, _ := json.Marshal(invalid)
		checkStatus(t, request(t, h, http.MethodPost, "/api/notes/edit", string(body), cookie, csrf), http.StatusBadRequest)
	}
	if stored, err := h.db.GetNote("legacy"); err != nil || stored != content {
		t.Fatalf("invalid edit changed raw content: %q, %v", stored, err)
	}
}

func TestNotesRespectBotReplyLimit(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, csrf := seedSession(t, h, permanentID)
	for _, test := range []struct {
		name, content string
		status        int
	}{
		{"ascii-limit", strings.Repeat("a", 2000), http.StatusOK},
		{"ascii-over", strings.Repeat("a", 2001), http.StatusBadRequest},
		{"ten-kib", strings.Repeat("a", 10*1024), http.StatusBadRequest},
		{"bmp-limit", strings.Repeat("界", 2000), http.StatusOK},
		{"surrogate-limit", strings.Repeat("\U0001d11e", 1000), http.StatusOK},
		{"surrogate-over", strings.Repeat("\U0001d11e", 1001), http.StatusBadRequest},
		{"escapes-limit", "x" + strings.Repeat(`\n`, 999) + "y", http.StatusOK},
		{"escapes-over", "x" + strings.Repeat(`\n`, 1000), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(mutation{Name: test.name, ShortDesc: "Description", Content: test.content})
			checkStatus(t, request(t, h, http.MethodPost, "/api/notes/create", string(body), cookie, csrf), test.status)
			if test.status == http.StatusBadRequest {
				if stored, err := h.db.GetNote(test.name); err != nil || stored != "" {
					t.Fatalf("oversized note was created: %q, %v", stored, err)
				}
				if err := h.db.AddNote(test.name, "Original", "original content"); err != nil {
					t.Fatal(err)
				}
			}
			checkStatus(t, request(t, h, http.MethodPost, "/api/notes/edit", string(body), cookie, csrf), test.status)
			want := test.content
			if test.status == http.StatusBadRequest {
				want = "original content"
			}
			if stored, err := h.db.GetNote(test.name); err != nil || stored != want {
				t.Fatalf("unexpected stored content: %q, %v", stored, err)
			}
		})
	}
}

func TestMemorySharedInstanceLimitsAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.md")
	memory, err := cmd.NewGarminMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHandler(t, memory)
	cookie, csrf := seedSession(t, h, permanentID)
	checkStatus(t, request(t, h, http.MethodPost, "/api/memory/replace", `{"content":"Shared facts <script>"}`, cookie, csrf), http.StatusOK)
	if got, err := memory.Read(); err != nil || got != "Shared facts <script>" {
		t.Fatalf("bot memory not updated: %q, %v", got, err)
	}
	if state := readState(t, h, cookie); !state.MemoryAvailable || state.Memory != "Shared facts <script>" {
		t.Fatal("memory state incorrect")
	}
	for _, content := range []string{" ", strings.Repeat("a", memoryLimit)} {
		body, _ := json.Marshal(mutation{Content: content})
		checkStatus(t, request(t, h, http.MethodPost, "/api/memory/replace", string(body), cookie, csrf), http.StatusBadRequest)
		if got, _ := memory.Read(); got != "Shared facts <script>" {
			t.Fatal("invalid mutation destroyed memory")
		}
	}
	boundary, _ := json.Marshal(mutation{Content: strings.Repeat("a", memoryLimit-1)})
	checkStatus(t, request(t, h, http.MethodPost, "/api/memory/replace", string(boundary), cookie, csrf), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodPost, "/api/memory/clear", `{}`, cookie, csrf), http.StatusOK)
	if got, _ := memory.Read(); got != "# Metrobot Memory" {
		t.Fatalf("memory not cleared: %q", got)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", memoryLimit+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", cookie, ""), http.StatusInternalServerError)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	checkStatus(t, request(t, h, http.MethodGet, "/api/state", "", cookie, ""), http.StatusInternalServerError)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, http.MethodPost, "/api/memory/replace", `{"content":"valid"}`, cookie, csrf)
	checkStatus(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), path) {
		t.Fatal("memory error exposes filesystem path")
	}
	h.memory = nil
	if state := readState(t, h, cookie); state.MemoryAvailable || state.Memory != "" {
		t.Fatal("disabled AI exposes memory")
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/memory/clear", `{}`, cookie, csrf), http.StatusConflict)
}

func TestRequestLimitsIDsAndSecurityHeaders(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, csrf := seedSession(t, h, permanentID)
	for _, id := range []string{"", "0", "01", "-1", "+1", " 1", "1.5", "18446744073709551616", "<script>"} {
		body, _ := json.Marshal(mutation{ID: id})
		for _, path := range []string{"/api/channels/add", "/api/channels/remove", "/api/admins/add", "/api/admins/remove"} {
			checkStatus(t, request(t, h, http.MethodPost, path, string(body), cookie, csrf), http.StatusBadRequest)
		}
	}
	for _, body := range []string{`{`, `null`, `[]`, `{} {}`, `{"unexpected":true}`, `{"id":"` + strings.Repeat("1", maxBody) + `"}`} {
		checkStatus(t, request(t, h, http.MethodPost, "/api/channels/add", body, cookie, csrf), http.StatusBadRequest)
	}
	for _, path := range []string{"/api/state", "/auth/login", "/"} {
		w := request(t, h, http.MethodGet, path, "", cookie, "")
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") == "" || strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline") || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("unsafe response headers: %+v", w.Header())
		}
	}
	r := httptest.NewRequest(http.MethodPost, h.cfg.Dashboard.BaseURL+"/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkStatus(t, w, http.StatusForbidden)
	r.Header.Del("Origin")
	r.Host = "untrusted.example"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkStatus(t, w, http.StatusBadRequest)
	checkStatus(t, request(t, h, http.MethodGet, "/api/notes/delete", "", cookie, csrf), http.StatusMethodNotAllowed)
	checkStatus(t, request(t, h, http.MethodGet, "/static/", "", nil, ""), http.StatusNotFound)
	h.secure = true
	w = httptest.NewRecorder()
	h.setCookie(w, sessionCookie, "session", sessionTTL)
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie is not secure")
	}
}

func TestOAuthUnknownStateAndSupersededLogin(t *testing.T) {
	h := newTestHandler(t, nil)
	calls := discordMock(t, h, permanentID)
	oldState, cookie := beginLogin(t, h)
	checkStatus(t, request(t, h, http.MethodGet, "/auth/callback?state=unknown&code=code", "", cookie, ""), http.StatusBadRequest)
	checkStatus(t, request(t, h, http.MethodGet, "/auth/login", "", cookie, ""), http.StatusFound)
	checkStatus(t, request(t, h, http.MethodGet, "/auth/callback?state="+oldState+"&code=code", "", cookie, ""), http.StatusBadRequest)
	if *calls != 0 || len(h.states) != 1 {
		t.Fatal("superseded login state still usable")
	}
}

func TestDefaultPortOrigin(t *testing.T) {
	h := newTestHandler(t, nil)
	h.cfg.Dashboard.BaseURL = "http://LOCALHOST:80"
	var err error
	h, err = New(h.cfg, h.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := seedSession(t, h, permanentID)
	r := httptest.NewRequest(http.MethodPost, "http://localhost/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkStatus(t, w, http.StatusOK)
}

func TestBoundedAuthStorage(t *testing.T) {
	h := newTestHandler(t, nil)
	for i := range maxEntries {
		h.states[fmt.Sprint(i)] = pendingLogin{browser: fmt.Sprint(i), expires: time.Now().Add(stateTTL)}
	}
	checkStatus(t, request(t, h, http.MethodGet, "/auth/login", "", nil, ""), http.StatusServiceUnavailable)
	h.states["0"] = pendingLogin{expires: time.Now().Add(-time.Second)}
	state, cookie := beginLogin(t, h)
	if len(h.states) != maxEntries {
		t.Fatal("state storage is not bounded")
	}
	for i := range maxEntries {
		h.sessions[fmt.Sprint(i)] = session{id: permanentID, expires: time.Now().Add(sessionTTL)}
	}
	discordMock(t, h, permanentID)
	checkStatus(t, request(t, h, http.MethodGet, "/auth/callback?state="+state+"&code=code", "", cookie, ""), http.StatusServiceUnavailable)
	if len(h.sessions) != maxEntries {
		t.Fatal("session storage is not bounded")
	}
}
