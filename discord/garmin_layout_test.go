package discord

import (
	"strings"
	"testing"
)

func TestGarminLayoutReply(t *testing.T) {
	const layout = "```components-v2\n{\"components\":[{\"type\":\"text\",\"content\":\"get the latest build\"},{\"type\":\"buttons\",\"buttons\":[{\"label\":\"Releases\",\"url\":\"https://github.com/MetrolistGroup/Metrolist/releases\"}]}]}\n```"
	for _, test := range []struct {
		name, reply string
		want        bool
	}{
		{"layout with text around it", "here you go\n" + layout + "\nback up first", true},
		{"plain text", "open settings, then updater", false},
		{"invalid json", "```components-v2\n{\"components\":[{\"type\":\"text\"}]}\n```", false},
		{"insecure button", strings.Replace(layout, "https://", "http://", 1), false},
		{"faq needs a saved note", "```faq\n## q\na\n```", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rendered, ok := garminLayoutReply(test.reply)
			if ok != test.want {
				t.Fatalf("ok=%v want %v", ok, test.want)
			}
			// The readable form keeps button URLs so the evidence checker can verify them.
			if ok && (!strings.Contains(rendered.Text, "https://github.com/MetrolistGroup/Metrolist/releases") || strings.Contains(rendered.Text, "components-v2")) {
				t.Fatalf("readable text = %q", rendered.Text)
			}
		})
	}
}
