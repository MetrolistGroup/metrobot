package discord

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	"github.com/MetrolistGroup/metrobot/util"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"go.uber.org/zap"
)

const kmpNoteSelectCustomID = "note:kmp:question" // Keep already-posted legacy FAQ menus usable.

func newDiscordNoteMessage(content, prefix string) discord.MessageCreate {
	message := discord.MessageCreate{Content: prefix + content}
	if util.UTF16Len(message.Content) > 2000 {
		message.Content = prefix
		message.Files = []*discord.File{{Name: "note.md", Reader: strings.NewReader(content)}}
	}
	return message
}

func noteSelectCustomID(name string) string {
	// Names are unbounded Unicode; never embed them directly in Discord's 100-character ID.
	return fmt.Sprintf("note:faq:%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name)))))
}

func noteMoreCustomID(name string) string {
	return strings.Replace(noteSelectCustomID(name), "note:faq:", "note:more:", 1)
}

func noteOptionText(text string) string {
	if util.UTF16Len(text) <= 100 {
		return text
	}
	units := 0
	for index, r := range text {
		units += utf16.RuneLen(r)
		if units > 99 {
			return text[:index] + "…"
		}
	}
	return text
}

func noteComponents(note notefmt.Rendered, name, prefix string, selected int) ([]discord.LayoutComponent, bool) {
	before, after := prefix+note.Before, note.After
	var body []discord.ContainerSubComponent
	if strings.TrimSpace(before) != "" {
		body = append(body, discord.TextDisplayComponent{Content: before})
	}
	total := 0
	for _, text := range []string{before, after} {
		if strings.TrimSpace(text) != "" {
			total += util.UTF16Len(text)
		}
	}
	count := 1 + len(body)
	if faq := note.FAQ; faq != nil {
		if len(faq.Items) < 1 || len(faq.Items) > 25 || selected < -1 || selected >= len(faq.Items) {
			return nil, false
		}
		header := faq.DisplayHeader()
		options := make([]discord.StringSelectMenuOption, len(faq.Items))
		for index, item := range faq.Items {
			// Validate every page before publishing a selector, including unselected answers.
			if item.Question == "" || item.Answer == "" || total+util.UTF16Len(header+"## "+item.Question+"\n"+item.Answer) > 4000 {
				return nil, false
			}
			description := strings.NewReplacer("*", "", "`", "").Replace(strings.Join(strings.Fields(item.Answer), " "))
			options[index] = discord.StringSelectMenuOption{Label: noteOptionText(item.Question), Value: strconv.Itoa(index), Description: noteOptionText(description), Default: index == selected}
		}
		body = append(body, discord.TextDisplayComponent{Content: header})
		if selected >= 0 {
			body = append(body, discord.SeparatorComponent{}, discord.TextDisplayComponent{Content: "## " + faq.Items[selected].Question + "\n" + faq.Items[selected].Answer})
		}
		body = append(body, discord.ActionRowComponent{Components: []discord.InteractiveComponent{discord.StringSelectMenuComponent{CustomID: noteSelectCustomID(name), Placeholder: "Choose a question", MaxValues: 1, Options: options}}})
		count += 5
	} else if layout := note.Layout; layout != nil {
		if total+layout.TextLength() > 4000 {
			return nil, false
		}
		count += layout.ComponentCount()
		for _, c := range layout.Components {
			switch c.Type {
			case "text":
				body = append(body, discord.TextDisplayComponent{Content: c.Content})
			case "separator":
				body = append(body, discord.SeparatorComponent{})
			case "section":
				body = append(body, discord.SectionComponent{Components: []discord.SectionSubComponent{discord.TextDisplayComponent{Content: c.Content}}, Accessory: discord.NewThumbnail(c.Thumbnail)})
			case "gallery":
				items := make([]discord.MediaGalleryItem, len(c.URLs))
				for i, url := range c.URLs {
					items[i] = discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: url}}
				}
				body = append(body, discord.MediaGalleryComponent{Items: items})
			case "buttons":
				buttons := make([]discord.InteractiveComponent, len(c.Buttons))
				for i, button := range c.Buttons {
					buttons[i] = discord.ButtonComponent{Style: discord.ButtonStyleLink, Label: button.Label, URL: button.URL}
				}
				body = append(body, discord.ActionRowComponent{Components: buttons})
			}
		}
	} else {
		return nil, false
	}
	if strings.TrimSpace(after) != "" {
		body = append(body, discord.TextDisplayComponent{Content: after})
		count++
	}
	if len(body) > 10 || count > 40 {
		return nil, false
	}
	return []discord.LayoutComponent{discord.ContainerComponent{Components: body}}, true
}

func authoredNoteMessage(note notefmt.Rendered, name, prefix string, selected int) discord.MessageCreate {
	if components, ok := noteComponents(note, name, prefix, selected); ok {
		return discord.MessageCreate{Components: components, Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: &discord.AllowedMentions{}}
	}
	return newDiscordNoteMessage(note.Text, prefix)
}

func (b *Bot) sendNoteReply(s *bot.Client, channelID, messageID, name string, note notefmt.Rendered) *discord.Message {
	attempts := 2
	if _, rich := noteComponents(note, name, "", -1); rich {
		attempts = 4 // Try readable text if Discord refuses an otherwise valid rich layout.
	}
	for attempt := 0; attempt < attempts; attempt++ {
		// Rebuild so an attachment's reader is not exhausted by a failed request.
		message := authoredNoteMessage(note, name, "", -1)
		if attempt >= 2 {
			message = newDiscordNoteMessage(note.Text, "")
		}
		message.AllowedMentions = &discord.AllowedMentions{}
		if attempt%2 == 0 {
			message.MessageReference = &discord.MessageReference{MessageID: snowflakePtr(messageID)}
		}
		reply, err := s.Rest.CreateMessage(snowflakeID(channelID), message)
		if err == nil {
			return reply
		}
		if !noteRequestRejected(err) || attempt == attempts-1 {
			b.Logger.Error("failed to send note", zap.Error(err))
			return nil
		}
	}
	return nil
}

// A transport failure may follow a successful send; only retry confirmed rejections.
func noteRequestRejected(err error) bool {
	var rejected *rest.Error
	return errors.As(err, &rejected) && rejected.Response != nil &&
		(rejected.Response.StatusCode == http.StatusBadRequest || rejected.Response.StatusCode == http.StatusNotFound)
}

func (b *Bot) respondNote(s *bot.Client, i discord.Interaction, name string, note notefmt.Rendered, ephemeral bool) {
	message := authoredNoteMessage(note, name, "", -1)
	if message.AllowedMentions == nil {
		message.AllowedMentions = &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}}
	}
	if ephemeral {
		message.Flags |= discord.MessageFlagEphemeral
		if i.Type() == discord.InteractionTypeComponent {
			message.AllowedMentions = &discord.AllowedMentions{}
		}
	}
	err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeCreateMessage, Data: message})
	if noteRequestRejected(err) && message.Flags.Has(discord.MessageFlagIsComponentsV2) {
		fallback := newDiscordNoteMessage(note.Text, "")
		fallback.AllowedMentions = message.AllowedMentions
		if ephemeral {
			fallback.Flags |= discord.MessageFlagEphemeral
		}
		err = s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeCreateMessage, Data: fallback})
	}
	if err != nil {
		b.Logger.Error("failed to respond with note", zap.Error(err))
	}
}

func (b *Bot) handleNoteComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	if i.Data.Type() == discord.ComponentTypeButton && strings.HasPrefix(i.Data.CustomID(), "note:more:") {
		unavailable := func() bool { respondEphemeral(s, i, "That note is no longer available."); return true }
		if b.Notes == nil || b.Notes.DB == nil {
			return unavailable()
		}
		names, err := b.Notes.DB.ListNotes()
		if err != nil {
			return unavailable()
		}
		for _, note := range names {
			if noteMoreCustomID(note.Name) != i.Data.CustomID() {
				continue
			}
			document, err := b.Notes.GetNoteDocument(note.Name)
			if err != nil {
				return unavailable()
			}
			b.respondNote(s, i, note.Name, document.Render(), true)
			return true
		}
		return unavailable()
	}
	if i.Data.Type() != discord.ComponentTypeStringSelectMenu {
		return false
	}
	data := i.StringSelectMenuInteractionData()
	id := data.CustomID()
	if id != kmpNoteSelectCustomID && !strings.HasPrefix(id, "note:faq:") {
		return false
	}
	unavailable := func() bool { respondEphemeral(s, i, "That FAQ question is unavailable."); return true }
	if len(data.Values) != 1 {
		return unavailable()
	}
	selected, err := strconv.Atoi(data.Values[0])
	if err != nil || selected < 0 {
		return unavailable()
	}
	name := "kmp"
	if id != kmpNoteSelectCustomID {
		names, err := b.Notes.DB.ListNotes()
		if err != nil {
			return unavailable()
		}
		name = ""
		for _, n := range names {
			if noteSelectCustomID(n.Name) == id {
				name = n.Name
				break
			}
		}
		if name == "" {
			return unavailable()
		}
	}
	document, err := b.Notes.GetNoteDocument(name)
	if err != nil {
		return unavailable()
	}
	note := document.Render()
	if note.FAQ == nil || selected >= len(note.FAQ.Items) {
		return unavailable()
	}
	components, ok := noteComponents(note, name, "", selected)
	message := discord.MessageCreate{Components: components, Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: &discord.AllowedMentions{}}
	responseType := discord.InteractionResponseTypeUpdateMessage
	if !ok {
		// A posted V2 message cannot turn into plain text; send a private readable fallback instead.
		message = newDiscordNoteMessage(note.Text, "")
		message.Flags = discord.MessageFlagEphemeral
		message.AllowedMentions = &discord.AllowedMentions{}
		responseType = discord.InteractionResponseTypeCreateMessage
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: responseType, Data: message}); err != nil {
		b.Logger.Error("failed to update FAQ note", zap.Error(err))
	}
	return true
}
