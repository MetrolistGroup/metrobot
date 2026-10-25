package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestDiscordNoteMessageBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, content, prefix string
		file                  bool
	}{
		{name: "ASCII boundary", content: strings.Repeat("a", 1999) + "\n"},
		{name: "newline overflow", content: strings.Repeat("a", 2000) + "\n", file: true},
		{name: "actions failure streak", content: "**Latest GitHub Actions runs:**\n\n" + strings.Repeat("Run #1000 failed or was cancelled. "+strings.Repeat("a", 60)+" (1234567)\n", 20), file: true},
		{name: "wrapped URL overflow", content: suppressDiscordEmbeds(strings.Repeat("https://example.com ", 100)), file: true},
		{name: "BMP boundary", content: strings.Repeat("界", 2000)},
		{name: "supplementary boundary", content: strings.Repeat("𝄞", 1000)},
		{name: "Unicode overflow", content: strings.Repeat("𝄞", 1000) + "\n", file: true},
		{name: "raw file", content: strings.Repeat("**界𝄞**\r\n<https://example.com>\\n\n", 100), file: true},
		{name: "donor boundary", prefix: "<@123>\n", content: strings.Repeat("a", 1993)},
		{name: "donor prefix overflow", prefix: "<@123>\n", content: strings.Repeat("a", 1994), file: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := newDiscordNoteMessage(test.content, test.prefix)
			if !test.file {
				if len(message.Files) != 0 || message.Content != test.prefix+test.content {
					t.Fatal("short note rendering changed")
				}
				return
			}
			if message.Content != test.prefix || len(message.Files) != 1 || message.Files[0].Name != "note.md" {
				t.Fatalf("attachment message = %#v", message)
			}
			content, err := io.ReadAll(message.Files[0].Reader)
			if err != nil || string(content) != test.content {
				t.Fatalf("attachment did not preserve full raw content: %v", err)
			}
		})
	}
}

func testFAQComponents(content string, selected int) ([]discord.LayoutComponent, bool) {
	note, err := notefmt.Parse(notefmt.Fence("faq", content))
	if err != nil {
		return nil, false
	}
	return noteComponents(note.Render(), "any-name", "", selected)
}

func TestFAQNoteComponentTextLimits(t *testing.T) {
	if _, ok := testFAQComponents(strings.Repeat("## Question\n"+strings.Repeat("answer\n", 100), 10), -1); !ok {
		t.Fatal("long FAQ with individually valid answers lost pagination")
	}
	answer := strings.Repeat("界", 4000-util.UTF16Len("# FAQ\nChoose a question below.## Q\n"))
	for _, selected := range []int{-1, 0} {
		if _, ok := testFAQComponents("## Q\n"+answer, selected); !ok {
			t.Fatal("FAQ at the total text limit was rejected")
		}
		if _, ok := testFAQComponents("## Q\n"+answer+"𝄞", selected); ok {
			t.Fatal("FAQ exceeding the UTF-16 text limit was accepted")
		}
	}
	if _, ok := testFAQComponents("## Small\nshort\n## Q\n"+answer+"a", 0); ok {
		t.Fatal("menu hid an oversized unselected answer")
	}
	content := "## " + strings.Repeat("𝄞", 60) + "\n" + strings.Repeat("界𝄞", 100)
	components, ok := testFAQComponents(content, 0)
	if !ok {
		t.Fatal("valid Unicode FAQ was rejected")
	}
	container := components[0].(discord.ContainerComponent)
	menu := container.Components[3].(discord.ActionRowComponent).Components[0].(discord.StringSelectMenuComponent)
	for _, text := range []string{menu.Options[0].Label, menu.Options[0].Description} {
		if !utf8.ValidString(text) || util.UTF16Len(text) > 100 || !strings.HasSuffix(text, "…") {
			t.Fatalf("invalid Unicode option: %q", text)
		}
	}
}

type deliveredNote struct {
	Type                        discord.InteractionResponseType `json:"type"`
	Message                     discord.MessageCreate           `json:"data"`
	Path, Filename, FileContent string
}

func readDeliveredNote(t *testing.T, r *http.Request) deliveredNote {
	t.Helper()
	var result deliveredNote
	result.Path = r.URL.Path
	var payload []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(256 * 1024); err != nil {
			t.Error(err)
			return result
		}
		defer r.MultipartForm.RemoveAll()
		payload = []byte(r.FormValue("payload_json"))
		file, header, err := r.FormFile("files[0]")
		if err != nil {
			t.Error(err)
			return result
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
		}
		result.Filename, result.FileContent = header.Filename, string(content)
	} else {
		var err error
		payload, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
	}
	if strings.Contains(result.Path, "/interactions/") {
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Error(err)
		}
	} else if err := json.Unmarshal(payload, &result.Message); err != nil {
		t.Error(err)
	}
	return result
}

func TestNoteDeliveryPathsPreserveVisibilityReplyAndMentions(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
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
	for _, name := range []string{"ordinary", "kmp", donorWelcomeNote} {
		if err := database.AddNote(name, "test", "initial"); err != nil {
			t.Fatal(err)
		}
		option, err := json.Marshal(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, long := range []bool{false, true} {
			text := "**Note**\n界𝄞 @everyone <@123> <@&456> https://example.com\n"
			if name == "kmp" {
				text = "## Question\n" + text
			}
			if long {
				text += strings.Repeat("full answer 界𝄞\n", 400)
			}
			stored := text
			if name == "kmp" {
				stored = notefmt.Fence("faq", text)
			}
			if err := database.EditNote(name, "test", stored); err != nil {
				t.Fatal(err)
			}
			paths := []string{"trigger", "private", "stay"}
			if name == donorWelcomeNote {
				paths = []string{"donor"}
			}
			for _, path := range paths {
				switch path {
				case "trigger":
					b.onMessageCreate(client, &discord.Message{ID: 23, ChannelID: 5, GuildID: ptr(snowflake.ID(12)), Content: "." + name, Author: discord.User{ID: 56}})
				case "donor":
					b.sendDonorWelcome(client, "56")
				default:
					b.handleNote(client, interaction, map[string]discord.SlashCommandOption{"name": {Value: option}}, "56", path == "stay")
				}
				got := <-requests
				if long {
					if got.Filename != "note.md" || got.FileContent != text || len(got.Message.Components) != 0 {
						t.Fatalf("%s/%s: full file not delivered", name, path)
					}
				} else if got.Filename != "" || (name != "kmp" && got.Message.Content != text && path != "donor") {
					t.Fatalf("%s/%s: short note changed", name, path)
				}
				mentions := got.Message.AllowedMentions
				if mentions == nil {
					t.Fatal("missing mention restrictions")
				}
				if path == "trigger" {
					if got.Message.MessageReference == nil || value(got.Message.MessageReference.MessageID) != 23 || got.Message.Flags.Has(discord.MessageFlagEphemeral) {
						t.Fatal("trigger lost public reply reference")
					}
				} else if path == "donor" {
					want := "<@56>\n"
					if !long {
						want += text
					}
					if got.Message.Content != want || !slices.Equal(mentions.Users, []snowflake.ID{56}) || len(mentions.Parse) != 0 || !strings.HasSuffix(got.Path, "/channels/"+donorWelcomeChannelID+"/messages") {
						t.Fatal("donor lost target-only ping")
					}
				} else {
					if got.Type != discord.InteractionResponseTypeCreateMessage || got.Message.Flags.Has(discord.MessageFlagEphemeral) != (path == "private") {
						t.Fatal("slash note lost visibility")
					}
					want := []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}
					if name == "kmp" && !long {
						want = nil
					}
					if !slices.Equal(mentions.Parse, want) || len(mentions.Roles) != 0 {
						t.Fatal("slash note broadened allowed mentions")
					}
				}
			}
		}
	}
}

func TestKMPSelectionAfterNoteBecomesOversized(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	requests := make(chan deliveredNote, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- readDeliveredNote(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	b := &Bot{Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop()}
	if err := database.AddNote("kmp", "test", "initial"); err != nil {
		t.Fatal(err)
	}
	var interaction discord.ComponentInteraction
	if err := json.Unmarshal([]byte(`{"id":"17","application_id":"1","token":"token","type":3,"channel":{"id":"5","type":0},"message":{"id":"38","flags":32768},"data":{"custom_id":"note:kmp:question","component_type":3,"values":["0"]}}`), &interaction); err != nil {
		t.Fatal(err)
	}
	for _, long := range []bool{false, true} {
		text := "## Question\nshort"
		if long {
			text += strings.Repeat("\nfull 界𝄞 @everyone <@123>", 400)
		}
		if err := database.EditNote("kmp", "test", notefmt.Fence("faq", text)); err != nil {
			t.Fatal(err)
		}
		if !b.handleNoteComponent(client, &events.ComponentInteractionCreate{ComponentInteraction: interaction}) {
			t.Fatal("KMP selection was not handled")
		}
		got := <-requests
		if long {
			if got.Type != discord.InteractionResponseTypeCreateMessage || got.Filename != "note.md" || got.FileContent != text || got.Message.Flags != discord.MessageFlagEphemeral || len(got.Message.Components) != 0 {
				t.Fatal("edited oversized FAQ was not delivered as a private full attachment")
			}
		} else if got.Type != discord.InteractionResponseTypeUpdateMessage || !got.Message.Flags.Has(discord.MessageFlagIsComponentsV2) || got.Filename != "" {
			t.Fatal("valid FAQ did not retain component pagination")
		}
		if got.Message.AllowedMentions == nil || len(got.Message.AllowedMentions.Parse) != 0 || len(got.Message.AllowedMentions.Users) != 0 {
			t.Fatal("FAQ selection allowed mentions")
		}
	}
}
