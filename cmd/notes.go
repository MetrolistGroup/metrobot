package cmd

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

type NotesHandler struct {
	DB *db.DB
}

const noteShortDescriptionMaxLength = 160

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

// GetNote returns readable Markdown for Telegram and AI callers.
func (h *NotesHandler) GetNote(name string) (string, error) {
	note, err := h.GetNoteDocument(name)
	if err != nil {
		return "", err
	}
	return note.Render().Text, nil
}

// GetNoteDocument preserves authored structure; editor APIs continue using raw DB reads.
func (h *NotesHandler) GetNoteDocument(name string) (*notefmt.Note, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil, fmt.Errorf("note name is required")
	}

	content, markupEnabled, err := h.DB.GetNoteForDelivery(name)
	if err != nil {
		return nil, fmt.Errorf("fetching note: %w", err)
	}

	if content == "" {
		return nil, fmt.Errorf("note %q not found", name)
	}

	content = normalizeNoteContent(content)
	if !markupEnabled {
		return &notefmt.Note{Parts: []notefmt.Part{{Text: content}}}, nil
	}
	note, err := notefmt.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parsing note %q: %w", name, err)
	}
	return note, nil
}

func (h *NotesHandler) AddNote(name, shortDesc, content string) error {
	name, shortDesc, content, err := ValidateNote(name, shortDesc, content)
	if err != nil {
		return err
	}
	return h.DB.AddNote(name, shortDesc, content)
}

func (h *NotesHandler) EditNote(name, shortDesc, content string) error {
	name, shortDesc, content, err := ValidateNote(name, shortDesc, content)
	if err != nil {
		return err
	}
	return h.DB.EditNote(name, shortDesc, content)
}

func (h *NotesHandler) RenameNote(oldName, newName string) error {
	oldName, err := validateNoteName(oldName)
	if err != nil {
		return err
	}
	newName, err = validateNoteName(newName)
	if err != nil {
		return err
	}
	return h.DB.RenameNote(oldName, newName)
}

// ValidateNote applies the shared note validation and command-time normalization.
func ValidateNote(name, shortDesc, content string) (string, string, string, error) {
	name, err := validateNoteName(name)
	if err != nil {
		return "", "", "", err
	}
	shortDesc = strings.TrimSpace(shortDesc)
	content = normalizeNoteContent(content)
	switch {
	case shortDesc == "":
		return "", "", "", fmt.Errorf("note short description is required")
	case strings.ContainsAny(shortDesc, "\r\n"):
		return "", "", "", fmt.Errorf("note short description must be one line")
	case utf8.RuneCountInString(shortDesc) > noteShortDescriptionMaxLength:
		return "", "", "", fmt.Errorf("note short description cannot exceed %d characters", noteShortDescriptionMaxLength)
	case strings.TrimSpace(content) == "":
		return "", "", "", fmt.Errorf("note content is required")
	}
	note, err := notefmt.Parse(content)
	if err != nil {
		return "", "", "", err
	}
	if err := note.Validate(); err != nil {
		return "", "", "", err
	}
	return name, shortDesc, content, nil
}

func validateNoteName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "":
		return "", fmt.Errorf("note name is required")
	case strings.ContainsFunc(name, unicode.IsSpace):
		return "", fmt.Errorf("note name cannot contain spaces")
	}
	return name, nil
}

func (h *NotesHandler) DeleteNote(name string) error {
	name, err := validateNoteName(name)
	if err != nil {
		return err
	}
	return h.DB.DeleteNote(name)
}

func normalizeNoteContent(content string) string {
	return notefmt.Normalize(content)
}
