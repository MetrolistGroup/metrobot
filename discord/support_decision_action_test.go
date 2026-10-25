package discord

import "testing"

func TestSupportDecisionActionDefersUncertainMatches(t *testing.T) {
	for _, tc := range []struct {
		name                string
		answer, top, margin float64
		route, note         string
		agent               bool
		want                string
	}{
		{"clear note", .99, .98, .96, "n0", "nightly", true, "note"},
		{"uncertain note", .95, .60, .20, "n0", "nightly", true, "agent"},
		{"close alternatives", .95, .85, .10, "n0", "nightly", true, "agent"},
		{"unmatched request", .90, .60, .20, "agent", "", true, "agent"},
		{"low request probability", .79, .99, .98, "n0", "nightly", true, "none"},
		{"explicit silence", .99, .99, .98, "none", "", true, "none"},
		{"no agent available", .99, .60, .20, "agent", "", false, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := supportDecisionAction(tc.answer, tc.top, tc.margin, tc.route, tc.note, tc.agent)
			if got != tc.want {
				t.Fatalf("action=%s want=%s", got, tc.want)
			}
		})
	}
}
