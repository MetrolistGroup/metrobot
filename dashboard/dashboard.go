// Package dashboard provides an opt-in, local-first bot admin dashboard.
package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
)

//go:embed static/*
var assets embed.FS

const (
	stateCookie   = "metrobot_oauth_state"
	sessionCookie = "metrobot_session"
	stateTTL      = 5 * time.Minute
	sessionTTL    = 8 * time.Hour
	maxEntries    = 256
	maxBody       = 128 * 1024
	memoryLimit   = 16 * 1024 // Matches the shared GarminMemory write limit, including its trailing newline.
)

type pendingLogin struct {
	browser string
	expires time.Time
}

type session struct {
	id      string
	name    string
	csrf    string
	expires time.Time
}

type Handler struct {
	cfg      *config.Config
	db       *db.DB
	notes    *cmd.NotesHandler
	memory   *cmd.GarminMemory
	static   http.Handler
	secure   bool
	host     string
	origin   string
	client   *http.Client
	tokenURL string
	userURL  string
	mu       sync.Mutex
	states   map[string]pendingLogin
	sessions map[string]session
}

// New does not connect to Discord. memory must be the bot's actual shared instance.
func New(cfg *config.Config, database *db.DB, memory *cmd.GarminMemory) (*Handler, error) {
	if cfg == nil || database == nil || !cfg.Dashboard.Enabled {
		return nil, fmt.Errorf("dashboard requires enabled configuration and database")
	}
	if err := cfg.Dashboard.Validate(); err != nil {
		return nil, err
	}
	if !validSnowflake(cfg.DiscordGuildID) {
		return nil, fmt.Errorf("dashboard requires a valid discord_guild_id")
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("loading dashboard assets: %w", err)
	}
	base, _ := url.Parse(cfg.Dashboard.BaseURL)
	return &Handler{
		cfg: cfg, db: database, notes: &cmd.NotesHandler{DB: database}, memory: memory,
		static: http.FileServer(http.FS(static)), secure: base.Scheme == "https",
		host:     canonicalHost(base.Host, base.Scheme == "https"),
		origin:   base.Scheme + "://" + canonicalHost(base.Host, base.Scheme == "https"),
		client:   &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		tokenURL: "https://discord.com/api/oauth2/token", userURL: "https://discord.com/api/users/@me",
		states: make(map[string]pendingLogin), sessions: make(map[string]session),
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	// Pin the configured origin to prevent DNS rebinding against the local listener.
	if canonicalHost(r.Host, h.secure) != h.host {
		fail(w, http.StatusBadRequest, "Invalid dashboard host.")
		return
	}
	switch r.URL.Path {
	case "/", "/app.js", "/style.css":
		if method(w, r, http.MethodGet) {
			h.static.ServeHTTP(w, r)
		}
	case "/auth/login":
		if method(w, r, http.MethodGet) {
			h.login(w, r)
		}
	case "/auth/callback":
		if method(w, r, http.MethodGet) {
			h.callback(w, r)
		}
	case "/api/state":
		if method(w, r, http.MethodGet) {
			if s, ok := h.authenticate(w, r); ok {
				h.state(w, s)
			}
		}
	case "/auth/logout", "/api/notes/create", "/api/notes/edit", "/api/notes/rename", "/api/notes/delete",
		"/api/memory/replace", "/api/memory/clear", "/api/channels/add", "/api/channels/remove", "/api/admins/add", "/api/admins/remove":
		if !method(w, r, http.MethodPost) {
			return
		}
		s, ok := h.authenticate(w, r)
		if !ok {
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrf)) != 1 || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.origin) {
			fail(w, http.StatusForbidden, "Invalid CSRF token or origin.")
			return
		}
		if r.URL.Path == "/auth/logout" {
			if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxBody)); err != nil {
				fail(w, http.StatusRequestEntityTooLarge, "Unable to read logout request or body too large.")
				return
			}
			h.invalidate(r)
			h.clearCookie(w, sessionCookie)
			okJSON(w)
			return
		}
		h.mutate(w, r, s)
	default:
		fail(w, http.StatusNotFound, "Not found.")
	}
}

func canonicalHost(host string, secure bool) string {
	host = strings.ToLower(host)
	if secure {
		return strings.TrimSuffix(host, ":443")
	}
	return strings.TrimSuffix(host, ":80")
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		w.Header().Set("Allow", want)
		fail(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return false
	}
	return true
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (h *Handler) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: h.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()), Expires: time.Now().Add(ttl)})
}

func (h *Handler) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", Secure: h.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

// pruneLocked bounds memory without a background goroutine, including idle expirations.
func (h *Handler) pruneLocked() {
	now := time.Now()
	for key, s := range h.states {
		if !now.Before(s.expires) {
			delete(h.states, key)
		}
	}
	for key, s := range h.sessions {
		if !now.Before(s.expires) {
			delete(h.sessions, key)
		}
	}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	state, err := randomToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to start login.")
		return
	}
	browser, err := randomToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to start login.")
		return
	}
	h.mu.Lock()
	h.pruneLocked()
	// A browser has only one pending login; starting again supersedes the old one.
	if cookie, err := r.Cookie(stateCookie); err == nil {
		for key, s := range h.states {
			if s.browser == cookie.Value {
				delete(h.states, key)
			}
		}
	}
	if len(h.states) >= maxEntries {
		h.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "Too many pending logins. Try again later.")
		return
	}
	h.states[state] = pendingLogin{browser: browser, expires: time.Now().Add(stateTTL)}
	h.mu.Unlock()
	h.setCookie(w, stateCookie, browser, stateTTL)
	query := url.Values{
		"client_id": {h.cfg.Dashboard.ClientID}, "redirect_uri": {h.cfg.Dashboard.BaseURL + "/auth/callback"},
		"response_type": {"code"}, "scope": {"identify"}, "state": {state},
	}
	http.Redirect(w, r, "https://discord.com/oauth2/authorize?"+query.Encode(), http.StatusFound)
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	h.clearCookie(w, stateCookie)
	state := r.URL.Query().Get("state")
	cookie, cookieErr := r.Cookie(stateCookie)
	h.mu.Lock()
	h.pruneLocked()
	pending, exists := h.states[state]
	delete(h.states, state) // Consume even failed callbacks; state is never reusable.
	h.mu.Unlock()
	if !exists || cookieErr != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(pending.browser)) != 1 {
		fail(w, http.StatusBadRequest, "Invalid or expired login state. Start login again.")
		return
	}
	code := r.URL.Query().Get("code")
	if r.URL.Query().Get("error") != "" || code == "" || len(code) > 4096 {
		fail(w, http.StatusBadRequest, "Discord login was not completed. Start login again.")
		return
	}
	form := url.Values{
		"client_id": {h.cfg.Dashboard.ClientID}, "client_secret": {h.cfg.Dashboard.ClientSecret},
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {h.cfg.Dashboard.BaseURL + "/auth/callback"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		fail(w, http.StatusBadGateway, "Discord login failed.")
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := h.discordJSON(req, &token); err != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || strings.ContainsAny(token.AccessToken, "\r\n") {
		fail(w, http.StatusBadGateway, "Discord login failed.")
		return
	}
	req, err = http.NewRequestWithContext(r.Context(), http.MethodGet, h.userURL, nil)
	if err != nil {
		fail(w, http.StatusBadGateway, "Discord login failed.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	var user struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
	}
	if err := h.discordJSON(req, &user); err != nil || !validSnowflake(user.ID) || user.Username == "" {
		fail(w, http.StatusBadGateway, "Discord identity lookup failed.")
		return
	}
	if !h.db.IsAdmin("discord", user.ID, h.cfg) {
		fail(w, http.StatusForbidden, "Only existing bot admins may sign in.")
		return
	}
	key, err := randomToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to create session.")
		return
	}
	csrf, err := randomToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to create session.")
		return
	}
	name := user.GlobalName
	if name == "" {
		name = user.Username
	}
	h.mu.Lock()
	h.pruneLocked()
	if old, err := r.Cookie(sessionCookie); err == nil {
		delete(h.sessions, old.Value)
	}
	if len(h.sessions) >= maxEntries {
		h.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "Too many active sessions. Try again later.")
		return
	}
	h.sessions[key] = session{id: user.ID, name: name, csrf: csrf, expires: time.Now().Add(sessionTTL)}
	h.mu.Unlock()
	h.setCookie(w, sessionCookie, key, sessionTTL)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) discordJSON(req *http.Request, target any) error {
	response, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("Discord request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Discord returned an error")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return fmt.Errorf("invalid Discord response")
	}
	return json.Unmarshal(body, target)
}

func (h *Handler) invalidate(r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		h.mu.Lock()
		delete(h.sessions, cookie.Value)
		h.mu.Unlock()
	}
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		fail(w, http.StatusUnauthorized, "Sign in with Discord.")
		return session{}, false
	}
	h.mu.Lock()
	h.pruneLocked()
	s, exists := h.sessions[cookie.Value]
	h.mu.Unlock()
	if !exists {
		h.clearCookie(w, sessionCookie)
		fail(w, http.StatusUnauthorized, "Session expired. Sign in again.")
		return session{}, false
	}
	if !h.db.IsAdmin("discord", s.id, h.cfg) {
		h.invalidate(r)
		h.clearCookie(w, sessionCookie)
		fail(w, http.StatusForbidden, "Bot admin access has been revoked.")
		return session{}, false
	}
	return s, true
}

type admin struct {
	ID        string `json:"id"`
	Permanent bool   `json:"permanent"`
}

type dashboardUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Permanent bool   `json:"permanent"`
}

type stateResponse struct {
	User             dashboardUser `json:"user"`
	CSRFToken        string        `json:"csrf_token"`
	Notes            []db.Note     `json:"notes"`
	Memory           string        `json:"memory"`
	MemoryAvailable  bool          `json:"memory_available"`
	ExcludedChannels []string      `json:"excluded_channels"`
	Admins           []admin       `json:"admins"`
}

func (h *Handler) state(w http.ResponseWriter, s session) {
	notes, err := h.db.ListRawNotes()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to read notes.")
		return
	}
	channels, err := h.db.ListExcludedChannels(h.cfg.DiscordGuildID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to read excluded channels.")
		return
	}
	if channels == nil {
		channels = []string{}
	}
	sort.Strings(channels)
	memory := ""
	if h.memory != nil {
		memory, err = h.memory.Read()
		if err != nil {
			fail(w, http.StatusInternalServerError, "Unable to read shared memory.")
			return
		}
	}
	permanent := h.db.IsPermaAdmin("discord", s.id, h.cfg)
	admins := []admin{}
	if permanent {
		ids, err := h.db.ListAdmins("discord")
		if err != nil {
			fail(w, http.StatusInternalServerError, "Unable to read admins.")
			return
		}
		seen := make(map[string]bool)
		for _, id := range append(ids, h.cfg.PermaAdminDiscordIDs...) {
			if !seen[id] {
				seen[id] = true
				admins = append(admins, admin{ID: id, Permanent: h.db.IsPermaAdmin("discord", id, h.cfg)})
			}
		}
		sort.Slice(admins, func(i, j int) bool { return admins[i].ID < admins[j].ID })
	}
	writeJSON(w, http.StatusOK, stateResponse{
		User: dashboardUser{s.id, s.name, permanent}, CSRFToken: s.csrf,
		Notes: notes, Memory: memory, MemoryAvailable: h.memory != nil,
		ExcludedChannels: channels, Admins: admins,
	})
}

type mutation struct {
	Name      string `json:"name"`
	ShortDesc string `json:"short_desc"`
	Content   string `json:"content"`
	NewName   string `json:"new_name"`
	ID        string `json:"id"`
}

func (h *Handler) mutate(w http.ResponseWriter, r *http.Request, s session) {
	if strings.HasPrefix(r.URL.Path, "/api/admins/") && !h.db.IsPermaAdmin("discord", s.id, h.cfg) {
		fail(w, http.StatusForbidden, "Only permanent admins may manage bot admins.")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "Use application/json.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input *mutation
	if err := decoder.Decode(&input); err != nil || input == nil {
		fail(w, http.StatusBadRequest, "Invalid JSON object or request body too large.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, http.StatusBadRequest, "Expected one JSON object.")
		return
	}
	switch r.URL.Path {
	case "/api/notes/create":
		err = h.notes.AddNote(input.Name, input.ShortDesc, input.Content)
	case "/api/notes/edit":
		var name, shortDesc string
		name, shortDesc, _, err = cmd.ValidateNote(input.Name, input.ShortDesc, input.Content)
		if err == nil {
			// The editor preserves stored escapes and line endings instead of command-time normalization.
			err = h.db.EditNote(name, shortDesc, input.Content)
		}
	case "/api/notes/rename":
		err = h.notes.RenameNote(input.Name, input.NewName)
	case "/api/notes/delete":
		err = h.notes.DeleteNote(input.Name)
	case "/api/memory/replace", "/api/memory/clear":
		if h.memory == nil {
			fail(w, http.StatusConflict, "Shared memory is unavailable while AI is disabled.")
			return
		}
		if r.URL.Path == "/api/memory/replace" {
			if content := strings.TrimSpace(input.Content); content == "" || len(content)+1 > memoryLimit {
				fail(w, http.StatusBadRequest, "Memory requires nonempty content below 16 KiB.")
				return
			}
			err = h.memory.Replace(input.Content)
		} else {
			err = h.memory.Clear()
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "Unable to save shared memory.")
			return
		}
	case "/api/channels/add", "/api/channels/remove", "/api/admins/add", "/api/admins/remove":
		if !validSnowflake(input.ID) {
			fail(w, http.StatusBadRequest, "A valid nonzero Discord snowflake ID is required.")
			return
		}
		switch r.URL.Path {
		case "/api/channels/add":
			err = h.db.AddExcludedChannel(h.cfg.DiscordGuildID, input.ID)
		case "/api/channels/remove":
			err = h.db.RemoveExcludedChannel(h.cfg.DiscordGuildID, input.ID)
		case "/api/admins/add":
			err = h.db.AddAdmin("discord", input.ID, s.id)
		case "/api/admins/remove":
			if h.db.IsPermaAdmin("discord", input.ID, h.cfg) {
				fail(w, http.StatusForbidden, "Permanent admins cannot be removed.")
				return
			}
			err = h.db.RemoveAdmin("discord", input.ID)
		}
	}
	if err != nil {
		// NotesHandler errors include useful validation/conflict details, not credentials.
		if strings.HasPrefix(r.URL.Path, "/api/notes/") {
			fail(w, http.StatusBadRequest, err.Error())
		} else {
			fail(w, http.StatusInternalServerError, "Unable to save changes.")
		}
		return
	}
	okJSON(w)
}

func validSnowflake(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n != 0 && strconv.FormatUint(n, 10) == id
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func okJSON(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
