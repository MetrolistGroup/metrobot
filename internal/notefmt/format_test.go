package notefmt

import (
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/util"
)

func mustParse(t *testing.T, source string) *Note {
	t.Helper()
	note, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	return note
}

func TestRandomGroupsPreserveEntriesAndSurroundings(t *testing.T) {
	source := "Fixed\n" + Fence("random", `["First\nparagraph", "Second", "Third"]`) + "\nBetween\n" + Fence("random", `["fourth", "fifth"]`) + "\nEnd"
	note := mustParse(t, source)
	seen := map[string]bool{}
	for range 60 {
		r := note.Render()
		if !strings.HasPrefix(r.Text, "Fixed\n") || !strings.HasSuffix(r.Text, "\nEnd") || !strings.Contains(r.Text, "\nBetween\n") {
			t.Fatal(r.Text)
		}
		for _, text := range []string{"First\nparagraph", "Second", "Third", "fourth", "fifth"} {
			if strings.Count(r.Text, text) != 1 {
				t.Fatalf("entry %q lost in %q", text, r.Text)
			}
		}
		seen[r.Text] = true
	}
	if len(seen) < 2 {
		t.Fatal("groups never shuffled")
	}
	literal := mustParse(t, Fence("random", `["`+"```faq\\n## Literal"+`"]`)).Render().Text
	if !strings.HasPrefix(literal, "```faq") {
		t.Fatal("random entry was recursively interpreted")
	}
}

func TestOnlyTopLevelBacktickDirectives(t *testing.T) {
	for _, source := range []string{
		"````markdown\n```random\nnot json\n```\n````",
		"    ```random\n    invalid\n    ```",
		"> ```random\n> invalid\n> ```",
		"- ```random\n  invalid\n  ```",
		"~~~random\ninvalid\n~~~",
	} {
		if got := mustParse(t, source).Render().Text; got != source {
			t.Fatalf("literal code changed: %q", got)
		}
	}
	for _, fence := range []string{"```", "````", "```````"} {
		source := "before\n" + fence + "random\n[\"one\"]\n" + fence + "\nafter"
		if got := mustParse(t, source).Render().Text; got != "before\none\nafter" {
			t.Fatal(got)
		}
	}
	if _, err := Parse("```random\n[\"one\"]"); err == nil {
		t.Fatal("unclosed directive accepted")
	}
}

func TestNormalizePreservesLiteralJSONEscapes(t *testing.T) {
	for _, separator := range []string{"\n", `\n`, `\r\n`} {
		source := "intro" + separator + "```random" + separator + `["line\nnext", "literal \\n", "quote \"ok\"", "music \uD834\uDD1E"]` + separator + "```" + separator + "end"
		normalized := Normalize(source)
		want := "intro\n```random\n" + `["line\nnext", "literal \\n", "quote \"ok\"", "music \uD834\uDD1E"]` + "\n```\nend"
		if normalized != want {
			t.Fatalf("Normalize = %q, want %q", normalized, want)
		}
		mustParse(t, normalized)
	}
	literal := "````markdown\n```random\n[\"literal \\n\"]\n```\n````"
	if got := mustParse(t, Normalize(literal)).Render().Text; got != literal {
		t.Fatalf("literal example changed: %q", got)
	}
	source := "```components-v2\\n" + `{"components":[{"type":"text","content":"line\nnext and literal \\n"}]}` + "\\n```"
	r := mustParse(t, Normalize(source)).Render()
	if r.Text != "line\nnext and literal \\n" {
		t.Fatalf("JSON escapes changed: %q", r.Text)
	}
}

func TestStrictDirectiveValidation(t *testing.T) {
	invalid := []struct{ language, body string }{
		{"random", `[]`}, {"random", `null`}, {"random", `[""]`}, {"random", `[" "]`}, {"random", `[3]`}, {"random", `[null]`}, {"random", `["x"] []`},
		{"random", `["\uD800"]`}, {"random", `["\uDC00"]`}, {"random", `["\uD800\u0041"]`},
		{"components-v2", `{}`}, {"components-v2", `{"components":[]}`}, {"components-v2", `{"components":[{"type":"embed"}]}`},
		{"components-v2", `{"components":[{"type":"text","content":"hi","url":"https://example.com"}]}`},
		{"components-v2", `{"components":[{"type":"separator","custom_id":"js"}]}`},
		{"components-v2", `{"components":[{"type":"text","content":""}]}`},
		{"components-v2", `{"components":[{"type":"section","content":"hi","thumbnail":"http://example.com"}]}`},
		{"components-v2", `{"components":[{"type":"gallery","urls":[]}]}`},
		{"components-v2", `{"components":[{"type":"gallery","urls":["javascript:alert(1)"]}]}`},
		{"components-v2", `{"components":[{"type":"buttons","buttons":[{"label":"go","url":"https://example.com","action":"evil"}]}]}`},
		{"components-v2", `{"components":[{"type":"buttons","buttons":[{"label":"go","url":"https://user:pass@example.com"}]}]}`},
	}
	for _, test := range invalid {
		t.Run(test.body, func(t *testing.T) {
			if _, err := Parse(Fence(test.language, test.body)); err == nil {
				t.Fatal("invalid block accepted")
			}
		})
	}
	for _, source := range []string{string([]byte{0xff}), Fence("random", "[\""+string([]byte{0xff})+"\"]"), Fence("faq", "## Q\nA") + "\n" + Fence("components-v2", `{"components":[{"type":"separator"}]}`)} {
		if _, err := Parse(source); err == nil {
			t.Fatal("invalid note accepted")
		}
	}
}

func TestReadableRichFallbackAndLimits(t *testing.T) {
	source := "Before\n" + Fence("components-v2", `{"components":[{"type":"text","content":"Help @everyone"},{"type":"separator"},{"type":"section","content":"App","thumbnail":"https://example.com/thumb.png"},{"type":"gallery","urls":["https://example.com/image.png"]},{"type":"buttons","buttons":[{"label":"Download","url":"https://example.com/app"}]}]}`) + "\nAfter"
	n := mustParse(t, source)
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	want := "Before\nHelp @everyone\n---\nApp\nhttps://example.com/thumb.png\nhttps://example.com/image.png\nDownload: https://example.com/app\nAfter"
	if got := n.Render().Text; got != want {
		t.Fatalf("fallback = %q", got)
	}
	for _, body := range []string{"## Q\nA", "# Title\nIntroduction\n## Q\nA\n```md\n## not a question\n```"} {
		faq := mustParse(t, Fence("faq", body)).Render().FAQ
		if len(faq.Items) != 1 || faq.Markdown != body {
			t.Fatalf("FAQ = %#v", faq)
		}
	}
	for _, body := range []string{"# empty", "## Q", strings.Repeat("## Q\nA\n", 26), "## Q\n" + strings.Repeat("𝄞", 2000)} {
		if err := mustParse(t, Fence("faq", body)).Validate(); err == nil {
			t.Fatal("invalid authored FAQ accepted")
		}
	}
	body := strings.Repeat("界", 4000-util.UTF16Len("# FAQ\nChoose a question below.## Q\n"))
	if err := mustParse(t, Fence("faq", "## Q\n"+body)).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := mustParse(t, "a\n"+Fence("faq", "## Q\n"+body)).Validate(); err == nil {
		t.Fatal("surrounding text omitted from limit")
	}
}

func TestLayoutBounds(t *testing.T) {
	row := `{"type":"buttons","buttons":[` + strings.TrimSuffix(strings.Repeat(`{"label":"Go","url":"https://example.com"},`, 5), ",") + `]},`
	tooMany := strings.TrimSuffix(strings.Repeat(row, 6)+strings.Repeat(`{"type":"text","content":"a"},`, 4), ",")
	for _, body := range []string{
		`{"components":[` + strings.TrimSuffix(strings.Repeat(`{"type":"separator"},`, 11), ",") + `]}`,
		`{"components":[{"type":"text","content":"` + strings.Repeat("𝄞", 2001) + `"}]}`,
		`{"components":[` + tooMany + `]}`,
		`{"components":[{"type":"gallery","urls":[` + strings.TrimSuffix(strings.Repeat(`"https://example.com",`, 11), ",") + `]}]}`,
		`{"components":[{"type":"buttons","buttons":[` + strings.TrimSuffix(strings.Repeat(`{"label":"Go","url":"https://example.com"},`, 6), ",") + `]}]}`,
	} {
		if _, err := Parse(Fence("components-v2", body)); err == nil {
			t.Fatal("bounds not checked")
		}
	}
	valid := Fence("components-v2", `{"components":[`+strings.TrimSuffix(strings.Repeat(`{"type":"separator"},`, 10), ",")+`]}`)
	if err := mustParse(t, "before\n"+valid).Validate(); err == nil {
		t.Fatal("surrounding children not counted")
	}
}
