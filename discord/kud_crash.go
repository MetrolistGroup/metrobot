package discord

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

const (
	crashLobby        = 15 * time.Second
	crashTick         = 2 * time.Second
	crashGrowth       = 0.12
	crashCap    int64 = 10_000
)

type crashPlayer struct {
	cashout, balance int64
	decided, paid    bool
}

type kudCrash struct {
	updateMu       sync.Mutex
	point          int64
	multiplier     int64
	running, ended bool
	players        map[string]*crashPlayer
}

type crashInput struct {
	at          time.Time
	interaction *events.ComponentInteractionCreate
	parts       []string
	game        *kudGame
	err         error
}

func (b *Bot) queueCrashInput(i *events.ComponentInteractionCreate) *crashInput {
	id, ok := strings.CutPrefix(i.Data.CustomID(), "kud:crash:")
	if !ok {
		return nil
	}
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return nil
	}
	b.crashInputMu.Lock()
	defer b.crashInputMu.Unlock()
	input := &crashInput{at: time.Now(), interaction: i, parts: parts}
	b.crashInputs = append(b.crashInputs, input)
	return input
}

// Callers hold kudMu. The cutoff and queue snapshot must share the arrival lock.
func (b *Bot) drainCrashInputs() time.Time {
	b.crashInputMu.Lock()
	inputs, now := b.crashInputs, time.Now()
	b.crashInputs = nil
	b.crashInputMu.Unlock()
	for _, input := range inputs {
		i := input.interaction
		g := b.kudGames["crash:"+input.parts[0]]
		version, err := strconv.Atoi(input.parts[1])
		switch {
		case g == nil || g.messageID != i.Message.ID || g.channelID != i.Channel().ID():
			input.err = fmt.Errorf("this round is already over")
		case err != nil || version != g.version:
			input.err = fmt.Errorf("the board changed. Choose again")
		case i.User().Bot:
			input.err = fmt.Errorf("bots cannot join Kud games")
		default:
			input.game = g
			input.err = b.playCrashAction(g, input.parts[2], i.User().ID.String(), i.User().Username, input.at)
		}
	}
	return now
}

func (b *Bot) handleCrashComponent(s *bot.Client, i *events.ComponentInteractionCreate, input *crashInput) {
	ackErr := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeDeferredUpdateMessage})
	b.kudMu.Lock()
	b.drainCrashInputs()
	g, err := input.game, input.err
	b.kudMu.Unlock()
	if ackErr == nil && err != nil {
		if _, sendErr := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{Content: err.Error(), Flags: discord.MessageFlagEphemeral, AllowedMentions: &discord.AllowedMentions{}}); sendErr != nil {
			b.Logger.Warn("failed to send Crash interaction error", zap.Error(sendErr))
		}
	}
	if g != nil {
		g.crash.updateMu.Lock()
		defer g.crash.updateMu.Unlock()
		b.kudMu.Lock()
		components := g.render()
		b.kudMu.Unlock()
		update := discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}
		if ackErr != nil {
			_, err = s.Rest.UpdateMessage(g.channelID, g.messageID, update)
		} else {
			_, err = s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), update)
		}
		if err != nil {
			b.Logger.Warn("failed to update Crash round", zap.Error(err))
		}
	}
}

func newKudCrash(owner string) *kudCrash {
	var sample [8]byte
	rand.Read(sample[:])
	return &kudCrash{
		point:      crashPoint(binary.LittleEndian.Uint64(sample[:]) >> 12),
		multiplier: 100,
		players:    map[string]*crashPlayer{owner: {}},
	}
}

// The 52-bit draw fixes the outcome before entry; the house edge is 3% before rounding.
func crashPoint(sample uint64) int64 {
	return max(100, int64(97*(uint64(1)<<52)/(sample+1)))
}

func (g *kudGame) escrowID(userID string) string {
	if g.kind == "crash" {
		return g.id + ":" + userID
	}
	return g.id
}

func (g *kudGame) crashEnd() time.Time {
	duration := time.Duration(math.Log(math.Min(float64(g.crash.point), float64(crashCap))/100) / crashGrowth * float64(time.Second))
	return g.expires.Add((duration + crashTick - 1) / crashTick * crashTick)
}

func (g *kudGame) advanceCrash(now time.Time) {
	c := g.crash
	if now.Before(g.expires) || c.ended {
		return
	}
	c.running = true
	if !now.Before(g.crashEnd()) {
		c.ended = true
		c.multiplier = int64(math.Min(float64(c.point), float64(crashCap)))
		for _, player := range c.players {
			if !player.decided {
				player.decided = true
				if c.point > crashCap {
					player.cashout = crashCap
				}
			}
		}
		return
	}
	elapsed := now.Sub(g.expires).Truncate(crashTick)
	c.multiplier = int64(math.Min(float64(crashCap), 100*math.Exp(elapsed.Seconds()*crashGrowth)))
}

func (b *Bot) settleCrash(g *kudGame) error {
	var failure error
	allPaid := true
	for userID, player := range g.crash.players {
		if player.decided && !player.paid {
			balances, err := b.DB.SettleKud(g.escrowID(userID), map[string]int64{userID: g.bet * player.cashout / 100})
			if err != nil {
				b.Logger.Error("failed to settle Crash payout", zap.String("game", g.id), zap.String("user", userID), zap.Error(err))
				failure = fmt.Errorf("settlement delayed. Your outcome is fixed and will retry automatically")
			} else {
				player.paid, player.balance = true, balances[userID]
			}
		}
		allPaid = allPaid && player.paid
	}
	if g.crash.ended && allPaid {
		g.done = true
		delete(b.kudGames, g.id)
		g.timer.Stop()
	}
	return failure
}

func (b *Bot) playCrashAction(g *kudGame, action, userID, name string, now time.Time) error {
	g.advanceCrash(now)
	c := g.crash
	if c.ended {
		b.settleCrash(g)
		return fmt.Errorf("this round has ended. Cash out before the crash")
	}
	switch action {
	case "join":
		if c.running {
			return fmt.Errorf("this round has started. Join the next lobby")
		}
		if c.players[userID] != nil {
			return fmt.Errorf("you are already in this round")
		}
		if len(c.players) >= 32 {
			return fmt.Errorf("this Crash lobby is full")
		}
		if err := b.DB.HoldKud(g.escrowID(userID), g.kind, userID, g.bet); err != nil {
			return fmt.Errorf("couldn't reserve %s. Check `>>kud`", kudMoney(g.bet))
		}
		c.players[userID] = &crashPlayer{}
		g.entrants = append(g.entrants, kudEntrant{userID: userID, name: name})
	case "cash":
		player := c.players[userID]
		if player == nil {
			return fmt.Errorf("you did not join this round")
		}
		if !c.running {
			return fmt.Errorf("the round has not started yet")
		}
		if player.decided {
			return fmt.Errorf("you already cashed out. Pending payouts retry automatically")
		}
		player.cashout, player.decided = c.multiplier, true
		return b.settleCrash(g)
	default:
		return fmt.Errorf("choose Join or Cash out")
	}
	return nil
}

func (b *Bot) tickCrash(s *bot.Client, g *kudGame) {
	b.kudMu.Lock()
	if b.kudGames[g.id] != g || g.done {
		b.kudMu.Unlock()
		return
	}
	now := b.drainCrashInputs()
	g.advanceCrash(now)
	b.settleCrash(g)
	if !g.done {
		delay := crashTick
		if !g.crash.running {
			delay = time.Until(g.expires)
		} else if !g.crash.ended && time.Until(g.crashEnd()) < delay {
			delay = time.Until(g.crashEnd())
		}
		g.timer.Reset(max(time.Millisecond, delay))
	}
	b.kudMu.Unlock()
	g.crash.updateMu.Lock()
	defer g.crash.updateMu.Unlock()
	b.kudMu.Lock()
	components := g.render()
	b.kudMu.Unlock()
	if _, err := s.Rest.UpdateMessage(g.channelID, g.messageID, discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
		b.Logger.Warn("failed to update Crash round", zap.Error(err))
	}
}

func (g *kudGame) renderCrash() []discord.LayoutComponent {
	c := g.crash
	var body strings.Builder
	fmt.Fprintf(&body, "Stake **%s each** · Players **%d/32**\n", kudMoney(g.bet), len(g.entrants))
	var rows []discord.ContainerSubComponent
	switch {
	case c.ended:
		if c.point > crashCap {
			body.WriteString("**100.00x limit reached.** Remaining players cashed out automatically.\n")
		} else {
			fmt.Fprintf(&body, "**Crashed at %.2fx.** Uncashed stakes are lost.\n", float64(c.point)/100)
		}
	case c.running:
		fmt.Fprintf(&body, "## %.2fx\nCash out before the crash to keep your payout.\n", float64(c.multiplier)/100)
		rows = append(rows, discord.NewActionRow(g.button("cash", "Cash out", discord.ButtonStyleSuccess, false)))
	default:
		fmt.Fprintf(&body, "Starts <t:%d:R>. Join once at the host's stake.\n", g.expires.Unix())
		rows = append(rows, discord.NewActionRow(g.button("join", "Join · "+kudMoney(g.bet), discord.ButtonStylePrimary, len(c.players) >= 32)))
	}
	for _, entrant := range g.entrants {
		player := c.players[entrant.userID]
		fmt.Fprintf(&body, "\n<@%s> · ", entrant.userID)
		switch {
		case player.decided:
			if player.cashout == 0 {
				body.WriteString("lost the stake")
			} else {
				fmt.Fprintf(&body, "%.2fx · %s", float64(player.cashout)/100, kudMoney(g.bet*player.cashout/100))
			}
			if player.paid {
				fmt.Fprintf(&body, " · balance %s", kudMoney(player.balance))
			} else {
				body.WriteString(" · settlement pending")
			}
		case c.running:
			body.WriteString("in play")
		default:
			body.WriteString("joined")
		}
	}
	body.WriteString("\n\n-# 3% house edge before rounding. Crashes can happen at 1x. Multipliers and crashes advance together in 2s steps. Cash-outs pay the current step. Limit 100x. Payouts include the stake; loan repayments apply. No loan luck. Restarts refund held stakes.")
	return kudLayout("Coins Crash", body.String(), rows...)
}
