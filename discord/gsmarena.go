package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const gsmarenaComponentPrefix = "gsm:nav:"

var gsmarenaPages = []struct {
	name     string
	sections []string
}{
	{name: "Overview"},
	{name: "Network", sections: []string{"Network"}},
	{name: "Body", sections: []string{"Body"}},
	{name: "Display", sections: []string{"Display"}},
	{name: "Platform", sections: []string{"Platform"}},
	{name: "Memory", sections: []string{"Memory"}},
	{name: "Cameras", sections: []string{"Main Camera", "Selfie camera"}},
	{name: "Battery", sections: []string{"Battery"}},
	{name: "Other", sections: []string{"Launch", "Sound", "Comms", "Features", "Misc", "Tests", "Our Tests", "EU LABEL"}},
}

func (b *Bot) handleGSMArenaAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	query := ""
	for _, option := range options {
		if option.Name == "search" && option.Focused {
			query = strings.TrimSpace(option.StringValue())
			break
		}
	}
	if len([]rune(query)) < 2 || b.gsmarena == nil {
		respondAutocomplete(s, i, nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	phones, err := b.gsmarena.Search(ctx, query, 25)
	if err != nil {
		b.Logger.Debug("GSMArena autocomplete failed", zap.Error(err))
		respondAutocomplete(s, i, nil)
		return
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(phones))
	for _, phone := range phones {
		name := strings.TrimSpace(phone.Brand + " " + phone.Name)
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  truncateRunes(name, 100),
			Value: strconv.FormatInt(phone.ID, 10),
		})
	}
	respondAutocomplete(s, i, choices)
}

func (b *Bot) handleGSMArena(s *discordgo.Session, i *discordgo.InteractionCreate, options map[string]*discordgo.ApplicationCommandInteractionDataOption) {
	if b.gsmarena == nil {
		respondEphemeral(s, i, "GSMArena lookup is unavailable right now.")
		return
	}
	if err := deferResponse(s, i, false); err != nil {
		b.Logger.Error("failed to defer GSMArena response", zap.Error(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	phone, err := b.gsmarena.Lookup(ctx, getOptString(options, "search"))
	if err != nil {
		b.Logger.Warn("GSMArena lookup failed", zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't find that phone on GSMArena.")
		return
	}
	components := gsmarenaComponents(phone, "Overview")
	if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Components:      components,
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		b.Logger.Error("failed to send GSMArena response", zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't display those phone specs.")
		return
	}
}

func (b *Bot) handleGSMArenaComponent(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	data := i.MessageComponentData()
	if !strings.HasPrefix(data.CustomID, gsmarenaComponentPrefix) {
		return false
	}
	if len(data.Values) != 1 || b.gsmarena == nil {
		respondEphemeral(s, i, "Those phone specs are unavailable.")
		return true
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		b.Logger.Error("failed to defer GSMArena component", zap.Error(err))
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	phone, err := b.gsmarena.Phone(ctx, strings.TrimPrefix(data.CustomID, gsmarenaComponentPrefix))
	if err != nil {
		b.Logger.Warn("GSMArena component lookup failed", zap.Error(err))
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: "Those phone specs are unavailable.", Flags: discordgo.MessageFlagsEphemeral})
		return true
	}
	components := gsmarenaComponents(phone, data.Values[0])
	mentions := &discordgo.MessageAllowedMentions{}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Components: &components, AllowedMentions: mentions}); err != nil {
		b.Logger.Error("failed to update GSMArena component", zap.Error(err))
	}
	return true
}

func gsmarenaComponents(phone gsmarena.Phone, active string) []discordgo.MessageComponent {
	page := gsmarenaPages[0]
	for _, candidate := range gsmarenaPages {
		if candidate.name == active {
			page = candidate
			break
		}
	}
	header := []discordgo.MessageComponent{discordgo.TextDisplay{Content: "# " + phone.Name + "\n-# Specifications from GSMArena"}}
	if phone.Image != "" {
		header = []discordgo.MessageComponent{discordgo.Section{
			Components: header,
			Accessory:  discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: phone.Image}},
		}}
	}
	body := gsmarenaOverview(phone)
	if page.name != "Overview" {
		body = gsmarenaSpecPage(phone, page.sections)
	}
	container := discordgo.Container{Components: append(header,
		discordgo.Separator{},
		discordgo.TextDisplay{Content: body},
		discordgo.Separator{},
		gsmarenaPageSelect(phone.ID, page.name),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.Button{Label: "View on GSMArena", Style: discordgo.LinkButton, URL: phone.URL}}},
	)}
	return []discordgo.MessageComponent{container}
}

func gsmarenaPageSelect(phoneID int64, active string) discordgo.ActionsRow {
	options := make([]discordgo.SelectMenuOption, len(gsmarenaPages))
	for index, page := range gsmarenaPages {
		options[index] = discordgo.SelectMenuOption{Label: page.name, Value: page.name, Default: page.name == active}
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		CustomID:    gsmarenaComponentPrefix + strconv.FormatInt(phoneID, 10),
		Placeholder: "Choose a specification category",
		MaxValues:   1,
		Options:     options,
	}}}
}

func gsmarenaOverview(phone gsmarena.Phone) string {
	lines := make([]string, 0, 8)
	for _, field := range []struct {
		label   string
		section string
		items   []string
	}{
		{label: "Released", section: "Launch", items: []string{"Announced", "Status"}},
		{label: "Network", section: "Network", items: []string{"Technology"}},
		{label: "Body", section: "Body", items: []string{"Dimensions", "Weight"}},
		{label: "Display", section: "Display", items: []string{"Type", "Size", "Resolution"}},
		{label: "Platform", section: "Platform", items: []string{"OS", "Chipset"}},
		{label: "Memory", section: "Memory", items: []string{"Internal"}},
		{label: "Battery", section: "Battery", items: []string{"Type", "Charging"}},
	} {
		if values := gsmarenaValues(phone, field.section, field.items...); len(values) > 0 {
			lines = append(lines, fmt.Sprintf("**%s:** %s", field.label, strings.Join(values, " · ")))
		}
	}
	if len(lines) == 0 {
		return "*No overview is available for this phone.*"
	}
	return truncateRunes(strings.Join(lines, "\n"), 3900)
}

func gsmarenaSpecPage(phone gsmarena.Phone, names []string) string {
	var lines []string
	for _, wanted := range names {
		for _, section := range phone.Specs {
			if !strings.EqualFold(section.Name, wanted) {
				continue
			}
			if len(names) > 1 {
				lines = append(lines, "## "+section.Name)
			}
			for _, item := range section.Items {
				lines = append(lines, fmt.Sprintf("**%s:** %s", item.Name, strings.Join(item.Values, " · ")))
			}
		}
	}
	if len(lines) == 0 {
		return "*No data is available for this category.*"
	}
	return truncateRunes(strings.Join(lines, "\n"), 3900)
}

func gsmarenaValues(phone gsmarena.Phone, sectionName string, itemNames ...string) []string {
	for _, section := range phone.Specs {
		if !strings.EqualFold(section.Name, sectionName) {
			continue
		}
		var values []string
		for _, wanted := range itemNames {
			for _, item := range section.Items {
				if strings.EqualFold(item.Name, wanted) {
					values = append(values, item.Values...)
				}
			}
		}
		return values
	}
	return nil
}
