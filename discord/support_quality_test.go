package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestSupportNamedDefectCannotPassHighGrounding(t *testing.T) {
	for _, problem := range []string{"none", "needs_research", "off_request", "unsafe", "unsupported", "wrong_scope"} {
		t.Run(problem, func(t *testing.T) {
			b := &Bot{garminDecisions: supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				response := supportNoulDecision("grounded", .99)
				response.Answers["problem"] = cmd.DecisionAnswer{Type: "choice", Choice: problem}
				return response, nil
			})}
			messages := []cmd.GarminAIMessage{{Role: "user", Content: "The option to stream to another device via WiFi is still not working?"}}
			accepted, hint, err := b.supportAnswerGrounded(context.Background(), supportTestMessage(20), messages, messages, "I can't verify a fix. Do downloaded songs work?", nil)
			if err != nil || accepted != (problem == "none") || problem != "none" && hint == "" {
				t.Fatalf("defect=%s accepted=%v hint=%q err=%v", problem, accepted, hint, err)
			}
		})
	}
}

func TestPlaybackAndCastingGuidesAreDiscoverableAndScoped(t *testing.T) {
	for _, name := range []string{"playback", "casting"} {
		guide, err := loadGarminSupportGuide(name)
		if err != nil || !strings.Contains(guide, "standalone Android") {
			t.Fatalf("guide %s lost scope: %v", name, err)
		}
		var found bool
		for _, tool := range garminAITools {
			if tool.Function.Name == "load_support_guide" {
				found = strings.Contains(string(tool.Function.Parameters), `"`+name+`"`) && strings.Contains(tool.Function.Description, name)
			}
		}
		if !found {
			t.Fatalf("guide %s is not discoverable by the model", name)
		}
	}
}
