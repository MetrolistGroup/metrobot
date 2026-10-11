// Package notefmt parses portable, body-authored note directives, never Discord payloads.
package notefmt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/util"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type Button struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}
type Component struct {
	Type      string
	Content   string
	Thumbnail string
	URLs      []string
	Buttons   []Button
}
type Layout struct {
	Components []Component `json:"components"`
}
type Question struct{ Question, Answer string }
type FAQ struct {
	Header   string
	Items    []Question
	Markdown string
}
type Part struct {
	Text   string
	Random []string
	FAQ    *FAQ
	Layout *Layout
}
type Note struct{ Parts []Part }
type Rendered struct {
	Text, Before, After string
	FAQ                 *FAQ
	Layout              *Layout
}

type directive struct {
	start, end     int
	language, body string
}

// directives uses the Markdown tree so examples inside code, lists or quotes stay literal.
func directives(source string) []directive {
	data := []byte(source)
	root := goldmark.DefaultParser().Parse(text.NewReader(data))
	var result []directive
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		block, ok := node.(*ast.FencedCodeBlock)
		if !ok || block.Info == nil {
			continue
		}
		language := string(block.Info.Segment.Value(data))
		if language != "random" && language != "faq" && language != "components-v2" {
			continue
		}
		start := strings.LastIndex(source[:block.Info.Segment.Start], "\n") + 1
		opening := source[start:]
		lineEnd := strings.IndexByte(opening, '\n')
		if lineEnd < 0 {
			lineEnd = len(opening)
		}
		line := strings.TrimLeft(opening[:lineEnd], " ")
		size := len(line) - len(strings.TrimLeft(line, "`"))
		if size < 3 {
			continue
		} // Only backtick directives are part of the note grammar.
		bodyStart := min(start+lineEnd+1, len(source))
		end, bodyEnd := len(source), len(source)
		for pos := bodyStart; pos < len(source); {
			stop := strings.IndexByte(source[pos:], '\n')
			if stop < 0 {
				stop = len(source) - pos
			}
			closing := strings.TrimSuffix(source[pos:pos+stop], "\r")
			trimmed := strings.TrimLeft(closing, " ")
			ticks := len(trimmed) - len(strings.TrimLeft(trimmed, "`"))
			if len(closing)-len(trimmed) <= 3 && ticks >= size && strings.TrimSpace(trimmed[ticks:]) == "" {
				end, bodyEnd = pos+stop, pos
				break
			}
			pos += stop + 1
		}
		body := source[bodyStart:bodyEnd]
		if bodyEnd == len(source) {
			body = ""
		} // Unclosed directives are errors, unlike ordinary code.
		result = append(result, directive{start: start, end: end, language: language, body: strings.TrimSuffix(body, "\n")})
	}
	return result
}

func HasDirectives(source string) bool { return len(directives(source)) != 0 }

// Fence always adds its own boundary newline and cannot close on a fence in body.
func Fence(language, body string) string {
	size, run := 3, 0
	for _, r := range body {
		if r == '`' {
			run++
			size = max(size, run+1)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", size)
	return fence + language + "\n" + body + "\n" + fence
}

func Parse(source string) (*Note, error) {
	if !utf8.ValidString(source) {
		return nil, fmt.Errorf("note must be valid UTF-8")
	}
	note := &Note{}
	pos, rich := 0, false
	for _, d := range directives(source) {
		note.Parts = append(note.Parts, Part{Text: source[pos:d.start]})
		if strings.TrimSpace(d.body) == "" {
			return nil, fmt.Errorf("%s block must be nonempty and closed", d.language)
		}
		part := Part{}
		switch d.language {
		case "random":
			if err := decodeJSON([]byte(d.body), &part.Random); err != nil {
				return nil, fmt.Errorf("random block: %w", err)
			}
			if len(part.Random) == 0 {
				return nil, fmt.Errorf("random block needs a nonempty JSON array")
			}
			for _, entry := range part.Random {
				if strings.TrimSpace(entry) == "" {
					return nil, fmt.Errorf("random entries must be nonempty Markdown strings")
				}
			}
		case "faq":
			part.FAQ = parseFAQ(d.body)
		case "components-v2":
			part.Layout = &Layout{}
			if err := decodeJSON([]byte(d.body), part.Layout); err != nil {
				return nil, fmt.Errorf("components-v2 block: %w", err)
			}
			if err := part.Layout.validate(); err != nil {
				return nil, fmt.Errorf("components-v2 block: %w", err)
			}
		}
		if part.FAQ != nil || part.Layout != nil {
			if rich {
				return nil, fmt.Errorf("only one faq or components-v2 block is allowed per note")
			}
			rich = true
		}
		note.Parts = append(note.Parts, part)
		pos = d.end
	}
	note.Parts = append(note.Parts, Part{Text: source[pos:]})
	return note, nil
}

func (n *Note) Render() Rendered {
	var result Rendered
	var before, after, plain strings.Builder
	rich := false
	for _, part := range n.Parts {
		value := part.Text
		if part.Random != nil {
			entries := append([]string(nil), part.Random...)
			rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
			value = strings.Join(entries, "\n")
		}
		if part.FAQ != nil {
			result.FAQ = part.FAQ
			value = part.FAQ.Markdown
			rich = true
		} else if part.Layout != nil {
			result.Layout = part.Layout
			value = part.Layout.Markdown()
			rich = true
		} else if rich {
			after.WriteString(value)
		} else {
			before.WriteString(value)
		}
		plain.WriteString(value)
	}
	result.Text, result.Before, result.After = plain.String(), before.String(), after.String()
	return result
}

func parseFAQ(source string) *FAQ {
	faq := &FAQ{Markdown: source}
	data := []byte(source)
	root := goldmark.DefaultParser().Parse(text.NewReader(data))
	starts := []int{}
	questions := []string{}
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		h, ok := node.(*ast.Heading)
		if !ok || h.Level != 2 || h.Lines().Len() == 0 {
			continue
		}
		segment := h.Lines().At(0)
		start := strings.LastIndex(source[:segment.Start], "\n") + 1
		if !strings.HasPrefix(strings.TrimLeft(source[start:segment.Start], " "), "##") {
			continue // Setext headings are ordinary answer Markdown, not questions.
		}
		starts = append(starts, start)
		questions = append(questions, strings.TrimSpace(string(segment.Value(data))))
	}
	if len(starts) == 0 {
		faq.Header = strings.TrimSpace(source)
		return faq
	}
	faq.Header = strings.TrimSpace(source[:starts[0]])
	for index, start := range starts {
		end := len(source)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		_, answer, _ := strings.Cut(source[start:end], "\n")
		faq.Items = append(faq.Items, Question{questions[index], strings.TrimSpace(answer)})
	}
	return faq
}

// Validate checks authored layouts against Discord's limits; old FAQs may still be read as files.
func (n *Note) Validate() error {
	r := n.Render()
	surrounding := 0
	for _, text := range []string{r.Before, r.After} {
		if strings.TrimSpace(text) != "" {
			surrounding += util.UTF16Len(text)
		}
	}
	if r.Layout != nil {
		if surrounding+r.Layout.TextLength() > 4000 {
			return fmt.Errorf("components-v2 text including surrounding Markdown cannot exceed 4000 UTF-16 units")
		}
		count := r.Layout.ComponentCount() + 1
		children := len(r.Layout.Components)
		if strings.TrimSpace(r.Before) != "" {
			children++
			count++
		}
		if strings.TrimSpace(r.After) != "" {
			children++
			count++
		}
		if children > 10 || count > 40 {
			return fmt.Errorf("components-v2 exceeds 10 container children or 40 total components")
		}
	}
	if r.FAQ != nil {
		f := r.FAQ
		if len(f.Items) < 1 || len(f.Items) > 25 {
			return fmt.Errorf("faq needs 1 to 25 level-two questions")
		}
		header := f.DisplayHeader()
		for _, q := range f.Items {
			if q.Question == "" || q.Answer == "" {
				return fmt.Errorf("faq questions and answers must be nonempty")
			}
			if surrounding+util.UTF16Len(header+"## "+q.Question+"\n"+q.Answer) > 4000 {
				return fmt.Errorf("faq page including surrounding Markdown cannot exceed 4000 UTF-16 units")
			}
		}
	}
	return nil
}
func (f *FAQ) DisplayHeader() string {
	if f.Header == "" {
		return "# FAQ\nChoose a question below."
	}
	return f.Header + "\nChoose a question below."
}

func (c *Component) UnmarshalJSON(data []byte) error {
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return err
	}
	c.Type = kind.Type
	switch c.Type {
	case "text":
		var v struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := decodeJSON(data, &v); err != nil {
			return err
		}
		c.Content = v.Content
	case "separator":
		var v struct {
			Type string `json:"type"`
		}
		return decodeJSON(data, &v)
	case "section":
		var v struct {
			Type      string `json:"type"`
			Content   string `json:"content"`
			Thumbnail string `json:"thumbnail"`
		}
		if err := decodeJSON(data, &v); err != nil {
			return err
		}
		c.Content, c.Thumbnail = v.Content, v.Thumbnail
	case "gallery":
		var v struct {
			Type string   `json:"type"`
			URLs []string `json:"urls"`
		}
		if err := decodeJSON(data, &v); err != nil {
			return err
		}
		c.URLs = v.URLs
	case "buttons":
		var v struct {
			Type    string   `json:"type"`
			Buttons []Button `json:"buttons"`
		}
		if err := decodeJSON(data, &v); err != nil {
			return err
		}
		c.Buttons = v.Buttons
	default:
		return fmt.Errorf("unsupported component type %q", c.Type)
	}
	return nil
}
func httpsURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(value, "\r\n\t ") && util.UTF16Len(value) <= 512
}
func (l *Layout) validate() error {
	if len(l.Components) < 1 || len(l.Components) > 10 {
		return fmt.Errorf("layout needs 1 to 10 components")
	}
	for i, c := range l.Components {
		fail := func(message string) error { return fmt.Errorf("component %d: %s", i+1, message) }
		switch c.Type {
		case "text", "section":
			if strings.TrimSpace(c.Content) == "" {
				return fail("content must be nonempty")
			}
			if c.Type == "section" && !httpsURL(c.Thumbnail) {
				return fail("thumbnail must be an HTTPS URL (at most 512 UTF-16 units)")
			}
		case "gallery":
			if len(c.URLs) < 1 || len(c.URLs) > 10 {
				return fail("gallery needs 1 to 10 URLs")
			}
			for _, u := range c.URLs {
				if !httpsURL(u) {
					return fail("gallery URLs must be HTTPS (at most 512 UTF-16 units)")
				}
			}
		case "buttons":
			if len(c.Buttons) < 1 || len(c.Buttons) > 5 {
				return fail("buttons needs 1 to 5 URL buttons")
			}
			for _, b := range c.Buttons {
				if strings.TrimSpace(b.Label) == "" || util.UTF16Len(b.Label) > 80 {
					return fail("button labels need 1 to 80 UTF-16 units")
				}
				if !httpsURL(b.URL) {
					return fail("button URLs must be HTTPS (at most 512 UTF-16 units)")
				}
			}
		}
	}
	if l.TextLength() > 4000 || l.ComponentCount()+1 > 40 {
		return fmt.Errorf("layout exceeds 4000 UTF-16 text units or 40 total components")
	}
	return nil
}
func (l *Layout) TextLength() int {
	n := 0
	for _, c := range l.Components {
		n += util.UTF16Len(c.Content)
	}
	return n
}
func (l *Layout) ComponentCount() int {
	n := len(l.Components)
	for _, c := range l.Components {
		if c.Type == "section" {
			n += 2
		}
		if c.Type == "buttons" {
			n += len(c.Buttons)
		}
	}
	return n
}
func (l *Layout) Markdown() string {
	var entries []string
	for _, c := range l.Components {
		switch c.Type {
		case "text":
			entries = append(entries, c.Content)
		case "section":
			entries = append(entries, c.Content+"\n"+c.Thumbnail)
		case "separator":
			entries = append(entries, "---")
		case "gallery":
			entries = append(entries, strings.Join(c.URLs, "\n"))
		case "buttons":
			for _, b := range c.Buttons {
				entries = append(entries, b.Label+": "+b.URL)
			}
		}
	}
	return strings.Join(entries, "\n")
}

func decodeJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON must be valid UTF-8")
	}
	// encoding/json replaces unpaired surrogate escapes; reject them instead of losing text.
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		if i+1 < len(data) && data[i+1] == 'u' && i+6 <= len(data) {
			value, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
			if err == nil && value >= 0xd800 && value <= 0xdfff {
				if value > 0xdbff || i+12 > len(data) || string(data[i+6:i+8]) != `\u` {
					return fmt.Errorf("JSON contains an unpaired Unicode surrogate")
				}
				low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return fmt.Errorf("JSON contains an unpaired Unicode surrogate")
				}
				i += 6
			}
			i += 5
		} else {
			i++
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
