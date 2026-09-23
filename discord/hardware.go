package discord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/MetrolistGroup/metrobot/nanoreview"
	"github.com/MetrolistGroup/metrobot/technicalcity"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// Both sources use category-qualified slugs so a selected result can be
// fetched again from the cache when someone changes the displayed category.
type hardwareDevice struct {
	Name, Slug, URL, Image, Summary string
	Specs                           []gsmarena.SpecSection
}

func (b *Bot) hardwareSearch(ctx context.Context, source, query string) ([]hardwareDevice, error) {
	var devices []hardwareDevice
	switch source {
	case "nano":
		matches, err := b.nanoreview.Search(ctx, query, 25)
		if err != nil {
			return nil, err
		}
		for _, d := range matches {
			devices = append(devices, hardwareDevice{Name: d.Name, Slug: d.Slug, URL: d.URL, Image: d.Image, Summary: d.Summary, Specs: d.Specs})
		}
	case "tc":
		matches, err := b.technicalCity.Search(ctx, query, 25)
		if err != nil {
			return nil, err
		}
		for _, d := range matches {
			devices = append(devices, hardwareDevice{Name: d.Name, Slug: d.Slug, URL: d.URL, Image: d.Image, Summary: d.Summary, Specs: d.Specs})
		}
	}
	return devices, nil
}

func (b *Bot) hardwareLookup(ctx context.Context, source, query string, selected bool) (hardwareDevice, error) {
	switch source {
	case "nano":
		var d nanoreview.Device
		var err error
		if selected {
			d, err = b.nanoreview.Device(ctx, query)
		} else {
			d, err = b.nanoreview.Lookup(ctx, query)
		}
		return hardwareDevice{d.Name, d.Slug, d.URL, d.Image, d.Summary, d.Specs}, err
	case "tc":
		var d technicalcity.Device
		var err error
		if selected {
			d, err = b.technicalCity.Device(ctx, query)
		} else {
			d, err = b.technicalCity.Lookup(ctx, query)
		}
		return hardwareDevice{d.Name, d.Slug, d.URL, d.Image, d.Summary, d.Specs}, err
	}
	return hardwareDevice{}, fmt.Errorf("unknown hardware source %q", source)
}

func hardwareSource(source string) string {
	if source == "nano" {
		return "NanoReview"
	}
	return "Technical City"
}

func (b *Bot) handleHardwareAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, source string, options []*discordgo.ApplicationCommandInteractionDataOption) {
	for _, option := range options {
		if option.Name != "search" || !option.Focused || len([]rune(strings.TrimSpace(option.StringValue()))) < 2 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		devices, err := b.hardwareSearch(ctx, source, option.StringValue())
		if err != nil {
			b.Logger.Debug("hardware autocomplete failed", zap.String("source", source), zap.Error(err))
			break
		}
		choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(devices))
		for _, d := range devices {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: truncateRunes(d.Name, 100), Value: d.Slug})
		}
		respondAutocomplete(s, i, choices)
		return
	}
	respondAutocomplete(s, i, nil)
}

func (b *Bot) handleHardware(s *discordgo.Session, i *discordgo.InteractionCreate, source string, options map[string]*discordgo.ApplicationCommandInteractionDataOption) {
	if err := deferResponse(s, i, false); err != nil {
		b.Logger.Error("failed to defer hardware response", zap.Error(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	query := getOptString(options, "search")
	selected := strings.HasPrefix(query, "cpu/") || strings.HasPrefix(query, "gpu/") || strings.HasPrefix(query, "phone/") || strings.HasPrefix(query, "soc/") || strings.HasPrefix(query, "laptop/")
	device, err := b.hardwareLookup(ctx, source, query, selected)
	if err != nil {
		b.Logger.Warn("hardware lookup failed", zap.String("source", source), zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't find that device on "+hardwareSource(source)+".")
		return
	}
	if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Components: hardwareComponents(source, device, "Overview"), Flags: discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		b.Logger.Error("failed to send hardware response", zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't display those specifications.")
	}
}

func (b *Bot) handleHardwareComponent(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, ":", 3)
	if len(parts) != 3 || parts[0] != "hw" || (parts[1] != "nano" && parts[1] != "tc") {
		return false
	}
	if len(data.Values) != 1 {
		respondEphemeral(s, i, "Those specifications are unavailable.")
		return true
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		b.Logger.Error("failed to defer hardware component", zap.Error(err))
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	device, err := b.hardwareLookup(ctx, parts[1], parts[2], true)
	if err != nil {
		b.Logger.Warn("hardware component lookup failed", zap.Error(err))
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: "Those specifications are unavailable.", Flags: discordgo.MessageFlagsEphemeral})
		return true
	}
	components := hardwareComponents(parts[1], device, data.Values[0])
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Components: &components, AllowedMentions: &discordgo.MessageAllowedMentions{}}); err != nil {
		b.Logger.Error("failed to update hardware component", zap.Error(err))
	}
	return true
}

func hardwareComponents(source string, device hardwareDevice, active string) []discordgo.MessageComponent {
	pages := []string{"Overview"}
	for _, section := range device.Specs {
		if section.Name != "" && len(section.Items) > 0 && !containsHardwarePage(pages, section.Name) {
			if len(pages) == 24 {
				pages = append(pages, "More")
				break
			}
			pages = append(pages, section.Name)
		}
	}
	activeIndex := 0
	for index := range pages {
		if active == fmt.Sprint(index) {
			activeIndex = index
		}
	}
	body := hardwareOverview(device)
	if activeIndex != 0 {
		var lines []string
		more := pages[activeIndex] == "More"
		for _, section := range device.Specs {
			if section.Name != pages[activeIndex] && !(more && !containsHardwarePage(pages[:len(pages)-1], section.Name)) {
				continue
			}
			if more {
				lines = append(lines, "## "+section.Name)
			}
			for _, item := range section.Items {
				lines = append(lines, fmt.Sprintf("**%s:** %s", item.Name, strings.Join(item.Values, " · ")))
			}
		}
		body = "*No data is available for this category.*"
		if len(lines) > 0 {
			body = truncateRunes(strings.Join(lines, "\n"), 3900)
		}
	}
	header := []discordgo.MessageComponent{discordgo.TextDisplay{Content: "# " + truncateRunes(device.Name, 180) + "\n-# Specifications from " + hardwareSource(source)}}
	if device.Image != "" {
		header = []discordgo.MessageComponent{discordgo.Section{Components: header, Accessory: discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: device.Image}}}}
	}
	components := append(header, discordgo.Separator{}, discordgo.TextDisplay{Content: body})
	if len(pages) > 1 && len("hw:"+source+":"+device.Slug) <= 100 {
		options := make([]discordgo.SelectMenuOption, 0, len(pages))
		for index, page := range pages {
			options = append(options, discordgo.SelectMenuOption{Label: truncateRunes(page, 100), Value: fmt.Sprint(index), Default: index == activeIndex})
		}
		components = append(components, discordgo.Separator{}, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			CustomID: "hw:" + source + ":" + device.Slug, Placeholder: "Choose a specification category", MaxValues: 1, Options: options,
		}}})
	}
	components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.Button{Label: "View on " + hardwareSource(source), Style: discordgo.LinkButton, URL: device.URL}}})
	return []discordgo.MessageComponent{discordgo.Container{Components: components}}
}

func containsHardwarePage(pages []string, page string) bool {
	for _, candidate := range pages {
		if candidate == page {
			return true
		}
	}
	return false
}

func hardwareOverview(device hardwareDevice) string {
	var lines []string
	if device.Summary != "" {
		lines = append(lines, truncateRunes(device.Summary, 700))
	}
	for _, section := range device.Specs {
		if len(section.Items) == 0 {
			continue
		}
		item := section.Items[0]
		lines = append(lines, fmt.Sprintf("**%s · %s:** %s", section.Name, item.Name, strings.Join(item.Values, " · ")))
		if len(lines) >= 9 {
			break
		}
	}
	if len(lines) == 0 {
		return "*No overview is available for this device.*"
	}
	return truncateRunes(strings.Join(lines, "\n"), 3900)
}
