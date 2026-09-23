package discord

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/MetrolistGroup/metrobot/nanoreview"
	"github.com/MetrolistGroup/metrobot/technicalcity"
	"github.com/bwmarrin/discordgo"
)

func TestHardwareCachedLookupAndNavigation(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "hardware.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	fixtures := []struct {
		source, searchKey, detailKey, searchJSON, deviceJSON, query, slug string
	}{
		{"nano", "nano:search:oneplus 15", "nano:detail:phone/oneplus-15", `[{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15"}]`, `{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15","specs":[{"name":"Display","items":[{"name":"Size","values":["6.78 inches"]}]},{"name":"Battery","items":[{"name":"Capacity","values":["7300 mAh"]}]}]}`, "OnePlus 15", "phone/oneplus-15"},
		{"tc", "tc:search:ryzen 9 9950x3d", "tc:device:cpu/Ryzen-9-9950X3D", `[{"name":"Ryzen 9 9950X3D","slug":"cpu/Ryzen-9-9950X3D","url":"https://technical.city/en/cpu/Ryzen-9-9950X3D"}]`, `{"name":"Ryzen 9 9950X3D","slug":"cpu/Ryzen-9-9950X3D","url":"https://technical.city/en/cpu/Ryzen-9-9950X3D","specs":[{"name":"Specifications","items":[{"name":"Cores","values":["16"]}]}]}`, "Ryzen 9 9950X3D", "cpu/Ryzen-9-9950X3D"},
	}
	bot := &Bot{nanoreview: nanoreview.New(database), technicalCity: technicalcity.New(database)}
	for _, fixture := range fixtures {
		for key, body := range map[string]string{fixture.searchKey: fixture.searchJSON, fixture.detailKey: fixture.deviceJSON} {
			if err := database.SetGSMArenaCache(key, []byte(body)); err != nil {
				t.Fatal(err)
			}
		}
		matches, err := bot.hardwareSearch(context.Background(), fixture.source, fixture.query)
		if err != nil || len(matches) != 1 || matches[0].Slug != fixture.slug {
			t.Fatalf("%s search: %v, %v", fixture.source, matches, err)
		}
		device, err := bot.hardwareLookup(context.Background(), fixture.source, matches[0].Slug, true)
		if err != nil || device.Name != matches[0].Name {
			t.Fatalf("%s detail: %v, %v", fixture.source, device, err)
		}
		components := hardwareComponents(fixture.source, device, "1")
		container := components[0].(discordgo.Container)
		foundCategory := false
		for _, part := range container.Components {
			if text, ok := part.(discordgo.TextDisplay); ok && strings.Contains(text.Content, device.Specs[0].Items[0].Values[0]) {
				foundCategory = true
			}
			if row, ok := part.(discordgo.ActionsRow); ok {
				if menu, ok := row.Components[0].(discordgo.SelectMenu); ok && (menu.CustomID != "hw:"+fixture.source+":"+fixture.slug || !menu.Options[1].Default) {
					t.Fatalf("%s navigation: %#v", fixture.source, menu)
				}
			}
		}
		if !foundCategory {
			t.Fatalf("%s category missing from %#v", fixture.source, components)
		}
	}
}

func TestRunGarminAIFetchesNanoReviewBeforeAnswering(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "garmin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for key, body := range map[string]string{
		"nano:search:oneplus 15":       `[{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15"}]`,
		"nano:detail:phone/oneplus-15": `{"name":"OnePlus 15","slug":"phone/oneplus-15","url":"https://nanoreview.net/en/phone/oneplus-15","specs":[{"name":"Battery","items":[{"name":"Capacity","values":["7300 mAh"]}]}]}`,
	} {
		if err := database.SetGSMArenaCache(key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{garminMemory: memory, nanoreview: nanoreview.New(database)}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !request.DisableReasoning || len(request.Tools) != 0 || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "7300 mAh") {
			t.Fatalf("did not receive cached source before synthesis: %#v", request)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "NanoReview lists a 7300 mAh battery."}}, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "1", GuildID: "guild", ChannelID: "channel", Content: "garmin, show NanoReview specs for OnePlus 15", Author: &discordgo.User{ID: "user"}}}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "show NanoReview specs for OnePlus 15"}})
	if err != nil || result.ToolCalls != 1 || !strings.Contains(result.Answer, "7300 mAh") {
		t.Fatalf("NanoReview run: %#v, %v", result, err)
	}
}

func TestHardwareNavigationOverDiscordCategoryLimit(t *testing.T) {
	device := hardwareDevice{Name: "Test", Slug: "cpu/test", URL: "https://technical.city/en/cpu/test"}
	for index := range 27 {
		device.Specs = append(device.Specs, gsmarena.SpecSection{Name: fmt.Sprint("Category ", index), Items: []gsmarena.SpecItem{{Name: "Value", Values: []string{fmt.Sprint("item ", index)}}}})
	}
	container := hardwareComponents("tc", device, "24")[0].(discordgo.Container)
	var menu discordgo.SelectMenu
	var content string
	for _, component := range container.Components {
		if text, ok := component.(discordgo.TextDisplay); ok {
			content += text.Content
		}
		if row, ok := component.(discordgo.ActionsRow); ok {
			if selectMenu, ok := row.Components[0].(discordgo.SelectMenu); ok {
				menu = selectMenu
			}
		}
	}
	if len(menu.Options) != 25 || !menu.Options[24].Default || !strings.Contains(content, "item 26") || strings.Contains(content, "item 22") {
		t.Fatalf("overflow navigation: %#v, %q", menu, content)
	}
}

func TestGarminHardwareSourceAndSubject(t *testing.T) {
	for _, tc := range []struct{ prompt, tool, query string }{
		{"show NanoReview specs for OnePlus 15", "get_nanoreview_device", "OnePlus 15"},
		{"search technical.city for Ryzen 9 9950X3D specs", "get_technicalcity_device", "Ryzen 9 9950X3D"},
		{"how much VRAM does RTX 5090 have?", "get_technicalcity_device", "RTX 5090"},
		{"show Snapdragon 8 Elite specs", "get_nanoreview_device", "Snapdragon 8 Elite"},
	} {
		messages := []cmd.GarminAIMessage{{Role: "user", Content: tc.prompt}}
		if got := garminHardwareToolRequested(messages); got != tc.tool {
			t.Fatalf("tool for %q = %q", tc.prompt, got)
		}
		if got := garminHardwareQuery(messages, tc.tool); got != tc.query {
			t.Fatalf("query for %q = %q, want %q", tc.prompt, got, tc.query)
		}
		if got := garminToolsForConversation(messages, false, false); !garminToolAvailable(got, tc.tool) || garminToolAvailable(got, "search_web") || garminToolAvailable(got, "get_gsmarena_phone") {
			t.Fatalf("wrong tools for %q: %#v", tc.prompt, got)
		}
		messages = append(messages, cmd.GarminAIMessage{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{Function: cmd.GarminAIFunctionCall{Name: tc.tool, Arguments: `{"query":"` + tc.query + `"}`}}}}, cmd.GarminAIMessage{Role: "tool"}, cmd.GarminAIMessage{Role: "user", Content: "what about its performance?"})
		if got := garminHardwareToolRequested(messages); got != tc.tool {
			t.Fatalf("follow-up tool = %q", got)
		}
		if got := garminHardwareQuery(messages, tc.tool); got != tc.query {
			t.Fatalf("follow-up subject = %q", got)
		}
	}
}
