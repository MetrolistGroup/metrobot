package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestSupportSetupGuidesReachAgentAndEvidenceCheck(t *testing.T) {
	skill, err := loadGarminSkill("support")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"support_rpc", "support_login", "support_scrobbling"} {
		guide, err := garminSkillFiles.ReadFile("garmin_skills/" + name + ".md")
		if err != nil || !strings.Contains(skill, string(guide)) {
			t.Fatalf("missing full %s guide: %v", name, err)
		}
	}
	checked := false
	b := &Bot{
		garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			if !strings.Contains(request.SystemPrompt, skill) {
				t.Fatal("agent did not receive the verified guides")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Which Metrolist variant are you using?"}}, nil
		}),
		garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
			checked = true
			if request.State.(map[string]any)["support_skill"] != skill {
				t.Fatal("checker and agent received different setup evidence")
			}
			return supportTestDecision(request, "agent", .99, .99), nil
		}),
	}
	m := supportTestMessage(20)
	m.Content = "How do I connect Discord?"
	result, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if err != nil || !checked || result.Silent {
		t.Fatalf("guide-backed answer failed: %v, %+v", err, result)
	}
}
