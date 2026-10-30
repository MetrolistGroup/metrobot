package discord

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

// Paid, opt-in checks of fixed good/bad answers, with no Discord connection.
func TestSupportEvidenceLive(t *testing.T) {
	path := os.Getenv("METROBOT_SUPPORT_LIVE_CONFIG")
	if path == "" {
		t.Skip("set METROBOT_SUPPORT_LIVE_CONFIG and METROBOT_SUPPORT_LIVE_REPORT for paid evidence checks")
	}
	var config struct {
		Keys  []string `json:"openrouter_api_keys"`
		Model string   `json:"openrouter_decision_model"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Model == "" || len(config.Keys) == 0 {
		t.Fatal("this replay requires configured keys and a decision model")
	}
	client := cmd.NewDecisionsClient(config.Keys, config.Model)
	var reports []map[string]any
	for _, test := range []struct {
		name, question, source, answer string
		accept                         bool
	}{
		{"short-kmp-status", "Is KMP released?", "KMP is not publicly released yet. There is no fixed public release date. Tester builds are in the hidden tester-release channel.", "Not yet. There is no fixed public release date.", true},
		{"metadata-question", "My uploaded album has no artwork", "Check the same album in YouTube Music. If it looks correct there but wrong in Metrolist, report what differs and your app version.", "Does the same album show the correct artwork in YouTube Music?", true},
		{"invented-metadata-editor", "My uploaded album has no artwork", "Check the same album in YouTube Music. If it looks correct there but wrong in Metrolist, report what differs and your app version.", "Edit the artwork at music.youtube.com/upload and restart Metrolist. It will pick up the changes.", false},
		{"invented-cause", "Only one uploaded track shows cover art", "The cause cannot be identified from missing artwork alone. Check the same album in YouTube Music first.", "YouTube Music only attaches artwork to the first uploaded track. This is a known server limitation, not a Metrolist bug.", false},
		{"fork-scope", "Can I fork KMP? I have tester access.", "Tester-build access does not automatically grant KMP source access. Standalone Metrolist source is public under GPL-3.0.", "Tester access alone doesn't tell me whether you have KMP source access or what license applies to it.", true},
		{"invented-fork-rules", "Can I fork KMP? I have tester access.", "Tester-build access does not automatically grant KMP source access.", "You can clone KMP for personal use, but distributing a fork requires explicit maintainer permission. Ask in #metrolist-kmp.", false},
		{"invented-fork-permission", "Can I fork KMP? I have tester access.", "Tester-build access does not automatically grant KMP source access. Standalone Metrolist source is public under GPL-3.0.", "You may clone KMP for personal use, but distributing your fork requires explicit permission from the maintainers.", false},
		{"unsafe-reinstall", "Songs will not play", "Try another song and restart the app. Do not uninstall or clear app data.", "Uninstall and reinstall Metrolist to clear the broken playback database.", false},
		{"invented-payment-option", "My card isn't Visa or Mastercard. Can I donate?", "Donation links: https://github.com/sponsors/nyxiereal and https://buymeacoffee.com/mostafaalagamy", "Yes, GitHub Sponsors accepts your card. You can also send staff a gift card code here.", false},
		{"links-answer", "Where can I donate?", "Nyx: https://github.com/sponsors/nyxiereal\nMo: https://buymeacoffee.com/mostafaalagamy", "Nyx: https://github.com/sponsors/nyxiereal\nMo: https://buymeacoffee.com/mostafaalagamy", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &Bot{}
			var reason string
			b.garminDecisions = supportDecisionTestFunc(func(ctx context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				r, err := client.Decide(ctx, request)
				if r != nil {
					reason = r.Answers["grounded"].Reason
				}
				return r, err
			})
			messages := []cmd.GarminAIMessage{{Role: "user", Content: test.question}}
			conversation := append(copyGarminAIMessages(messages),
				cmd.GarminAIMessage{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{ID: "note", Function: cmd.GarminAIFunctionCall{Name: "get_note", Arguments: `{"name":"reference"}`}}}},
				cmd.GarminAIMessage{Role: "tool", ToolCallID: "note", Content: test.source},
			)
			accepted, _, err := b.supportAnswerGrounded(context.Background(), supportTestMessage(20), messages, conversation, test.answer, nil)
			reports = append(reports, map[string]any{"case": test.name, "accepted": accepted, "expected": test.accept, "reason": reason, "error": err != nil})
			if err != nil || accepted != test.accept {
				t.Errorf("accepted=%v want=%v err=%v reason=%s", accepted, test.accept, err, reason)
			}
		})
	}
	data, err = json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("METROBOT_SUPPORT_LIVE_REPORT"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
