package cmd

import (
	"context"
	"fmt"
	"html"
	"strings"

	gh "github.com/MetrolistGroup/metrobot/github"
)

// Keep the command name for existing Discord and Telegram registrations.
type ActionsHandler struct{ Releases *gh.ReleasesClient }

func (h *ActionsHandler) GetActions(ctx context.Context, isTelegram bool) (string, error) {
	release, err := h.Releases.GetNightly(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching nightly release: %w", err)
	}
	return formatNightlyRelease(release, isTelegram), nil
}

func formatNightlyRelease(release *gh.ReleaseInfo, isTelegram bool) string {
	if release == nil {
		return "No published nightly release found."
	}
	var sb strings.Builder
	if isTelegram {
		sb.WriteString("<b>Metrolist nightly</b>\nUnstable development build. Back up first; installs over stable in place.\n")
		fmt.Fprintf(&sb, "<a href=\"%s\">Nightly prerelease</a>\n", html.EscapeString(release.HTMLURL))
	} else {
		sb.WriteString("**Metrolist nightly**\nUnstable development build. Back up first; installs over stable in place.\n")
		fmt.Fprintf(&sb, "Nightly prerelease: <%s>\n", release.HTMLURL)
	}
	count := 0
	for _, asset := range release.Assets {
		platform := ""
		switch {
		case asset.Name == "Metrolist.apk":
			platform = "Android 7+"
		case asset.Name == "Metrolist-Desktop-windows.zip":
			platform = "Windows x64"
		case strings.HasPrefix(asset.Name, "metrolist-desktop-linux-x64-") && strings.HasSuffix(asset.Name, ".AppImage"):
			platform = "Linux x64"
		}
		if platform == "" || asset.DownloadURL == "" {
			continue
		}
		count++
		if isTelegram {
			fmt.Fprintf(&sb, "- <a href=\"%s\">%s: %s</a>\n", html.EscapeString(asset.DownloadURL), platform, html.EscapeString(asset.Name))
		} else {
			fmt.Fprintf(&sb, "- %s: %s <%s>\n", platform, asset.Name, asset.DownloadURL)
		}
	}
	if count == 0 {
		sb.WriteString("No supported nightly assets have been uploaded yet.\n")
	}
	return sb.String()
}
