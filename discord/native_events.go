package discord

import (
	"encoding/json"
	"fmt"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/bot/handlers"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"go.uber.org/zap"
)

func (b *Bot) gatewayHandlers() map[gateway.EventType]bot.GatewayEventHandler {
	gatewayHandlers := handlers.GetGatewayHandlers()
	// disgo splits bulk deletes into individual events; preserve one audit record.
	gatewayHandlers[gateway.EventTypeMessageDeleteBulk] = bot.NewGatewayEventHandler(gateway.EventTypeMessageDeleteBulk,
		func(client *bot.Client, sequence, shard int, event gateway.EventMessageDeleteBulk) {
			if thread, ok := client.Caches.GuildThread(event.ChannelID); ok {
				thread.MessageCount = max(0, thread.MessageCount-len(event.IDs))
				client.Caches.AddChannel(thread)
			}
			b.onMessageDeleteBulkLog(client, &event)
			for _, id := range event.IDs {
				go b.handleMessageDelete(client, &events.MessageDelete{GenericMessage: &events.GenericMessage{
					MessageID: id, ChannelID: event.ChannelID, GuildID: event.GuildID,
				}})
			}
		})
	return gatewayHandlers
}

func (b *Bot) installListeners() {
	s := b.Client
	s.AddEventListeners(&events.ListenerAdapter{
		OnReady:                         func(e *events.Ready) { b.readyOnce.Do(func() { close(b.ready) }) },
		OnApplicationCommandInteraction: func(e *events.ApplicationCommandInteractionCreate) { go b.onInteractionCreate(s, e) },
		OnAutocompleteInteraction:       func(e *events.AutocompleteInteractionCreate) { go b.handleAutocomplete(s, e) },
		OnComponentInteraction: func(e *events.ComponentInteractionCreate) {
			if snowflakeString(e.GuildID()) != b.Config.DiscordGuildID {
				return
			}
			go func() {
				if b.handleHelpComponent(s, e) || e.Data.Type() != discord.ComponentTypeStringSelectMenu {
					return
				}
				if !b.handleNoteComponent(s, e) && !b.handleGSMArenaComponent(s, e) && !b.handleHardwareComponent(s, e) {
					b.handleCompareComponent(s, e)
				}
			}()
		},
		OnMessageCreate: func(e *events.MessageCreate) {
			b.rememberMessageForLogs(s, &e.Message)
			go b.onMessageCreate(s, &e.Message)
		},
		OnGuildMemberJoin: func(e *events.GuildMemberJoin) { go b.onGuildMemberAdd(s, e) },
		OnGuildMemberUpdate: func(e *events.GuildMemberUpdate) {
			go b.onGuildMemberUpdate(s, e)
			go b.onGuildMemberUpdateLog(s, e)
		},
		OnGuildMemberLeave:   func(e *events.GuildMemberLeave) { go b.onGuildMemberRemoveLog(s, e) },
		OnGuildBan:           func(e *events.GuildBan) { go b.onGuildBanAddLog(s, e) },
		OnGuildUnban:         func(e *events.GuildUnban) { go b.onGuildBanRemoveLog(s, e) },
		OnRoleCreate:         func(e *events.RoleCreate) { go b.onGuildRoleCreateLog(s, e) },
		OnRoleUpdate:         func(e *events.RoleUpdate) { go b.onGuildRoleUpdateLog(s, e) },
		OnRoleDelete:         func(e *events.RoleDelete) { go b.onGuildRoleDeleteLog(s, e) },
		OnGuildChannelUpdate: func(e *events.GuildChannelUpdate) { go b.onChannelUpdateLog(s, e) },
		OnMessageReactionAdd: func(e *events.MessageReactionAdd) {
			go b.onReactionAddLog(s, e)
			go b.handleReactionAdd(s, e)
		},
		OnMessageReactionRemove: func(e *events.MessageReactionRemove) {
			go b.onReactionRemoveLog(s, e)
			go b.handleReactionRemove(s, e)
		},
		OnMessageDelete: func(e *events.MessageDelete) {
			b.onMessageDeleteLog(s, e)
			go b.handleMessageDelete(s, e)
		},
		OnRaw: func(e *events.Raw) {
			if e.EventType == gateway.EventTypeMessageUpdate {
				if err := b.onRawMessageUpdate(s, e); err != nil {
					b.Logger.Warn("failed to decode partial message update", zap.Error(err))
				}
			}
		},
	})
}

// The typed MESSAGE_UPDATE loses omitted-vs-empty fields, so merge only raw keys.
func (b *Bot) onRawMessageUpdate(s *bot.Client, event *events.Raw) error {
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(event.Payload).Decode(&fields); err != nil {
		return err
	}
	var partial discord.Message
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &partial); err != nil {
		return err
	}
	if partial.ID == 0 || partial.ChannelID == 0 {
		return fmt.Errorf("message update is missing IDs")
	}
	before, found := b.cachedMessage(partial.ChannelID.String(), partial.ID.String(), false)
	current := partial
	var old discord.Message
	if found && before.Message != nil {
		old = *before.Message
		current, err = mergeMessageUpdate(old, fields)
		if err != nil {
			return err
		}
	}
	b.onMessageUpdateLog(s, &events.MessageUpdate{GenericMessage: &events.GenericMessage{
		MessageID: current.ID, Message: current, ChannelID: current.ChannelID, GuildID: current.GuildID,
	}, OldMessage: old})
	return nil
}

func mergeMessageUpdate(old discord.Message, fields map[string]json.RawMessage) (discord.Message, error) {
	data, err := json.Marshal(old)
	if err != nil {
		return discord.Message{}, err
	}
	var merged map[string]json.RawMessage
	if err = json.Unmarshal(data, &merged); err != nil {
		return discord.Message{}, err
	}
	for key, value := range fields {
		merged[key] = value
	}
	data, err = json.Marshal(merged)
	if err != nil {
		return discord.Message{}, err
	}
	var message discord.Message
	err = json.Unmarshal(data, &message)
	return message, err
}
