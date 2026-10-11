package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/util"
)

type promptResponse struct {
	Content        string `json:"content"`
	DefaultContent string `json:"default_content"`
	Overridden     bool   `json:"overridden"`
}

// prompt is reached only after the shared method, session and mutation CSRF checks.
func (h *Handler) prompt(w http.ResponseWriter, r *http.Request, s session) {
	if !h.db.IsPermaAdmin("discord", s.id, h.cfg) {
		fail(w, http.StatusForbidden, "Only permanent admins may manage the system prompt.")
		return
	}
	if r.Method == http.MethodGet {
		content, overridden, err := h.db.GetGarminSystemPrompt()
		if err != nil {
			fail(w, http.StatusInternalServerError, "Unable to read system prompt.")
			return
		}
		if !overridden {
			content = cmd.GarminSystemPrompt()
		}
		writeJSON(w, http.StatusOK, promptResponse{content, cmd.GarminSystemPrompt(), overridden})
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "Use application/json.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil || !util.ValidJSONUnicode(body) {
		fail(w, http.StatusBadRequest, "Invalid UTF-8 JSON object or request body too large.")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var replacement *struct {
		Content string `json:"content"`
	}
	var reset *struct{}
	var input any = &replacement
	if r.URL.Path == "/api/prompt/reset" {
		input = &reset
	}
	if err := decoder.Decode(input); err != nil || (replacement == nil && reset == nil) {
		fail(w, http.StatusBadRequest, "Invalid JSON object.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, http.StatusBadRequest, "Expected one JSON object.")
		return
	}
	action := "prompt_replace"
	if r.URL.Path == "/api/prompt/reset" {
		err = h.db.ResetGarminSystemPrompt()
		action = "prompt_reset"
	} else {
		err = h.db.SetGarminSystemPrompt(replacement.Content)
	}
	if errors.Is(err, db.ErrInvalidGarminPrompt) {
		fail(w, http.StatusBadRequest, "Prompt must be nonblank valid UTF-8 and at most 64 KiB.")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "Unable to save system prompt.")
		return
	}
	// Audit is best-effort after commit: its failure must not report an already saved edit as failed.
	_ = h.db.LogModAction("discord", s.id, "", action, "")
	okJSON(w)
}
