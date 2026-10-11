package dashboard

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
)

func readPrompt(t *testing.T, h *Handler, cookie *http.Cookie) promptResponse {
	t.Helper()
	w := request(t, h, http.MethodGet, "/api/prompt", "", cookie, "")
	checkStatus(t, w, http.StatusOK)
	var result promptResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("prompt route bypassed security headers")
	}
	return result
}

func TestPromptDefaultReplaceResetAndStateUnchanged(t *testing.T) {
	h := newTestHandler(t, nil) // Prompt editing is available even when AI/memory is disabled.
	cookie, csrf := seedSession(t, h, permanentID)
	want := promptResponse{cmd.GarminSystemPrompt(), cmd.GarminSystemPrompt(), false}
	if got := readPrompt(t, h, cookie); got != want {
		t.Fatalf("default = %+v", got)
	}
	content := "  edited base\r\nwith literal \\n and é\n  "
	body, _ := json.Marshal(map[string]string{"content": content})
	w := request(t, h, http.MethodPost, "/api/prompt/replace", string(body), cookie, csrf)
	checkStatus(t, w, http.StatusOK)
	if strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("replace response = %s", w.Body)
	}
	want.Content, want.Overridden = content, true
	if got := readPrompt(t, h, cookie); got != want {
		t.Fatalf("override = %+v", got)
	}
	state := request(t, h, http.MethodGet, "/api/state", "", cookie, "")
	checkStatus(t, state, http.StatusOK)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(state.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 || fields["prompt"] != nil || fields["default_content"] != nil || fields["content"] != nil {
		t.Fatal("state eagerly exposes prompt")
	}
	for range 2 {
		checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/reset", `{}`, cookie, csrf), http.StatusOK)
	}
	want.Content, want.Overridden = cmd.GarminSystemPrompt(), false
	if got := readPrompt(t, h, cookie); got != want {
		t.Fatalf("reset = %+v", got)
	}
}

func TestPromptRequiresActivePermanentAdmin(t *testing.T) {
	for _, scenario := range []string{"unauthenticated", "nonadmin", "regular-admin", "revoked-permanent", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			h := newTestHandler(t, nil)
			var cookie *http.Cookie
			var csrf string
			status := http.StatusForbidden
			switch scenario {
			case "unauthenticated":
				status = http.StatusUnauthorized
			case "nonadmin":
				cookie, csrf = seedSession(t, h, otherID)
			case "regular-admin":
				if err := h.db.AddAdmin("discord", regularID, permanentID); err != nil {
					t.Fatal(err)
				}
				cookie, csrf = seedSession(t, h, regularID)
			case "revoked-permanent":
				cookie, csrf = seedSession(t, h, permanentID)
				if err := h.db.AddAdmin("discord", permanentID, permanentID); err != nil {
					t.Fatal(err)
				}
				h.cfg.PermaAdminDiscordIDs = nil
			case "expired":
				cookie, csrf = seedSession(t, h, permanentID)
				s := h.sessions[cookie.Value]
				s.expires = time.Now().Add(-time.Second)
				h.sessions[cookie.Value] = s
				status = http.StatusUnauthorized
			}
			for _, path := range []string{"/api/prompt", "/api/prompt/replace", "/api/prompt/reset"} {
				// Revoked/nonadmin sessions are invalidated on the first request.
				if cookie != nil && scenario == "nonadmin" {
					cookie, csrf = seedSession(t, h, otherID)
				}
				method, body := http.MethodPost, `{}`
				if path == "/api/prompt" {
					method = http.MethodGet
				} else if path == "/api/prompt/replace" {
					body = `{"content":"unauthorized edit"}`
				}
				w := request(t, h, method, path, body, cookie, csrf)
				checkStatus(t, w, status)
				if strings.Contains(w.Body.String(), "You are Metrobot") || strings.Contains(w.Body.String(), "private-") {
					t.Fatal("authorization failure leaked prompt or secrets")
				}
			}
			if _, overridden, err := h.db.GetGarminSystemPrompt(); err != nil || overridden {
				t.Fatalf("unauthorized request wrote prompt: %v, %v", overridden, err)
			}
		})
	}
}

func TestPromptMutationCSRFOriginAndMethods(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, csrf := seedSession(t, h, permanentID)
	for _, path := range []string{"/api/prompt/replace", "/api/prompt/reset"} {
		checkStatus(t, request(t, h, http.MethodGet, path, "", cookie, ""), http.StatusMethodNotAllowed)
		for _, token := range []string{"", "wrong"} {
			checkStatus(t, request(t, h, http.MethodPost, path, `{}`, cookie, token), http.StatusForbidden)
		}
		r := httptest.NewRequest(http.MethodPost, h.cfg.Dashboard.BaseURL+path, strings.NewReader(`{}`))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Origin", "https://untrusted.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		checkStatus(t, w, http.StatusForbidden)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/prompt", `{}`, cookie, csrf), http.StatusMethodNotAllowed)
	if _, overridden, err := h.db.GetGarminSystemPrompt(); err != nil || overridden {
		t.Fatalf("CSRF request wrote prompt: %v, %v", overridden, err)
	}
}

func TestPromptValidationDoesNotWrite(t *testing.T) {
	h := newTestHandler(t, nil)
	cookie, csrf := seedSession(t, h, permanentID)
	if err := h.db.SetGarminSystemPrompt("keep this base"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{}`, `null`, `[]`, `"text"`, `42`, `true`, `{"content":null}`, `{"content":12}`,
		`{"content":""}`, `{"content":" \n\t"}`, `{"content":"\u2003"}`,
		`{"content":"new","unknown":true}`, `{"content":"new","name":"unused"}`,
		`{"content":"new"} {}`, `{"content":"new"} trailing`, `{"content":`,
		"{\"content\":\"" + string([]byte{0xff}) + "\"}",
		`{"content":"` + strings.Repeat("x", db.GarminPromptMaxSize+1) + `"}`,
		strings.Repeat(" ", maxBody+1),
	} {
		checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/replace", body, cookie, csrf), http.StatusBadRequest)
		if got := readPrompt(t, h, cookie); !got.Overridden || got.Content != "keep this base" {
			t.Fatal("invalid replacement changed stored prompt")
		}
	}
	for _, body := range []string{`null`, `[]`, `"text"`, `{"content":"ignored"}`, `{"unknown":true}`, `{} {}`, `{} trailing`, ""} {
		checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/reset", body, cookie, csrf), http.StatusBadRequest)
		if got := readPrompt(t, h, cookie); !got.Overridden || got.Content != "keep this base" {
			t.Fatal("invalid reset cleared stored prompt")
		}
	}
	r := httptest.NewRequest(http.MethodPost, h.cfg.Dashboard.BaseURL+"/api/prompt/replace", strings.NewReader(`{"content":"new"}`))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkStatus(t, w, http.StatusUnsupportedMediaType)
	content := strings.Repeat("é", db.GarminPromptMaxSize/2)
	body, _ := json.Marshal(map[string]string{"content": content})
	checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/replace", string(body), cookie, csrf), http.StatusOK)
	if got := readPrompt(t, h, cookie); got.Content != content {
		t.Fatal("64 KiB UTF-8 content changed")
	}
}

func TestPromptStorageErrorsAndMetadataOnlyAudit(t *testing.T) {
	h := newTestHandler(t, nil)
	path := filepath.Join(t.TempDir(), "prompt.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	h.db = database
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cookie, csrf := seedSession(t, h, permanentID)
	checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/replace", `{"content":"private prompt text"}`, cookie, csrf), http.StatusOK)
	checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/reset", `{}`, cookie, csrf), http.StatusOK)
	rows, err := conn.Query(`SELECT platform, moderator_id, target_id, action, reason FROM mod_actions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"prompt_replace", "prompt_reset"} {
		var platform, callerID, targetID, gotAction, reason string
		if !rows.Next() || rows.Scan(&platform, &callerID, &targetID, &gotAction, &reason) != nil {
			t.Fatal("missing prompt audit metadata")
		}
		if platform != "discord" || callerID != permanentID || targetID != "" || gotAction != action || reason != "" {
			t.Fatalf("unexpected audit entry: %q %q %q %q %q", platform, callerID, targetID, gotAction, reason)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected audit rows")
	}
	rows.Close()
	if _, err := conn.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON mod_actions BEGIN SELECT RAISE(ABORT, 'audit rejected'); END`); err != nil {
		t.Fatal(err)
	}
	checkStatus(t, request(t, h, http.MethodPost, "/api/prompt/replace", `{"content":"saved despite audit failure"}`, cookie, csrf), http.StatusOK)
	if got := readPrompt(t, h, cookie); got.Content != "saved despite audit failure" {
		t.Fatal("audit failure obscured saved state")
	}
	if _, err := conn.Exec(`CREATE TRIGGER reject_prompt BEFORE UPDATE ON settings BEGIN SELECT RAISE(ABORT, 'private prompt.db path'); END`); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, http.MethodPost, "/api/prompt/replace", `{"content":"must fail"}`, cookie, csrf)
	checkStatus(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "prompt.db") || readPrompt(t, h, cookie).Content != "saved despite audit failure" {
		t.Fatal("failed save leaked details or cleared previous prompt")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/prompt", "/api/prompt/replace", "/api/prompt/reset"} {
		method, body := http.MethodPost, `{}`
		if path == "/api/prompt" {
			method = http.MethodGet
		} else if path == "/api/prompt/replace" {
			body = `{"content":"new"}`
		}
		w := request(t, h, method, path, body, cookie, csrf)
		checkStatus(t, w, http.StatusInternalServerError)
		if strings.Contains(w.Body.String(), "prompt.db") || strings.Contains(w.Body.String(), "database") {
			t.Fatal("storage failure leaked details")
		}
	}
}
