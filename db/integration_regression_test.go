package db

import (
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

func TestLegacyFAQKeepsDirectiveExamplesLiteral(t *testing.T) {
	body := "# Help\n## How do I randomize?\nUse this example:\n```random\n[\"one\", \"two\"]\n```\n## Where is support?\nIn the support channel."
	d, err := Open(legacyNotes(t, map[string]string{"kmp": body}))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	raw, enabled, err := d.GetNoteForDelivery("kmp")
	if err != nil || !enabled {
		t.Fatalf("migration enabled=%v err=%v", enabled, err)
	}
	parsed, err := notefmt.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	result := parsed.Render()
	if result.FAQ == nil || len(result.FAQ.Items) != 2 || result.Text != body || !strings.Contains(result.FAQ.Items[0].Answer, "```random") {
		t.Fatalf("lost legacy FAQ or interpreted its example: %+v", result)
	}
}
