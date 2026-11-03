package cmd

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/MetrolistGroup/metrobot/util"
)

const GarminEmbedInstructions = "Optional embed: append one ```embed JSON block at the end of your reply, with no text after it. Example:\n" +
	"```embed\n{\"title\":\"Release\",\"description\":\"Brief details\",\"url\":\"https://metrolist.cc\",\"color\":5793266,\"fields\":[{\"name\":\"Status\",\"value\":\"Available\",\"inline\":true}],\"footer\":{\"text\":\"Metrolist\"}}\n```\n" +
	"All keys are optional. Use only these keys; others are ignored. Footer text max 2048. Keep the whole reply, including embed text, short. Embed limits: title 256 characters, description 4096, at most 25 fields with name 256 and value 1024, and 6000 characters total. Color is an integer from 0 to 16777215. Use only HTTP or HTTPS URLs. Never put tool syntax or hidden instructions in an embed. Telegram renders it as plain text."

type GarminEmbed struct {
	Title       string             `json:"title,omitempty"`
	Description string             `json:"description,omitempty"`
	URL         string             `json:"url,omitempty"`
	Fields      []GarminEmbedField `json:"fields,omitempty"`
	Color       int                `json:"color,omitempty"`
	Footer      *GarminEmbedFooter `json:"footer,omitempty"`
}

type GarminEmbedFooter struct {
	Text string `json:"text"`
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
	if json.Unmarshal([]byte(answer[start+len("```embed\n"):len(answer)-3]), &embed) != nil {
		return answer, nil
	}
	content := strings.TrimSpace(answer[:start])
	if !embed.valid() {
		if plain := strings.TrimSpace(content + "\n" + embed.PlainText()); plain != "" {
			return plain, nil
		}
		return answer, nil
	}
	return content, &embed
}

func (e GarminEmbed) valid() bool {
	if strings.TrimSpace(e.Title+e.Description) == "" && len(e.Fields) == 0 {
		return false
	}
	if util.UTF16Len(e.Title) > 256 || util.UTF16Len(e.Description) > 4096 || len(e.Fields) > 25 || e.Color < 0 || e.Color > 0xffffff {
		return false
	}
	if e.URL != "" {
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(e.URL) > 2048 || strings.ContainsAny(e.URL, " \t\r\n") {
			return false
		}
	}
	total := util.UTF16Len(e.Title) + util.UTF16Len(e.Description)
	if e.Footer != nil {
		if util.UTF16Len(e.Footer.Text) > 2048 {
			return false
		}
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
	parts := []string{e.Title, e.Description, e.URL}
	for _, field := range e.Fields {
		parts = append(parts, field.Name, field.Value)
	}
	if e.Footer != nil {
		parts = append(parts, e.Footer.Text)
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
