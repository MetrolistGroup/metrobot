package db

import (
	"bytes"
	"database/sql"
	"log"
	"os"
	"strings"
	"testing"
)

func TestV14ManagedNotesMatchAuthoredDocs(t *testing.T) {
	authored, err := os.ReadFile("../docs/support-notes.json")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := managedNoteVersions.ReadFile("note_versions/v14.json")
	if err != nil || !bytes.Equal(authored, embedded) {
		t.Fatal("migration and authored notes differ", err)
	}
}

func TestV14MigrationUpdatesKnownVersionsAndPreservesEdits(t *testing.T) {
	entries, err := managedNoteVersions.ReadDir("note_versions")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := readManagedNotes("note_versions/v14.json")
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "pre_v14") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			previous, err := readManagedNotes("note_versions/" + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			bodies := map[string]string{}
			for _, note := range previous {
				bodies[note.Name] = note.Content
			}
			bodies["up"] = "staff-edited update instructions"
			delete(bodies, "logs")
			path := legacyNotes(t, bodies)
			conn, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`ALTER TABLE notes ADD COLUMN short_desc TEXT NOT NULL DEFAULT ''`); err != nil {
				t.Fatal(err)
			}
			for _, note := range previous {
				if _, err := conn.Exec(`UPDATE notes SET short_desc = ? WHERE name = ?`, note.Description, note.Name); err != nil {
					t.Fatal(err)
				}
			}
			conn.Close()
			var logs bytes.Buffer
			original := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(original)
			d, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, note := range current {
				got, err := d.GetNote(note.Name)
				switch note.Name {
				case "logs":
					if got != "" {
						t.Fatal("missing note was recreated")
					}
				case "up":
					if got != bodies["up"] || !strings.Contains(logs.String(), "up: skipped, staff-edited or unknown version; manual review required") {
						t.Fatal("staff edit lost or no review log", got)
					}
				default:
					if _, present := bodies[note.Name]; !present {
						if got != "" {
							t.Fatal("absent note created", note.Name)
						}
						continue
					}
					if err != nil || got != note.Content {
						t.Fatalf("%s: got %q, err %v", note.Name, got, err)
					}
					_, markup, err := d.GetNoteForDelivery(note.Name)
					if err != nil || (!markup && note.Name != "appname" && note.Name != "report") {
						t.Fatal("updated note lost markup", note.Name, err)
					}
				}
			}
			if err := d.EditNote("repo", "staff description", "staff changed it after migration"); err != nil {
				t.Fatal(err)
			}
			d.Close()
			logs.Reset()
			d, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if got, _ := d.GetNote("repo"); got != "staff changed it after migration" || logs.Len() != 0 {
				t.Fatal("migration repeated")
			}
		})
	}
}

func TestV14MigrationPreservesDescriptionEditsAndRollsBack(t *testing.T) {
	d, err := Open(legacyNotes(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	previous, _ := readManagedNotes("note_versions/pre_v14.json")
	for _, note := range previous {
		if err := d.AddNote(note.Name, note.Description, note.Content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.conn.Exec(`DELETE FROM migrations WHERE name = 'metrolist_v14_notes_v1'; UPDATE notes SET short_desc = 'staff description' WHERE name = 'repo'; UPDATE notes SET short_desc = '', markup_enabled = 0 WHERE name = 'ver'; CREATE TRIGGER reject_v14 BEFORE UPDATE ON notes WHEN NEW.name = 'whatsnightly' BEGIN SELECT RAISE(ABORT, 'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.migrateV14Notes(); err == nil {
		t.Fatal("expected transaction failure")
	}
	for _, note := range previous {
		if got, _ := d.GetNote(note.Name); got != note.Content {
			t.Fatal("partial migration", note.Name)
		}
	}
	if _, err := d.conn.Exec(`DROP TRIGGER reject_v14`); err != nil {
		t.Fatal(err)
	}
	if err := d.migrateV14Notes(); err != nil {
		t.Fatal(err)
	}
	for _, note := range previous {
		if note.Name == "repo" || note.Name == "ver" {
			if got, _ := d.GetNote(note.Name); got != note.Content {
				t.Fatal("description-only edit was overwritten")
			}
		}
	}
	_, markup, err := d.GetNoteForDelivery("ver")
	if err != nil || markup {
		t.Fatal("opted-out note markup changed", err)
	}
	var done int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM migrations WHERE name = 'metrolist_v14_notes_v1'`).Scan(&done); err != nil || done != 1 {
		t.Fatal("missing marker", err)
	}
}

func TestV14MigrationReportsExactLatestSet(t *testing.T) {
	d, err := Open(legacyNotes(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	old, _ := readManagedNotes("note_versions/pre_v14.json")
	for _, note := range old {
		if err := d.AddNote(note.Name, note.Description, note.Content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.conn.Exec(`DELETE FROM migrations WHERE name = 'metrolist_v14_notes_v1'`); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(writer)
	if err := d.migrateV14Notes(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"discover", "download", "kmp", "logs", "nightly", "playback", "playbackkmp", "repo", "rpc", "uploads", "up", "ver", "whatsnightly"} {
		if !strings.Contains(output.String(), "migration: "+name+": updated") {
			t.Fatal("missing update", name, output.String())
		}
	}
	for _, name := range []string{"appname", "report"} {
		if !strings.Contains(output.String(), name+": skipped, already current") {
			t.Fatal("unchanged note was rewritten", name)
		}
	}
	if strings.Count(output.String(), ": updated") != 13 {
		t.Fatal("unexpected update count", output.String())
	}
}
