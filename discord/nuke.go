package discord

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func isNukeTrigger(content string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(content), " ")) {
	case "garmin nuke them", "garmin, nuke them", "ok garmin nuke them", "ok garmin, nuke them":
		return true
	default:
		return false
	}
}

func (b *Bot) handleNukeReply(s *bot.Client, command *discord.Message) {
	if !b.nukeBusy.CompareAndSwap(false, true) {
		sendReply(s, command.ChannelID.String(), command.ID.String(), "Already rendering a nuke. Try again in a moment.", false, b.Logger)
		return
	}
	defer b.nukeBusy.Store(false)
	target := command.ReferencedMessage
	if target == nil && command.MessageReference != nil && value(command.MessageReference.MessageID) != 0 {
		target, _ = s.Rest.GetMessage(command.ChannelID, value(command.MessageReference.MessageID))
	}
	if target == nil || target.Author.ID == 0 {
		sendReply(s, command.ChannelID.String(), command.ID.String(), "Reply to someone's message with `garmin nuke them`.", false, b.Logger)
		return
	}
	copy := *target
	copy.GuildID = command.GuildID
	data, err := b.makeNukeGIF(s, &copy)
	if err != nil {
		b.Logger.Error("failed to render nuke", zap.Error(err))
		sendReply(s, command.ChannelID.String(), command.ID.String(), "I couldn't render that nuke.", false, b.Logger)
		return
	}
	_, err = s.Rest.CreateMessage(command.ChannelID, discord.MessageCreate{
		Files:           []*discord.File{{Name: "nuke.gif", Reader: bytes.NewReader(data)}},
		AllowedMentions: &discord.AllowedMentions{},
		MessageReference: &discord.MessageReference{
			MessageID: &target.ID,
		},
	})
	if err != nil {
		b.Logger.Error("failed to send nuke", zap.Error(err))
	}
}

func (b *Bot) handleNukeInteraction(s *bot.Client, i *events.ApplicationCommandInteractionCreate) {
	user := i.SlashCommandInteractionData().User("user")
	if user.ID == 0 {
		respondEphemeral(s, i, "Choose a user to nuke.")
		return
	}
	if !b.nukeBusy.CompareAndSwap(false, true) {
		respondEphemeral(s, i, "Already rendering a nuke. Try again in a moment.")
		return
	}
	defer b.nukeBusy.Store(false)
	if err := deferResponse(s, i, false); err != nil {
		b.Logger.Error("failed to defer nuke response", zap.Error(err))
		return
	}
	data, err := b.makeNukeGIF(s, &discord.Message{Author: user, GuildID: i.GuildID()})
	if err != nil {
		b.Logger.Error("failed to render nuke", zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't render that nuke.")
		return
	}
	_, err = s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{
		Files:           []*discord.File{{Name: "nuke.gif", Reader: bytes.NewReader(data)}},
		AllowedMentions: &discord.AllowedMentions{},
	})
	if err != nil {
		b.Logger.Error("failed to send nuke interaction", zap.Error(err))
	}
}

func (b *Bot) makeNukeGIF(s *bot.Client, target *discord.Message) ([]byte, error) {
	if target.Author.ID == 0 {
		return nil, fmt.Errorf("target has no author")
	}
	name, avatarURL := quoteAuthor(s, target)
	avatar, err := fetchQuoteAvatar(s.Rest.HTTPClient(), avatarURL)
	if err != nil {
		b.Logger.Debug("failed to fetch nuke avatar", zap.Error(err))
	}
	return renderNukeGIF(name, avatar)
}
