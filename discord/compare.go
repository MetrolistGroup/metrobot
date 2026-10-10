package discord

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func compareOptions(description string) []discord.ApplicationCommandOption {
	return []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{Name: "first", Description: "First " + description, Required: true, Autocomplete: true, MaxLength: ptr(100)}, discord.ApplicationCommandOptionString{Name: "second", Description: "Second " + description, Required: true, Autocomplete: true, MaxLength: ptr(100)}}
}

func (b *Bot) compareLookup(ctx context.Context, source, query string) (hardwareDevice, error) {
	if source != "gsm" {
		selected := strings.HasPrefix(query, "phone/") || strings.HasPrefix(query, "cpu/") || strings.HasPrefix(query, "gpu/") || strings.HasPrefix(query, "soc/")
		return b.hardwareLookup(ctx, source, query, selected)
	}
	phone, err := b.gsmarena.Lookup(ctx, query)
	return hardwareDevice{Name: phone.Name, Slug: strconv.FormatInt(phone.ID, 10), URL: phone.URL, Specs: phone.Specs}, err
}

func compareSource(source string) string {
	if source == "gsm" {
		return "GSMArena"
	}
	return hardwareSource(source)
}

func (b *Bot) handleCompare(s *bot.Client, i *events.ApplicationCommandInteractionCreate, source string, options map[string]discord.SlashCommandOption) {
	if err := deferResponse(s, i, false); err != nil {
		b.Logger.Error("failed to defer comparison", zap.Error(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	first, err := b.compareLookup(ctx, source, getOptString(options, "first"))
	if err == nil {
		var second hardwareDevice
		second, err = b.compareLookup(ctx, source, getOptString(options, "second"))
		if err == nil {
			if first.Slug == second.Slug {
				_ = editDeferredResponse(s, i, "Choose two different devices to compare.")
				return
			}
			if source != "gsm" && strings.SplitN(first.Slug, "/", 2)[0] != strings.SplitN(second.Slug, "/", 2)[0] {
				_ = editDeferredResponse(s, i, "Choose two devices of the same type (phones, CPUs, GPUs or SoCs).")
				return
			}
			components := compareComponents(source, first, second, "0")
			if _, err := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{
				Components: components, Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: &discord.AllowedMentions{},
			}); err != nil {
				b.Logger.Error("failed to send comparison", zap.Error(err))
				_ = editDeferredResponse(s, i, "I couldn't display that comparison.")
			}
			return
		}
	}
	b.Logger.Warn("comparison lookup failed", zap.String("source", source), zap.Error(err))
	_ = editDeferredResponse(s, i, "I couldn't find both devices on "+compareSource(source)+".")
}

func (b *Bot) handleCompareComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	parts := strings.Split(i.Data.CustomID(), ":")
	if len(parts) != 4 || parts[0] != "cmp" || (parts[1] != "gsm" && parts[1] != "nano" && parts[1] != "tc") {
		return false
	}
	values := i.StringSelectMenuInteractionData().Values
	if len(values) != 1 {
		respondEphemeral(s, i, "That comparison is unavailable.")
		return true
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeDeferredUpdateMessage}); err != nil {
		b.Logger.Error("failed to defer comparison navigation", zap.Error(err))
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	first, err := b.compareLookup(ctx, parts[1], parts[2])
	if err == nil {
		var second hardwareDevice
		second, err = b.compareLookup(ctx, parts[1], parts[3])
		if err == nil {
			components := compareComponents(parts[1], first, second, values[0])
			if _, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
				b.Logger.Error("failed to update comparison", zap.Error(err))
			}
			return true
		}
	}
	b.Logger.Warn("comparison navigation failed", zap.Error(err))
	_, _ = s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{Content: "That comparison is unavailable.", Flags: discord.MessageFlagEphemeral})
	return true
}

type compareRow struct {
	section, name, first, second string
}

func compareRows(first, second hardwareDevice) []compareRow {
	var rows []compareRow
	indices := make(map[string]int)
	for side, device := range []hardwareDevice{first, second} {
		for _, section := range device.Specs {
			for _, item := range section.Items {
				key := strings.ToLower(section.Name + "\x00" + item.Name)
				index, ok := indices[key]
				if !ok {
					index = len(rows)
					indices[key] = index
					rows = append(rows, compareRow{section: section.Name, name: item.Name})
				}
				value := strings.Join(item.Values, " · ")
				if side == 0 {
					rows[index].first = value
				} else {
					rows[index].second = value
				}
			}
		}
	}
	return rows
}

func compareComponents(source string, first, second hardwareDevice, active string) []discord.LayoutComponent {
	rows := compareRows(first, second)
	pages := []string{"Overview"}
	for _, row := range rows {
		if row.section != "" && !slices.ContainsFunc(pages, func(page string) bool { return strings.EqualFold(page, row.section) }) {
			if len(pages) == 24 {
				pages = append(pages, "More")
				break
			}
			pages = append(pages, row.section)
		}
	}
	index, err := strconv.Atoi(active)
	if err != nil || index < 0 || index >= len(pages) {
		index = 0
	}
	var shown []compareRow
	if index == 0 {
		seen := make(map[string]bool)
		for _, row := range rows {
			section := strings.ToLower(row.section)
			if row.first != "" && row.second != "" && !seen[section] {
				shown = append(shown, row)
				seen[section] = true
			}
			if len(shown) == 12 {
				break
			}
		}
		if len(shown) == 0 {
			shown = rows[:min(12, len(rows))]
		}
	} else {
		for _, row := range rows {
			if strings.EqualFold(row.section, pages[index]) || (pages[index] == "More" && !slices.ContainsFunc(pages[:len(pages)-1], func(page string) bool { return strings.EqualFold(page, row.section) })) {
				shown = append(shown, row)
			}
		}
	}
	var lines []string
	for _, row := range shown {
		left, right := row.first, row.second
		if left == "" {
			left = "—"
		}
		if right == "" {
			right = "—"
		}
		lines = append(lines, fmt.Sprintf("**%s · %s**\nA: %s\nB: %s", row.section, row.name, left, right))
	}
	body := "*No comparable specifications are available.*"
	if len(lines) > 0 {
		body = truncateRunes(strings.Join(lines, "\n\n"), 3200)
	}
	components := []discord.ContainerSubComponent{
		discord.TextDisplayComponent{Content: "# " + truncateRunes(first.Name+" vs "+second.Name, 170) + "\n-# Comparison from " + compareSource(source)},
		discord.TextDisplayComponent{Content: "A: " + truncateRunes(first.Name, 150) + "\nB: " + truncateRunes(second.Name, 150)},
		discord.SeparatorComponent{}, discord.TextDisplayComponent{Content: body},
	}
	customID := "cmp:" + source + ":" + first.Slug + ":" + second.Slug
	// ponytail: long slug pairs cannot fit Discord's 100-character custom ID; add persisted comparison IDs if this becomes common.
	if len(pages) > 1 && len(customID) <= 100 {
		options := make([]discord.StringSelectMenuOption, len(pages))
		for n, page := range pages {
			options[n] = discord.StringSelectMenuOption{Label: truncateRunes(page, 100), Value: strconv.Itoa(n), Default: n == index}
		}
		components = append(components, discord.ActionRowComponent{Components: []discord.InteractiveComponent{discord.StringSelectMenuComponent{
			CustomID: customID, Placeholder: "Choose a specification category", MaxValues: 1, Options: options,
		}}})
	}
	components = append(components, discord.ActionRowComponent{Components: []discord.InteractiveComponent{
		discord.ButtonComponent{Label: "View A", Style: discord.ButtonStyleLink, URL: first.URL},
		discord.ButtonComponent{Label: "View B", Style: discord.ButtonStyleLink, URL: second.URL},
	}})
	return []discord.LayoutComponent{discord.ContainerComponent{Components: components}}
}
