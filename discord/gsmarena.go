package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
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

func (b *Bot) handleGSMArenaAutocomplete(s *bot.Client, i *events.AutocompleteInteractionCreate, options map[string]discord.AutocompleteOption) {
	query := ""
	for _, option := range options {
		if (option.Name == "search" || option.Name == "first" || option.Name == "second") && option.Focused {
			query = strings.TrimSpace(option.String())
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
	choices := make([]discord.AutocompleteChoice, 0, len(phones))
	for _, phone := range phones {
		name := strings.TrimSpace(phone.Brand + " " + phone.Name)
		choices = append(choices, discord.AutocompleteChoiceString{
			Name:  truncateRunes(name, 100),
			Value: strconv.FormatInt(phone.ID, 10),
		})
	}
	respondAutocomplete(s, i, choices)
}

func (b *Bot) handleGSMArena(s *bot.Client, i *events.ApplicationCommandInteractionCreate, options map[string]discord.SlashCommandOption) {
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
	if _, err := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{
		Components:      components,
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: &discord.AllowedMentions{},
	}); err != nil {
		b.Logger.Error("failed to send GSMArena response", zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't display those phone specs.")
		return
	}
}

func (b *Bot) handleGSMArenaComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	data := i.StringSelectMenuInteractionData()
	if !strings.HasPrefix(data.CustomID(), gsmarenaComponentPrefix) {
		return false
	}
	if len(data.Values) != 1 || b.gsmarena == nil {
		respondEphemeral(s, i, "Those phone specs are unavailable.")
		return true
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeDeferredUpdateMessage}); err != nil {
		b.Logger.Error("failed to defer GSMArena component", zap.Error(err))
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	phone, err := b.gsmarena.Phone(ctx, strings.TrimPrefix(data.CustomID(), gsmarenaComponentPrefix))
	if err != nil {
		b.Logger.Warn("GSMArena component lookup failed", zap.Error(err))
		_, _ = s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{Content: "Those phone specs are unavailable.", Flags: discord.MessageFlagEphemeral})
		return true
	}
	components := gsmarenaComponents(phone, data.Values[0])
	mentions := &discord.AllowedMentions{}
	if _, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Components: &components, AllowedMentions: mentions}); err != nil {
		b.Logger.Error("failed to update GSMArena component", zap.Error(err))
	}
	return true
}

func gsmarenaComponents(phone gsmarena.Phone, active string) []discord.LayoutComponent {
	page := gsmarenaPages[0]
	for _, candidate := range gsmarenaPages {
		if candidate.name == active {
			page = candidate
			break
		}
	}
	header := []discord.ContainerSubComponent{discord.TextDisplayComponent{Content: "# " + phone.Name + "\n-# Specifications from GSMArena"}}
	if phone.Image != "" {
		header = []discord.ContainerSubComponent{discord.SectionComponent{
			Components: []discord.SectionSubComponent{header[0].(discord.TextDisplayComponent)},
			Accessory:  discord.ThumbnailComponent{Media: discord.UnfurledMediaItem{URL: phone.Image}},
		}}
	}
	body := gsmarenaOverview(phone)
	if page.name != "Overview" {
		body = gsmarenaSpecPage(phone, page.sections)
	}
	container := discord.ContainerComponent{Components: append(header,
		discord.SeparatorComponent{},
		discord.TextDisplayComponent{Content: body},
		discord.SeparatorComponent{},
		gsmarenaPageSelect(phone.ID, page.name),
		discord.ActionRowComponent{Components: []discord.InteractiveComponent{discord.ButtonComponent{Label: "View on GSMArena", Style: discord.ButtonStyleLink, URL: phone.URL}}},
	)}
	return []discord.LayoutComponent{container}
}

func gsmarenaPageSelect(phoneID int64, active string) discord.ActionRowComponent {
	options := make([]discord.StringSelectMenuOption, len(gsmarenaPages))
	for index, page := range gsmarenaPages {
		options[index] = discord.StringSelectMenuOption{Label: page.name, Value: page.name, Default: page.name == active}
	}
	return discord.ActionRowComponent{Components: []discord.InteractiveComponent{discord.StringSelectMenuComponent{
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
