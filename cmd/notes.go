package cmd

import (
	"fmt"
	"strings"

	"github.com/MetrolistGroup/metrobot/db"
)

type NotesHandler struct {
	DB *db.DB
}

var noteContentNormalizer = strings.NewReplacer(
	"\r\n", "\n",
	`\r\n`, "\n",
	`\n`, "\n",
)

func (h *NotesHandler) ListNotes() (string, error) {
	names, err := h.DB.ListNotes()
	if err != nil {
		return "", fmt.Errorf("listing notes: %w", err)
	}

	if len(names) == 0 {
		return "No notes saved yet.", nil
	}

	var sb strings.Builder
	sb.WriteString("📝 **Available notes:**\n")
	for _, name := range names {
		sb.WriteString(fmt.Sprintf("• `%s`\n", name))
	}
	return sb.String(), nil
}

func (h *NotesHandler) GetNote(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", fmt.Errorf("note name is required")
	}

	content, err := h.DB.GetNote(name)
	if err != nil {
		return "", fmt.Errorf("fetching note: %w", err)
	}

	if content == "" {
		return "", fmt.Errorf("note %q not found", name)
	}

	return normalizeNoteContent(content), nil
}

func (h *NotesHandler) AddNote(name, content string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	content = normalizeNoteContent(content)
	if name == "" {
		return fmt.Errorf("note name is required")
	}
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	return h.DB.AddNote(name, content)
}

func (h *NotesHandler) EditNote(name, content string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	content = normalizeNoteContent(content)
	if name == "" {
		return fmt.Errorf("note name is required")
	}
	if content == "" {
		return fmt.Errorf("note content is required")
	}
	return h.DB.EditNote(name, content)
}

func (h *NotesHandler) DeleteNote(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("note name is required")
	}
	return h.DB.DeleteNote(name)
}

func normalizeNoteContent(content string) string {
	return noteContentNormalizer.Replace(content)
}
