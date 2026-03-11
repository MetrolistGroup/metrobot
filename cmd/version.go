package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	gh "github.com/MetrolistGroup/metrobot/github"
)

// titleCase returns s with first rune in title case and the rest lowercased (e.g. "WARNING" -> "Warning").
func titleCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(strings.ToLower(s))
	for i, r := range runes {
		if unicode.IsLetter(r) {
			runes[i] = unicode.ToTitle(r)
			return string(runes)
		}
	}
	return string(runes)
}

type VersionHandler struct {
	Releases *gh.ReleasesClient
}

var githubCalloutPattern = regexp.MustCompile(`^>\s*\[!([A-Z]+)\]\s*$`)

func (h *VersionHandler) GetVersion(ctx context.Context, tag string, isTelegram bool) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		tag = "latest"
	}

	rel, err := h.Releases.GetRelease(ctx, tag)
	if err != nil {
		return "", fmt.Errorf("fetching release: %w", err)
	}

	if rel == nil {
		return fmt.Sprintf("Version `%s` not found.", tag), nil
	}

	return formatRelease(rel, isTelegram), nil
}

func formatRelease(rel *gh.ReleaseInfo, isTelegram bool) string {
	var sb strings.Builder
	assets := selectReleaseAssets(rel.Assets)
	totalDownloads := 0
	for _, asset := range assets {
		totalDownloads += asset.DownloadCount
	}

	if isTelegram {
		sb.WriteString(fmt.Sprintf("<b>%s</b>\n", rel.TagName))
		sb.WriteString(fmt.Sprintf("📅 %s\n\n", rel.PublishedAt.Format(time.DateOnly)))

		if rel.Body != "" {
			sb.WriteString("<blockquote expandable>")
			sb.WriteString(escapeHTML(rel.Body))
			sb.WriteString("</blockquote>\n\n")
		}

		if len(assets) > 0 {
			sb.WriteString(fmt.Sprintf("<b>Downloads:</b> %d total\n", totalDownloads))
			for _, asset := range assets {
				sb.WriteString(fmt.Sprintf("• <a href=\"%s\">%s</a> - %s - %s\n",
					asset.DownloadURL,
					escapeHTML(asset.Name),
					formatBytes(asset.Size),
					formatDownloadCount(asset.DownloadCount),
				))
			}
		}
	} else {
		sb.WriteString(fmt.Sprintf("**%s**\n", rel.TagName))
		sb.WriteString(fmt.Sprintf("📅 %s\n\n", rel.PublishedAt.Format(time.DateOnly)))

		if rel.Body != "" {
			sb.WriteString(formatDiscordReleaseBody(rel.Body))
			sb.WriteString("\n\n")
		}

		if len(assets) > 0 {
			sb.WriteString(fmt.Sprintf("**Downloads:** %d total\n", totalDownloads))
			for _, asset := range assets {
				sb.WriteString(fmt.Sprintf("• %s - %s - %s - <%s>\n",
					asset.Name,
					formatBytes(asset.Size),
					formatDownloadCount(asset.DownloadCount),
					asset.DownloadURL,
				))
			}
		}
	}

	return sb.String()
}

func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

func selectReleaseAssets(assets []gh.ReleaseAsset) []gh.ReleaseAsset {
	if len(assets) <= 1 {
		return assets
	}

	preferred := []string{"Metrolist.apk", "Metrolist-with-Google-Cast.apk"}
	selected := make([]gh.ReleaseAsset, 0, len(preferred))
	seen := make(map[string]struct{}, len(preferred))

	for _, preferredName := range preferred {
		for _, asset := range assets {
			if asset.Name == preferredName {
				if _, ok := seen[asset.Name]; ok {
					continue
				}
				selected = append(selected, asset)
				seen[asset.Name] = struct{}{}
			}
		}
	}

	if len(selected) > 0 {
		return selected
	}

	return assets
}

func formatDownloadCount(count int) string {
	if count == 1 {
		return "1 download"
	}
	return fmt.Sprintf("%d downloads", count)
}

func formatBytes(size int) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := int64(size) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	value := float64(size) / float64(div)
	return fmt.Sprintf("%.1f %ciB", value, "KMGTPE"[exp])
}

func formatDiscordReleaseBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	formatted := make([]string, 0, len(lines))
	inCodeFence := false
	pendingCallout := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
			formatted = append(formatted, line)
			continue
		}

		if inCodeFence {
			formatted = append(formatted, line)
			continue
		}

		if matches := githubCalloutPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
			pendingCallout = titleCase(matches[1])
			continue
		}

		if pendingCallout != "" && strings.HasPrefix(trimmed, ">") {
			calloutText := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			formatted = append(formatted, fmt.Sprintf("**%s:** %s", pendingCallout, calloutText))
			pendingCallout = ""
			continue
		}

		pendingCallout = ""
		formatted = append(formatted, formatDiscordReleaseLine(line))
	}

	return strings.TrimSpace(strings.Join(formatted, "\n"))
}

func formatDiscordReleaseLine(line string) string {
	trimmed := strings.TrimSpace(line)

	switch {
	case strings.HasPrefix(trimmed, "### "):
		return "**" + strings.TrimSpace(trimmed[4:]) + "**"
	case strings.HasPrefix(trimmed, "## "):
		return "**" + strings.TrimSpace(trimmed[3:]) + "**"
	case strings.HasPrefix(trimmed, "# "):
		return "**" + strings.TrimSpace(trimmed[2:]) + "**"
	case strings.HasPrefix(trimmed, "- "):
		return "• " + strings.TrimSpace(trimmed[2:])
	case strings.HasPrefix(trimmed, "* "):
		return "• " + strings.TrimSpace(trimmed[2:])
	case strings.HasPrefix(trimmed, ">"):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
	default:
		return line
	}
}
