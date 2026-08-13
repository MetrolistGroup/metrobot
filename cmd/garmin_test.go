package cmd

import "testing"

func TestExtractGarminPrompt(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		want      string
		triggered bool
	}{
		{name: "lowercase", content: "garmin, what is an apk?", want: "what is an apk?", triggered: true},
		{name: "case insensitive", content: "  GARMIN, hello  ", want: "hello", triggered: true},
		{name: "empty prompt", content: "garmin,", want: "", triggered: true},
		{name: "comma required", content: "garmin hello", want: "", triggered: false},
		{name: "not at start", content: "hey garmin, hello", want: "", triggered: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, triggered := ExtractGarminPrompt(tt.content)
			if got != tt.want || triggered != tt.triggered {
				t.Fatalf("ExtractGarminPrompt(%q) = (%q, %v), want (%q, %v)", tt.content, got, triggered, tt.want, tt.triggered)
			}
		})
	}
}
