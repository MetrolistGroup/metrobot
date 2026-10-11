package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	"github.com/MetrolistGroup/metrobot/util"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestAuthoredLayoutDeliveryPathsAndRawReads(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	raw := "Before\n" + notefmt.Fence("random", `["Fixed group"]`) + "\n" + notefmt.Fence("components-v2", `{"components":[{"type":"text","content":"Hello @everyone <@123> <@&456>"},{"type":"separator"},{"type":"section","content":"Details","thumbnail":"https://example.com/thumb.png"},{"type":"gallery","urls":["https://example.com/image.png"]},{"type":"buttons","buttons":[{"label":"Open","url":"https://example.com"}]}]}`) + "\nAfter"
	longName := strings.Repeat("界𝄞", 80)
	for _, name := range []string{longName, donorWelcomeNote} {
		if err := (&cmd.NotesHandler{DB: database}).AddNote(name, "Layout", raw); err != nil {
			t.Fatal(err)
		}
	}
	requests := make(chan deliveredNote, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- readDeliveredNote(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"38","channel_id":"5"}`))
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	b := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}, Config: &config.Config{DiscordGuildID: "12"}, Logger: zap.NewNop(), garminProcessor: cmd.NewGarminProcessor()}
	interaction := testCommandInteraction(t, client, `{"id":"17","application_id":"1","token":"token","type":2,"channel":{"id":"5","type":0},"data":{"name":"note","type":1}}`)
	option, _ := json.Marshal(longName)
	for _, path := range []string{"trigger", "private", "stay", "donor", "support"} {
		switch path {
		case "trigger":
			b.onMessageCreate(client, &discord.Message{ID: 23, ChannelID: 5, GuildID: ptr(snowflake.ID(12)), Content: "." + longName, Author: discord.User{ID: 56}})
		case "donor":
			b.sendDonorWelcome(client, "56")
		case "support":
			readable, err := b.Notes.GetNote(longName)
			if err != nil || strings.Contains(readable, "components-v2") || !strings.Contains(readable, "Open: https://example.com") {
				t.Fatalf("AI/Telegram fallback = %q, %v", readable, err)
			}
			if b.sendGarminAppSupportReply(client, &discord.Message{ID: 23, ChannelID: 5}, readable, longName) == nil {
				t.Fatal("support delivery failed")
			}
		default:
			b.handleNote(client, interaction, map[string]discord.SlashCommandOption{"name": {Value: option}}, "56", path == "stay")
		}
		got := <-requests
		if got.Message.Content != "" || got.Filename != "" || !got.Message.Flags.Has(discord.MessageFlagIsComponentsV2) || len(got.Message.Components) != 1 {
			t.Fatalf("%s: not V2: %#v", path, got)
		}
		container := got.Message.Components[0].(discord.ContainerComponent)
		if len(container.Components) != 7 || container.AccentColor != 0 {
			t.Fatalf("%s: not a plain complete container: %#v", path, container)
		}
		before := container.Components[0].(discord.TextDisplayComponent).Content
		if path == "donor" {
			if before != "<@56>\nBefore\nFixed group\n" || !slices.Equal(got.Message.AllowedMentions.Users, []snowflake.ID{56}) {
				t.Fatal("donor prefix/ping lost")
			}
		} else if before != "Before\nFixed group\n" || len(got.Message.AllowedMentions.Users) != 0 {
			t.Fatal("layout broadened mentions")
		}
		if len(got.Message.AllowedMentions.Parse) != 0 || container.Components[6].(discord.TextDisplayComponent).Content != "\nAfter" {
			t.Fatal("surroundings/mention policy changed")
		}
		section := container.Components[3].(discord.SectionComponent)
		if section.Accessory.(discord.ThumbnailComponent).Media.URL != "https://example.com/thumb.png" {
			t.Fatal("typed thumbnail missing")
		}
		button := container.Components[5].(discord.ActionRowComponent).Components[0].(discord.ButtonComponent)
		if button.Style != discord.ButtonStyleLink || button.CustomID != "" || button.URL != "https://example.com" {
			t.Fatal("URL-only button changed")
		}
		if got.Message.Flags.Has(discord.MessageFlagEphemeral) != (path == "private") {
			t.Fatal("visibility changed")
		}
		if (path == "trigger" || path == "support") && (got.Message.MessageReference == nil || value(got.Message.MessageReference.MessageID) != 23) {
			t.Fatal("reply reference lost")
		}
	}
	stored, err := database.GetNote(longName)
	if err != nil || stored != raw {
		t.Fatal("retrieval mutated raw editor body")
	}
}

func TestBoundedFAQSelectorForUnicodeName(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	name := strings.Repeat("𝄞界", 100)
	raw := "Intro\n" + notefmt.Fence("faq", "# Title\n## Q\nAnswer") + "\nFooter"
	h := &cmd.NotesHandler{DB: database}
	if err := h.AddNote(name, "FAQ", raw); err != nil {
		t.Fatal(err)
	}
	if err := h.RenameNote(name, name+"renamed"); err != nil {
		t.Fatal(err)
	}
	name += "renamed"
	doc, err := h.GetNoteDocument(name)
	if err != nil {
		t.Fatal(err)
	}
	components, ok := noteComponents(doc.Render(), name, "", 0)
	if !ok {
		t.Fatal("renamed FAQ lost behavior")
	}
	container := components[0].(discord.ContainerComponent)
	menu := container.Components[4].(discord.ActionRowComponent).Components[0].(discord.StringSelectMenuComponent)
	if menu.CustomID != noteSelectCustomID(name) || util.UTF16Len(menu.CustomID) > 100 || len(menu.Options) != 1 {
		t.Fatal("unsafe selector ID")
	}
	requests := make(chan deliveredNote, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- readDeliveredNote(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	var interaction discord.ComponentInteraction
	payload := `{"id":"17","application_id":"1","token":"token","type":3,"channel":{"id":"5","type":0},"message":{"id":"38","flags":32768},"data":{"custom_id":"` + menu.CustomID + `","component_type":3,"values":["0"]}}`
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		t.Fatal(err)
	}
	b := &Bot{Notes: h, Logger: zap.NewNop()}
	if !b.handleNoteComponent(client, &events.ComponentInteractionCreate{ComponentInteraction: interaction}) {
		t.Fatal("hashed selector not handled")
	}
	got := <-requests
	if got.Type != discord.InteractionResponseTypeUpdateMessage || len(got.Message.Components) != 1 {
		t.Fatal("selector failed")
	}
	container = got.Message.Components[0].(discord.ContainerComponent)
	if container.Components[0].(discord.TextDisplayComponent).Content != "Intro\n" || container.Components[5].(discord.TextDisplayComponent).Content != "\nFooter" {
		t.Fatal("selection dropped surrounding Markdown")
	}
	if err := h.EditNote(name, "Plain", "Behavior removed."); err != nil {
		t.Fatal(err)
	}
	if !b.handleNoteComponent(client, &events.ComponentInteractionCreate{ComponentInteraction: interaction}) {
		t.Fatal("old selector not acknowledged")
	}
	got = <-requests
	if got.Type != discord.InteractionResponseTypeCreateMessage || !got.Message.Flags.Has(discord.MessageFlagEphemeral) || len(got.Message.Components) != 0 {
		t.Fatal("removing directive did not disable behavior")
	}
}

func TestRichNoteRESTRejectionFallsBackToReadableAttachment(t *testing.T) {
	doc, err := notefmt.Parse(notefmt.Fence("faq", strings.Repeat("## Q\n"+strings.Repeat("Answer\n", 100), 8)))
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan deliveredNote, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := readDeliveredNote(t, r)
		requests <- got
		if len(got.Message.Components) != 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Invalid Form Body","code":50035}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"38","channel_id":"5"}`))
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	b := &Bot{Logger: zap.NewNop()}
	r := doc.Render()
	if b.sendNoteReply(client, "5", "23", "faq", r) == nil {
		t.Fatal("fallback failed")
	}
	<-requests
	<-requests
	got := <-requests
	if got.Filename != "note.md" || got.FileContent != r.Text || strings.Contains(got.FileContent, "```faq") {
		t.Fatal("fallback lost readable content")
	}
}
