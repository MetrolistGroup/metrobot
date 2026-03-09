package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/MetrolistGroup/metrobot/config"
	gh "github.com/MetrolistGroup/metrobot/github"
)

type ActionsHandler struct {
	Actions *gh.ActionsClient
	Config  *config.Config
}

func (h *ActionsHandler) GetActions(ctx context.Context, isTelegram bool) (string, error) {
	result, err := h.Actions.FetchRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching actions: %w", err)
	}

	if len(result.Runs) == 0 {
		return "No workflow runs found.", nil
	}

	var sb strings.Builder
	if isTelegram {
		sb.WriteString("<b>GitHub Actions - Latest Runs:</b>\n\n")
	} else {
		sb.WriteString("**GitHub Actions - Latest Runs:**\n\n")
	}

	for _, run := range result.Runs {
		title := formatActionRunTitle(run, isTelegram)

		switch {
		case run.Status == "in_progress" || run.Status == "queued":
			sb.WriteString(fmt.Sprintf("⏳ Run #%d - %s\n", run.RunNumber, title))
		case run.Conclusion == "failure" || run.Conclusion == "cancelled":
			sb.WriteString(fmt.Sprintf("❌ Run #%d - %s\n", run.RunNumber, title))
		case run.Conclusion == "success":
			fossURL := fmt.Sprintf("https://nightly.link/%s/%s/workflows/%s/main/%s.zip",
				h.Config.GitHubOwner, h.Config.GitHubRepo,
				h.Config.ActionsWorkflowFile, h.Config.ActionsArtifactNames.Foss)
			gmsURL := fmt.Sprintf("https://nightly.link/%s/%s/workflows/%s/main/%s.zip",
				h.Config.GitHubOwner, h.Config.GitHubRepo,
				h.Config.ActionsWorkflowFile, h.Config.ActionsArtifactNames.GMS)
			if isTelegram {
				sb.WriteString(fmt.Sprintf("✅ Run #%d - %s\n", run.RunNumber, title))
				sb.WriteString(fmt.Sprintf("• <a href=\"%s\">FOSS APK</a>\n", fossURL))
				sb.WriteString(fmt.Sprintf("• <a href=\"%s\">GMS APK</a>\n", gmsURL))
			} else {
				sb.WriteString(fmt.Sprintf("✅ Run #%d - %s\n", run.RunNumber, title))
				sb.WriteString(fmt.Sprintf("• FOSS APK: <%s>\n", fossURL))
				sb.WriteString(fmt.Sprintf("• GMS APK: <%s>\n", gmsURL))
			}
		}
	}

	return sb.String(), nil
}

func formatActionRunTitle(run gh.WorkflowRunInfo, isTelegram bool) string {
	title := run.Title
	if title == "" {
		title = "unknown commit"
	}
	if run.HeadSHA != "" {
		title = fmt.Sprintf("%s (%s)", title, run.HeadSHA)
	}
	if isTelegram {
		return escapeHTML(title)
	}
	return title
}
