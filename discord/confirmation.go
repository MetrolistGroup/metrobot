package discord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/util"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// pendingConfirmation stores information about a pending command confirmation
type pendingConfirmation struct {
	action     string
	args       string
	callerID   string
	targetID   string
	channelID  string
	messageID  string
	timestamp  time.Time
	cancelFunc context.CancelFunc
}

// confirmationStore stores pending confirmations by message ID
type confirmationStore struct {
	confirmations map[string]*pendingConfirmation
}

func newConfirmationStore() *confirmationStore {
	return &confirmationStore{
		confirmations: make(map[string]*pendingConfirmation),
	}
}

func (cs *confirmationStore) add(id string, pc *pendingConfirmation) {
	cs.confirmations[id] = pc
}

func (cs *confirmationStore) get(id string) (*pendingConfirmation, bool) {
	pc, ok := cs.confirmations[id]
	return pc, ok
}

func (cs *confirmationStore) remove(id string) {
	delete(cs.confirmations, id)
}

// sendConfirmation sends a confirmation message with Yes/No buttons
func (b *Bot) sendConfirmation(s *discordgo.Session, channelID, callerID, action, args, targetID string) (string, error) {
	actionUpper := strings.ToUpper(action)
	targetRef := ""
	if targetID != "" {
		banner := b.newBanner()
		username, err := banner.GetUsername(targetID)
		if err != nil || username == "" {
			targetRef = fmt.Sprintf(" `@%s`", targetID)  // fallback to userID
		} else {
			targetRef = fmt.Sprintf(" `@%s`", username)
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Confirm %s", actionUpper),
		Description: fmt.Sprintf("Are you sure you want to %s%s?\n\n**Args:** %s", action, targetRef, args),
		Color:       0xFFA500, // Orange
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	buttons := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Yes",
					Style:    discordgo.SuccessButton,
					CustomID: "confirm_yes",
				},
				discordgo.Button{
					Label:    "No",
					Style:    discordgo.DangerButton,
					CustomID: "confirm_no",
				},
			},
		},
	}

	msg, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embed:      embed,
		Components: buttons,
	})
	if err != nil {
		return "", err
	}

	return msg.ID, nil
}

// handleConfirmationButton handles button interactions for confirmations
func (b *Bot) handleConfirmationButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if data.CustomID != "confirm_yes" && data.CustomID != "confirm_no" {
		return
	}

	pc, ok := b.confirmations.get(i.Message.ID)
	if !ok {
		respondEphemeral(s, i, "This confirmation has expired or is invalid.")
		return
	}

	// Verify the user clicking is the original caller
	if i.Member.User.ID != pc.callerID {
		respondEphemeral(s, i, "Only the person who issued the command can confirm it.")
		return
	}

	// Cancel the timeout
	if pc.cancelFunc != nil {
		pc.cancelFunc()
	}
	b.confirmations.remove(i.Message.ID)

	if data.CustomID == "confirm_no" {
		// Delete the confirmation message
		s.ChannelMessageDelete(pc.channelID, i.Message.ID)
		return
	}

	// Execute the action
	s.ChannelMessageDelete(pc.channelID, i.Message.ID)
	b.executePrefixCommand(s, pc.channelID, pc.callerID, pc.action, pc.args, pc.targetID)
}

// requestPrefixConfirmation requests confirmation for a prefix command
func (b *Bot) requestPrefixConfirmation(s *discordgo.Session, m *discordgo.MessageCreate, action, args, targetID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	msgID, err := b.sendConfirmation(s, m.ChannelID, m.Author.ID, action, args, targetID)
	if err != nil {
		b.Logger.Error("failed to send confirmation", zap.Error(err))
		cancel()
		return
	}

	pc := &pendingConfirmation{
		action:     action,
		args:       args,
		callerID:   m.Author.ID,
		targetID:   targetID,
		channelID:  m.ChannelID,
		messageID:  msgID,
		timestamp:  time.Now(),
		cancelFunc: cancel,
	}
	b.confirmations.add(msgID, pc)

	// Start timeout goroutine
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			// Timeout - clean up
			b.confirmations.remove(msgID)
			s.ChannelMessageDelete(pc.channelID, msgID)
		}
	}()
}

// executePrefixCommand executes a prefix command after confirmation
// args contains the full argument string (reason, or duration+reason for tban/mute)
// targetID is the resolved target user ID
func (b *Bot) executePrefixCommand(s *discordgo.Session, channelID, callerID, action, args, targetID string) {
	banner := b.newBanner()
	parts := strings.Fields(args)

	switch action {
	case "ban":
		resp, _, err := b.Moderation.Ban(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("ban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "dban":
		resp, _, err := b.Moderation.DBan(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("dban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "tban":
		if len(parts) < 2 {
			return
		}
		dur, err := util.ParseDuration(parts[0])
		if err != nil {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Invalid duration: %s", err))
			return
		}
		var reason string
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
		resp, _, err := b.Moderation.TBan(banner, callerID, targetID, dur, reason, b.Config)
		if err != nil {
			b.Logger.Error("tban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "sban":
		resp, _, err := b.Moderation.SBan(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("sban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "mute":
		if len(parts) < 2 {
			return
		}
		dur, err := util.ParseDuration(parts[0])
		if err != nil {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Invalid duration: %s", err))
			return
		}
		var reason string
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
		resp, _, err := b.Moderation.Mute(banner, callerID, targetID, dur, reason, b.Config)
		if err != nil {
			b.Logger.Error("mute failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "warn":
		resp, extras, _, err := b.Warn.Warn(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("warn failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)
		for _, extra := range extras {
			s.ChannelMessageSend(channelID, extra)
		}
	}
}
