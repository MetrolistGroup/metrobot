package discord

import (
	"context"
	"fmt"
	"math"
	"regexp"
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
	winner                       int     // 0 no verdict, 1 A, 2 B, 3 identical
	lead                         float64 // winner's percentage lead, 0 when not meaningful
}

// The first standalone number in a value and the unit glued to it, e.g. "5060 mAh", "3 nanometers", "-25.5 LUFS".
var compareNumber = regexp.MustCompile(`(?:^|[\s(~:=/])(-?\d+(?:,\d{3})*(?:\.\d+)?)\s*([a-zA-Zµ%]*)`)

var (
	compareWords       = regexp.MustCompile(`[^a-z0-9]+`)
	compareLowerBetter = []string{"weight", "process", "lithography", "tdp", "power consumption", "place in the ranking", "sar"}
	// Numbers in these are dates, model names, versions of different products or matters of taste.
	compareNoVerdict = []string{"platform", "dimensions", "announced", "release date", "status", "model", "models", "model number", "colors", "bands", "technology", "name", "architecture", "graphics card", "popularity", "samples", "phones tested", "socket", "sim", "build", "bus", "protection", "price", "temperature", "number of cpus", "internal"}
)

func compareValue(value string) (float64, string, bool) {
	match := compareNumber.FindStringSubmatch(value)
	if match == nil {
		return 0, "", false
	}
	number, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	return number, strings.ToLower(match[2]), err == nil
}

// compareVerdict picks the better side of one spec row: 0 no verdict, 1 A, 2 B, 3 identical, plus the winner's lead in percent when meaningful.
// shortcut: units must match exactly (512 KB vs 1 MB gets no verdict); normalize sizes if that shows up often.
func compareVerdict(section, name, first, second string) (int, float64) {
	if first == "" || second == "" {
		return 0, 0
	}
	if strings.EqualFold(first, second) {
		return 3, 0
	}
	words := " " + strings.TrimSpace(compareWords.ReplaceAllString(strings.ToLower(section+" "+name), " ")) + " "
	has := func(phrases []string) bool {
		return slices.ContainsFunc(phrases, func(phrase string) bool { return strings.Contains(words, " "+phrase+" ") })
	}
	lower := has(compareLowerBetter)
	if strings.EqualFold(name, "size") || (!lower && has(compareNoVerdict)) {
		return 0, 0
	}
	a, unitA, okA := compareValue(first)
	b, unitB, okB := compareValue(second)
	if !okA || !okB || unitA != unitB || a == b {
		return 0, 0
	}
	winner := 2
	if (a > b) != lower {
		winner = 1
	}
	// Versions (USB 3.2, Wi-Fi 7) have no meaningful percentage.
	if a <= 0 || b <= 0 || (unitA == "" && math.Max(a, b) < 10) {
		return winner, 0
	}
	return winner, (math.Max(a, b)/math.Min(a, b) - 1) * 100
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
	for n := range rows {
		rows[n].winner, rows[n].lead = compareVerdict(rows[n].section, rows[n].name, rows[n].first, rows[n].second)
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
		// Overview: the widest gap in each section, so the page answers "which is better at what".
		best := make(map[string]int)
		for n, row := range rows {
			section := strings.ToLower(row.section)
			if row.winner == 1 || row.winner == 2 {
				if current, ok := best[section]; !ok || row.lead > rows[current].lead {
					best[section] = n
				}
			}
		}
		for n, row := range rows {
			if best[strings.ToLower(row.section)] == n && (row.winner == 1 || row.winner == 2) && len(shown) < 12 {
				shown = append(shown, row)
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
		if row.winner == 3 {
			lines = append(lines, fmt.Sprintf("**%s · %s**\nBoth: %s", row.section, row.name, row.first))
			continue
		}
		left, right := row.first, row.second
		if left == "" {
			left = "—"
		}
		if right == "" {
			right = "—"
		}
		lead := ""
		if row.lead >= 1 {
			lead = fmt.Sprintf(" (+%.0f%%)", row.lead)
		}
		switch row.winner {
		case 1:
			left = "**" + left + "**" + lead
		case 2:
			right = "**" + right + "**" + lead
		}
		lines = append(lines, fmt.Sprintf("**%s · %s**\nA: %s\nB: %s", row.section, row.name, left, right))
	}
	tally := [4]int{}
	for _, row := range rows {
		tally[row.winner]++
	}
	specs := func(n int) string {
		if n == 1 {
			return "**1** spec"
		}
		return fmt.Sprintf("**%d** specs", n)
	}
	body := "*No comparable specifications are available.*"
	if len(lines) > 0 {
		body = truncateRunes(strings.Join(lines, "\n\n"), 3200)
	}
	components := []discord.ContainerSubComponent{
		discord.TextDisplayComponent{Content: "# " + truncateRunes(first.Name+" vs "+second.Name, 170) + "\n-# Comparison from " + compareSource(source)},
		discord.TextDisplayComponent{Content: fmt.Sprintf("A: %s · better in %s\nB: %s · better in %s\n-# %d identical, %d without a clear winner", truncateRunes(first.Name, 150), specs(tally[1]), truncateRunes(second.Name, 150), specs(tally[2]), tally[3], tally[0])},
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
