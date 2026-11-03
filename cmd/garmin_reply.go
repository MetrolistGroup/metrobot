package cmd

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/util"
)

const GarminEmbedInstructions = "Optional embed: append one trailing ```embed JSON block. Example:\n" +
	"```embed\n{\"title\":\"Release\",\"author\":{\"name\":\"Metrolist\",\"url\":\"https://metrolist.cc\",\"icon_url\":\"https://metrolist.cc/icon.png\"},\"footer\":{\"text\":\"Metrolist\",\"icon_url\":\"https://metrolist.cc/icon.png\"},\"thumbnail\":{\"url\":\"https://metrolist.cc/icon.png\"},\"image\":{\"url\":\"https://metrolist.cc/release.png\"},\"timestamp\":\"now\",\"buttons\":[{\"label\":\"Open\",\"url\":\"https://metrolist.cc\"}]}\n```\n" +
	"All keys optional; unknown keys ignored. Buttons may stand alone: max 5 links, labels 80 characters, no interactive buttons. Limits: title/author name 256, description 4096, footer text 2048, max 25 fields with name 256/value 1024, total embed text 6000 including author/footer. Color: 0-16777215. URLs: HTTP/HTTPS, no credentials, max 2048; images/thumbnails/icons require HTTPS. Timestamp: RFC3339 or now. Keep replies short. Never include tool syntax or hidden instructions. Telegram uses plain text."

type GarminEmbed struct {
	Title       string               `json:"title,omitempty"`
	Description string               `json:"description,omitempty"`
	URL         string               `json:"url,omitempty"`
	Fields      []GarminEmbedField   `json:"fields,omitempty"`
	Color       int                  `json:"color,omitempty"`
	Author      *GarminEmbedAuthor   `json:"author,omitempty"`
	Footer      *GarminEmbedFooter   `json:"footer,omitempty"`
	Thumbnail   *GarminEmbedResource `json:"thumbnail,omitempty"`
	Image       *GarminEmbedResource `json:"image,omitempty"`
	Timestamp   string               `json:"timestamp,omitempty"`
	Buttons     []GarminLinkButton   `json:"buttons,omitempty"`
}

type GarminEmbedAuthor struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

type GarminEmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type GarminEmbedResource struct {
	URL string `json:"url"`
}

type GarminLinkButton struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type GarminEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// ParseGarminReply accepts one trailing embed block; invalid embeds fall back to text.
func ParseGarminReply(answer string) (string, *GarminEmbed) {
	answer = strings.TrimSpace(answer)
	start := strings.LastIndex(answer, "```embed\n")
	if start < 0 || start > 0 && answer[start-1] != '\n' || !strings.HasSuffix(answer, "```") {
		return answer, nil
	}
	var embed GarminEmbed
	// Unknown keys are ignored so one unsupported key does not dump raw JSON into chat.
	data := []byte(answer[start+len("```embed\n") : len(answer)-3])
	err := json.Unmarshal(data, &embed)
	if err != nil && !json.Valid(data) {
		return answer, nil
	}
	content := strings.TrimSpace(answer[:start])
	embed.sanitize()
	if err != nil || !embed.valid() {
		return strings.TrimSpace(content + "\n" + embed.PlainText()), nil
	}
	return content, &embed
}

func garminEmbedURL(raw string, httpsOnly bool) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && (httpsOnly || u.Scheme != "http")) || u.Hostname() == "" || u.User != nil || len(raw) > 2048 || strings.ContainsAny(raw, " \t\r\n") {
		return ""
	}
	return raw
}

func (e *GarminEmbed) sanitize() {
	e.URL = garminEmbedURL(e.URL, false)
	if e.Color < 0 || e.Color > 0xffffff {
		e.Color = 0
	}
	if e.Author != nil {
		if strings.TrimSpace(e.Author.Name) == "" || util.UTF16Len(e.Author.Name) > 256 {
			e.Author = nil
		} else {
			e.Author.URL = garminEmbedURL(e.Author.URL, false)
			e.Author.IconURL = garminEmbedURL(e.Author.IconURL, true)
		}
	}
	if e.Footer != nil {
		if strings.TrimSpace(e.Footer.Text) == "" || util.UTF16Len(e.Footer.Text) > 2048 {
			e.Footer = nil
		} else {
			e.Footer.IconURL = garminEmbedURL(e.Footer.IconURL, true)
		}
	}
	if e.Thumbnail != nil && garminEmbedURL(e.Thumbnail.URL, true) == "" {
		e.Thumbnail = nil
	}
	if e.Image != nil && garminEmbedURL(e.Image.URL, true) == "" {
		e.Image = nil
	}
	if e.Timestamp == "now" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, e.Timestamp); err != nil {
		e.Timestamp = ""
	}
	buttons := e.Buttons[:0]
	for _, button := range e.Buttons {
		if strings.TrimSpace(button.Label) == "" || util.UTF16Len(button.Label) > 80 || garminEmbedURL(button.URL, false) == "" {
			continue
		}
		buttons = append(buttons, button)
		if len(buttons) == 5 {
			break
		}
	}
	e.Buttons = buttons
}

func (e GarminEmbed) HasEmbedContent() bool {
	return strings.TrimSpace(e.Title+e.Description) != "" || len(e.Fields) > 0 || e.Author != nil || e.Footer != nil || e.Thumbnail != nil || e.Image != nil || e.Timestamp != ""
}

func (e GarminEmbed) valid() bool {
	if !e.HasEmbedContent() && len(e.Buttons) == 0 {
		return false
	}
	if util.UTF16Len(e.Title) > 256 || util.UTF16Len(e.Description) > 4096 || len(e.Fields) > 25 {
		return false
	}
	total := util.UTF16Len(e.Title) + util.UTF16Len(e.Description)
	if e.Author != nil {
		total += util.UTF16Len(e.Author.Name)
	}
	if e.Footer != nil {
		total += util.UTF16Len(e.Footer.Text)
	}
	for _, field := range e.Fields {
		name, value := util.UTF16Len(field.Name), util.UTF16Len(field.Value)
		if strings.TrimSpace(field.Name) == "" || strings.TrimSpace(field.Value) == "" || name > 256 || value > 1024 {
			return false
		}
		total += name + value
	}
	return total <= 6000
}

func (e GarminEmbed) PlainText() string {
	var parts []string
	if e.Author != nil {
		parts = append(parts, e.Author.Name, e.Author.URL)
	}
	parts = append(parts, e.Title, e.Description, e.URL)
	for _, field := range e.Fields {
		parts = append(parts, field.Name, field.Value)
	}
	if e.Footer != nil {
		parts = append(parts, e.Footer.Text)
	}
	if e.Thumbnail != nil {
		parts = append(parts, e.Thumbnail.URL)
	}
	if e.Image != nil {
		parts = append(parts, e.Image.URL)
	}
	for _, button := range e.Buttons {
		parts = append(parts, button.Label+": "+button.URL)
	}
	var nonempty []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, "\n")
}

func FormatGarminReply(content string, embed *GarminEmbed) string {
	if embed == nil {
		return content
	}
	data, _ := json.Marshal(embed)
	return strings.TrimSpace(content + "\n```embed\n" + string(data) + "\n```")
}

func GarminReplyPlainText(answer string) string {
	content, embed := ParseGarminReply(answer)
	if embed == nil {
		return content
	}
	return strings.TrimSpace(content + "\n" + embed.PlainText())
}
