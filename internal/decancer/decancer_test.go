package decancer

import "testing"

func TestCure(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ascii capitalization", input: "Alice", want: "Alice"},
		{name: "stylized letters", input: "𝔄𝔩𝔦𝔠𝔢", want: "alice"},
		{name: "full width", input: "Ａｌｉｃｅ", want: "alice"},
		{name: "circled", input: "Ⓐⓛⓘⓒⓔ", want: "alice"},
		{name: "accented", input: "Áłïçé", want: "alice"},
		{name: "zalgo", input: "A̴͗̽l̷i̸c̵e̵", want: "Alice"},
		{name: "CJK lookalike", input: "乇xample", want: "example"},
		{name: "emoji", input: "🔥Alice🔥", want: "Alice"},
		{name: "Greek", input: "Γιάννης", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Cure(tt.input); got != tt.want {
				t.Fatalf("Cure(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
