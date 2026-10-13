package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

func legacyNotes(t *testing.T, notes map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE notes (name TEXT PRIMARY KEY, content TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for name, body := range notes {
		if _, err := conn.Exec(`INSERT INTO notes VALUES (?,?)`, name, body); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestNoteMarkupMigrationPreservesContentAndRunsOnce(t *testing.T) {
	faq := "# Original FAQ\n\n## Question\nLiteral \\n and \"quotes\"\n```go\nfmt.Println(\"hello\")\n```\n"
	donate := "Fixed intro\nFirst \\path and \"quoted\"\nSecond <https://example.com>\nThird\n"
	path := legacyNotes(t, map[string]string{"donate": donate, "kmp": faq, "ordinary": "raw\\nbody"})
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.GetNote("donate")
	if err != nil {
		t.Fatal(err)
	}
	note, err := notefmt.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Parts) != 3 || note.Parts[0].Text != "Fixed intro\n" || strings.Join(note.Parts[1].Random, "\n") != strings.TrimPrefix(donate, "Fixed intro\n") {
		t.Fatalf("donate entries lost: %#v", note.Parts)
	}
	if err := note.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err = d.GetNote("kmp")
	if err != nil {
		t.Fatal(err)
	}
	note, err = notefmt.Parse(raw)
	if err != nil || note.Render().Text != faq {
		t.Fatalf("FAQ lost content: %q, %v", raw, err)
	}
	for name, want := range map[string]bool{"donate": true, "kmp": true, "ordinary": false} {
		_, enabled, err := d.GetNoteForDelivery(name)
		if err != nil || enabled != want {
			t.Fatalf("%s interpretation = %v, %v", name, enabled, err)
		}
	}
	if err := d.EditNote("donate", "edited", "admin removed directive"); err != nil {
		t.Fatal(err)
	}
	if err := d.RenameNote("kmp", "portable"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddNote("kmp", "new", "plain FAQ name"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for name, want := range map[string]string{"donate": "admin removed directive", "kmp": "plain FAQ name", "portable": raw, "ordinary": "raw\\nbody"} {
		got, err := d.GetNote(name)
		if err != nil || got != want {
			t.Fatalf("restart changed %s: %q, %v", name, got, err)
		}
	}
}

func TestNoteMarkupMigrationSkipsDirectiveBodiesAndHandlesBlankLegacyLines(t *testing.T) {
	authored := "intro\n" + notefmt.Fence("random", `["one", "two"]`)
	path := legacyNotes(t, map[string]string{"donate": authored, "kmp": notefmt.Fence("faq", "## Q\nA")})
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for name, want := range map[string]string{"donate": authored, "kmp": notefmt.Fence("faq", "## Q\nA")} {
		got, enabled, err := d.GetNoteForDelivery(name)
		if err != nil || got != want || enabled {
			t.Fatalf("%s skipped body = %q, markup = %v, error = %v", name, got, enabled, err)
		}
	}
	path = legacyNotes(t, map[string]string{"donate": "Intro\n\nFirst\n\nSecond\n\n"})
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	raw, _ := other.GetNote("donate")
	n, err := notefmt.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(n.Parts[1].Random, "\n"); got != "\nFirst\n\nSecond\n\n" {
		t.Fatalf("blank lines lost: %q", got)
	}
}

func TestNoteMarkupMigrationHandlesEscapedSlashContentWithoutDataLoss(t *testing.T) {
	authored := "```faq\\n## Q\\nA\\n```"
	path := legacyNotes(t, map[string]string{"donate": "Intro\\nFirst\\nSecond", "kmp": authored})
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got, _ := d.GetNote("kmp"); got != authored {
		t.Fatal("escaped authored directive double-wrapped")
	}
	raw, _ := d.GetNote("donate")
	n, err := notefmt.Parse(raw)
	if err != nil || n.Parts[0].Text != "Intro\n" || strings.Join(n.Parts[1].Random, "\n") != "First\nSecond" {
		t.Fatalf("legacy slash separators lost: %q, %v", raw, err)
	}
	invalid := "Intro\n" + string([]byte{0xff})
	path = legacyNotes(t, map[string]string{"donate": invalid})
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if got, _ := other.GetNote("donate"); got != invalid {
		t.Fatal("invalid legacy bytes were silently replaced")
	}
}

func TestNoteMarkupMigrationRollbackIncludesMarker(t *testing.T) {
	path := legacyNotes(t, map[string]string{"donate": "Intro\nFirst\nSecond", "kmp": "## Q\nA"})
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TRIGGER fail_kmp BEFORE UPDATE OF content ON notes WHEN OLD.name = 'kmp' BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if d, err := Open(path); err == nil {
		d.Close()
		t.Fatal("failed migration unexpectedly committed")
	}
	var content string
	if err := conn.QueryRow(`SELECT content FROM notes WHERE name = 'donate'`).Scan(&content); err != nil || content != "Intro\nFirst\nSecond" {
		t.Fatalf("partial migration persisted: %q, %v", content, err)
	}
	var hasMarkupEnabled int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notes') WHERE name = 'markup_enabled'`).Scan(&hasMarkupEnabled); err != nil || hasMarkupEnabled != 0 {
		t.Fatalf("partial interpretation metadata persisted: %d, %v", hasMarkupEnabled, err)
	}
	if _, err := conn.Exec(`DROP TRIGGER fail_kmp`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	content, _ = d.GetNote("donate")
	if !notefmt.HasDirectives(content) {
		t.Fatal("rollback left a completion marker")
	}
}
