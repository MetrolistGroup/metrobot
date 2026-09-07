package cmd

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/db"
)

type NotesHandler struct {
	DB *db.DB
}

const noteShortDescriptionMaxLength = 160

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
	for _, note := range names {
		sb.WriteString(fmt.Sprintf("• `%s`", note.Name))
		if note.ShortDesc != "" {
			sb.WriteString(" - " + note.ShortDesc)
		}
		sb.WriteByte('\n')
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

func (h *NotesHandler) AddNote(name, shortDesc, content string) error {
	name, shortDesc, content, err := validateNote(name, shortDesc, content)
	if err != nil {
		return err
	}
	return h.DB.AddNote(name, shortDesc, content)
}

func (h *NotesHandler) EditNote(name, shortDesc, content string) error {
	name, shortDesc, content, err := validateNote(name, shortDesc, content)
	if err != nil {
		return err
	}
	return h.DB.EditNote(name, shortDesc, content)
}

func validateNote(name, shortDesc, content string) (string, string, string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	shortDesc = strings.TrimSpace(shortDesc)
	content = normalizeNoteContent(content)
	switch {
	case name == "":
		return "", "", "", fmt.Errorf("note name is required")
	case strings.ContainsFunc(name, unicode.IsSpace):
		return "", "", "", fmt.Errorf("note name cannot contain spaces")
	case shortDesc == "":
		return "", "", "", fmt.Errorf("note short description is required")
	case strings.ContainsAny(shortDesc, "\r\n"):
		return "", "", "", fmt.Errorf("note short description must be one line")
	case utf8.RuneCountInString(shortDesc) > noteShortDescriptionMaxLength:
		return "", "", "", fmt.Errorf("note short description cannot exceed %d characters", noteShortDescriptionMaxLength)
	case strings.TrimSpace(content) == "":
		return "", "", "", fmt.Errorf("note content is required")
	}
	return name, shortDesc, content, nil
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
