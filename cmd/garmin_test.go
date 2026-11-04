package cmd

import "testing"

func TestProcessTriggerSupportsModeratorCommands(t *testing.T) {
	processor := NewGarminProcessor()
	for _, command := range []string{"mute", "timeout", "kick", "sban"} {
		input := "ok garmin " + command + " @someone 1m reason"
		if got := processor.ProcessTrigger(input); got != "!"+command+" @someone 1m reason" {
			t.Errorf("ProcessTrigger(%q) = %q", input, got)
		}
	}
	input := "france modération mute @someone 1m reason"
	if got := processor.ProcessTrigger(input); got != "!mute @someone 1m reason" {
		t.Errorf("ProcessTrigger(%q) = %q", input, got)
	}
}

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
		{name: "garmin without comma", content: "garmin hello", want: "hello", triggered: true},
		{name: "arbitrary prompt", content: "garmin summarize the last ten messages", want: "summarize the last ten messages", triggered: true},
		{name: "single word prompt", content: "GARMIN ping", want: "ping", triggered: true},
		{name: "punctuation prompt", content: "garmin 2 + 2?", want: "2 + 2?", triggered: true},
		{name: "whitespace separator", content: "  Garmin\t\nwrite a haiku  ", want: "write a haiku", triggered: true},
		{name: "spaced comma", content: "garmin  , summarize this", want: "summarize this", triggered: true},
		{name: "not at start", content: "i told garmin to summarize", triggered: false},
		{name: "moderation trigger", content: "ok garmin mute @someone", triggered: false},
		{name: "third person alias", content: "metrobot is alive", triggered: false},
		{name: "metrobot", content: "Metrobot, hello", want: "hello", triggered: true},
		{name: "metro", content: "metro, hello", want: "hello", triggered: true},
		{name: "partial name", content: "garminade hello", want: "", triggered: false},
		{name: "greeting address", content: "hey garmin, hello", want: "hello", triggered: true},
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
