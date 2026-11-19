package discord

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

const (
	dailyKudosAmount         = 250
	dailyKudosCooldown       = 24 * time.Hour
	blackjackIdle            = 5 * time.Minute
	blackjackComponentPrefix = "bj:"
)

// handleSigmaCommand runs the `>>` fun commands carried over from the Sigma bot.
func (b *Bot) handleSigmaCommand(s *bot.Client, m *discord.Message, content string) bool {
	rest, found := strings.CutPrefix(content, ">>")
	if !found {
		return false
	}
	name, args, _ := strings.Cut(strings.TrimSpace(rest), " ")
	args = strings.TrimSpace(args)
	userID := m.Author.ID.String()
	switch strings.ToLower(name) {
	case "wa":
		b.handleWolfram(s, m, args)
	case "daily":
		b.handleDaily(s, m, userID)
	case "lb":
		b.handleKudosLeaderboard(s, m, userID)
	case "slots":
		b.handleSlots(s, m, userID, args)
	case "bj":
		b.handleBlackjack(s, m, userID, args)
	default:
		return false
	}
	return true
}

func (b *Bot) kudosReply(s *bot.Client, m *discord.Message, message discord.MessageCreate) (*discord.Message, error) {
	message.MessageReference = &discord.MessageReference{MessageID: &m.ID}
	message.AllowedMentions = &discord.AllowedMentions{}
	msg, err := s.Rest.CreateMessage(m.ChannelID, message)
	if err != nil {
		b.Logger.Error("failed to send kudos reply", zap.Error(err))
	}
	return msg, err
}

func (b *Bot) kudosText(s *bot.Client, m *discord.Message, content string) {
	b.kudosReply(s, m, discord.MessageCreate{Content: content})
}

func (b *Bot) handleDaily(s *bot.Client, m *discord.Message, userID string) {
	balance, next, ok, err := b.DB.ClaimDaily(userID, dailyKudosAmount, dailyKudosCooldown, time.Now())
	switch {
	case err != nil:
		b.Logger.Error("failed to claim daily kudos", zap.Error(err))
		b.kudosText(s, m, "The kudos bank is down, try again in a bit.")
	case !ok:
		b.kudosText(s, m, fmt.Sprintf("Already claimed. Your next daily is <t:%d:R>. Balance: **%d** kudos.", next.Unix(), balance))
	default:
		b.kudosText(s, m, fmt.Sprintf("+%d kudos! Balance: **%d**. Next daily <t:%d:R>.", dailyKudosAmount, balance, next.Unix()))
	}
}

func (b *Bot) handleKudosLeaderboard(s *bot.Client, m *discord.Message, userID string) {
	entries, err := b.DB.KudosLeaderboard(10)
	if err != nil {
		b.Logger.Error("failed to load kudos leaderboard", zap.Error(err))
		b.kudosText(s, m, "The kudos bank is down, try again in a bit.")
		return
	}
	var out strings.Builder
	out.WriteString("**Kudos leaderboard**\n")
	if len(entries) == 0 {
		out.WriteString("Nobody has kudos yet. Start with `>>daily`.\n")
	}
	for index, entry := range entries {
		fmt.Fprintf(&out, "%d. <@%s> · %d\n", index+1, entry.UserID, entry.Balance)
	}
	balance, balanceErr := b.DB.Kudos(userID)
	rank, rankErr := b.DB.KudosRank(userID)
	if balanceErr == nil && rankErr == nil && rank > 0 {
		fmt.Fprintf(&out, "-# You: #%d · %d kudos", rank, balance)
	}
	b.kudosText(s, m, out.String())
}

// placeBet parses a bet ("100" or "all") and deducts it; false means the user was already told why not.
func (b *Bot) placeBet(s *bot.Client, m *discord.Message, userID, arg, game string) (int64, bool) {
	var bet int64
	if arg = strings.ToLower(arg); arg == "all" || arg == "max" {
		balance, err := b.DB.Kudos(userID)
		if err != nil {
			b.Logger.Error("failed to read kudos", zap.Error(err))
			b.kudosText(s, m, "The kudos bank is down, try again in a bit.")
			return 0, false
		}
		if balance == 0 {
			b.kudosText(s, m, "You have no kudos. Grab some with `>>daily`.")
			return 0, false
		}
		bet = balance
	} else if parsed, err := strconv.ParseInt(arg, 10, 64); err == nil && parsed > 0 {
		bet = parsed
	} else {
		b.kudosText(s, m, fmt.Sprintf("Usage: `>>%s <bet|all>`", game))
		return 0, false
	}
	_, ok, err := b.DB.SpendKudos(userID, bet)
	if err != nil {
		b.Logger.Error("failed to spend kudos", zap.Error(err))
		b.kudosText(s, m, "The kudos bank is down, try again in a bit.")
		return 0, false
	}
	if !ok {
		balance, _ := b.DB.Kudos(userID)
		b.kudosText(s, m, fmt.Sprintf("You only have **%d** kudos. Grab more with `>>daily`.", balance))
	}
	return bet, ok
}

func (b *Bot) payKudos(userID string, amount int64) (int64, string) {
	balance, err := b.DB.AddKudos(userID, amount)
	if err != nil {
		b.Logger.Error("failed to pay kudos", zap.String("user", userID), zap.Int64("amount", amount), zap.Error(err))
		return 0, " Payout failed, ping an admin."
	}
	return balance, ""
}

// --- Slots ---

var slotSymbols = []struct {
	face   string
	weight int
	triple int64
}{{"🍒", 6, 4}, {"🍋", 5, 6}, {"🔔", 4, 10}, {"⭐", 3, 20}, {"💎", 2, 50}}

func spinSlots(roll func(int) int) [3]int {
	total := 0
	for _, symbol := range slotSymbols {
		total += symbol.weight
	}
	var reels [3]int
	for reel := range reels {
		n := roll(total)
		for n >= slotSymbols[reels[reel]].weight {
			n -= slotSymbols[reels[reel]].weight
			reels[reel]++
		}
	}
	return reels
}

// slotPayout returns what goes back to the player, stake included: a pair refunds, three of a kind multiplies.
func slotPayout(reels [3]int, bet int64) int64 {
	switch a, b, c := reels[0], reels[1], reels[2]; {
	case a == b && b == c:
		return bet * slotSymbols[a].triple
	case a == b || b == c || a == c:
		return bet
	}
	return 0
}

func (b *Bot) handleSlots(s *bot.Client, m *discord.Message, userID, arg string) {
	bet, ok := b.placeBet(s, m, userID, arg, "slots")
	if !ok {
		return
	}
	reels := spinSlots(rand.IntN)
	payout := slotPayout(reels, bet)
	balance, failure := b.payKudos(userID, payout)
	var outcome string
	switch {
	case payout > bet:
		outcome = fmt.Sprintf("Three %s! You win **%d** kudos.", slotSymbols[reels[0]].face, payout-bet)
	case payout == bet:
		outcome = "A pair, you get your bet back."
	default:
		outcome = fmt.Sprintf("No luck, you lost **%d** kudos.", bet)
	}
	b.kudosText(s, m, fmt.Sprintf("**[ %s | %s | %s ]**\n%s Balance: **%d**.%s",
		slotSymbols[reels[0]].face, slotSymbols[reels[1]].face, slotSymbols[reels[2]].face, outcome, balance, failure))
}

// --- Blackjack ---

// shortcut: games live in memory, so a restart forfeits bets on open hands; persist them if restarts mid-game become common.
type blackjackGame struct {
	userID               string
	bet                  int64
	deck, player, dealer []int
	channelID, messageID snowflake.ID
	timer                *time.Timer
}

func newBlackjackGame(userID string, bet int64, deck []int) *blackjackGame {
	g := &blackjackGame{userID: userID, bet: bet, deck: deck}
	g.player = []int{g.draw(), g.draw()}
	g.dealer = []int{g.draw(), g.draw()}
	return g
}

func (g *blackjackGame) draw() int {
	card := g.deck[0]
	g.deck = g.deck[1:]
	return card
}

func handValue(cards []int) int {
	total, aces := 0, 0
	for _, card := range cards {
		rank := card%13 + 1
		switch {
		case rank == 1:
			total += 11
			aces++
		case rank > 10:
			total += 10
		default:
			total += rank
		}
	}
	for total > 21 && aces > 0 {
		total -= 10
		aces--
	}
	return total
}

func isBlackjack(cards []int) bool { return len(cards) == 2 && handValue(cards) == 21 }

func formatCards(cards []int) string {
	parts := make([]string, len(cards))
	for index, card := range cards {
		parts[index] = []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}[card%13] + string([]rune("♠♥♦♣")[card/13])
	}
	return strings.Join(parts, " ")
}

// blackjackPayout settles finished hands; the stake is included in the payout.
func blackjackPayout(player, dealer []int, bet int64) (int64, string) {
	p, d := handValue(player), handValue(dealer)
	switch {
	case p > 21:
		return 0, "Bust, you lose."
	case isBlackjack(player) && isBlackjack(dealer):
		return bet, "Both blackjack, push."
	case isBlackjack(player):
		return bet + bet*3/2, "Blackjack!"
	case isBlackjack(dealer):
		return 0, "Dealer blackjack, you lose."
	case d > 21:
		return 2 * bet, "Dealer busts, you win!"
	case p > d:
		return 2 * bet, "You win!"
	case p == d:
		return bet, "Push."
	}
	return 0, "Dealer wins."
}

func (g *blackjackGame) render(done bool, footer string) string {
	dealer := formatCards(g.dealer[:1]) + " ??"
	if done {
		dealer = fmt.Sprintf("%s (%d)", formatCards(g.dealer), handValue(g.dealer))
	}
	return fmt.Sprintf("**Blackjack** · <@%s> bet %d kudos\nDealer: %s\nYou: %s (%d)%s",
		g.userID, g.bet, dealer, formatCards(g.player), handValue(g.player), footer)
}

// finishBlackjack plays the dealer, pays out and forgets the game. Callers hold blackjackMu.
func (b *Bot) finishBlackjack(g *blackjackGame) string {
	delete(b.blackjackGames, g.userID)
	if g.timer != nil {
		g.timer.Stop()
	}
	if handValue(g.player) <= 21 && !isBlackjack(g.player) {
		for handValue(g.dealer) < 17 {
			g.dealer = append(g.dealer, g.draw())
		}
	}
	payout, outcome := blackjackPayout(g.player, g.dealer, g.bet)
	balance, failure := b.payKudos(g.userID, payout)
	return g.render(true, fmt.Sprintf("\n**%s** Balance: **%d**.%s", outcome, balance, failure))
}

func blackjackButtons(userID string) []discord.LayoutComponent {
	return []discord.LayoutComponent{discord.ActionRowComponent{Components: []discord.InteractiveComponent{
		discord.ButtonComponent{Label: "Hit", Style: discord.ButtonStylePrimary, CustomID: blackjackComponentPrefix + "hit:" + userID},
		discord.ButtonComponent{Label: "Stand", Style: discord.ButtonStyleSecondary, CustomID: blackjackComponentPrefix + "stand:" + userID},
	}}}
}

func (b *Bot) handleBlackjack(s *bot.Client, m *discord.Message, userID, arg string) {
	// Reserve the seat before betting so two quick `>>bj` messages cannot open two hands.
	b.blackjackMu.Lock()
	if b.blackjackGames == nil {
		b.blackjackGames = make(map[string]*blackjackGame)
	}
	if _, playing := b.blackjackGames[userID]; playing {
		b.blackjackMu.Unlock()
		b.kudosText(s, m, "Finish your current hand first.")
		return
	}
	seat := &blackjackGame{userID: userID}
	b.blackjackGames[userID] = seat
	b.blackjackMu.Unlock()

	bet, ok := b.placeBet(s, m, userID, arg, "bj")
	b.blackjackMu.Lock()
	defer b.blackjackMu.Unlock()
	if !ok {
		delete(b.blackjackGames, userID)
		return
	}
	g := newBlackjackGame(userID, bet, rand.Perm(52))
	b.blackjackGames[userID] = g
	if isBlackjack(g.player) || isBlackjack(g.dealer) {
		b.kudosText(s, m, b.finishBlackjack(g))
		return
	}
	msg, err := b.kudosReply(s, m, discord.MessageCreate{Content: g.render(false, ""), Components: blackjackButtons(userID)})
	if err != nil {
		delete(b.blackjackGames, userID)
		b.payKudos(userID, bet)
		return
	}
	g.channelID, g.messageID = msg.ChannelID, msg.ID
	g.timer = time.AfterFunc(blackjackIdle, func() {
		b.blackjackMu.Lock()
		if b.blackjackGames[userID] != g {
			b.blackjackMu.Unlock()
			return
		}
		content := b.finishBlackjack(g) + "\n-# Auto-stood after 5 idle minutes."
		b.blackjackMu.Unlock()
		if _, err := s.Rest.UpdateMessage(g.channelID, g.messageID, discord.MessageUpdate{
			Content: &content, Components: &[]discord.LayoutComponent{}, AllowedMentions: &discord.AllowedMentions{},
		}); err != nil {
			b.Logger.Error("failed to close idle blackjack hand", zap.Error(err))
		}
	})
}

func (b *Bot) handleBlackjackComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	rest, found := strings.CutPrefix(i.Data.CustomID(), blackjackComponentPrefix)
	if !found {
		return false
	}
	action, owner, _ := strings.Cut(rest, ":")
	if i.User().ID.String() != owner {
		respondEphemeral(s, i, "This isn't your hand. Start one with `>>bj <bet>`.")
		return true
	}
	b.blackjackMu.Lock()
	g := b.blackjackGames[owner]
	if g == nil || g.messageID != i.Message.ID {
		b.blackjackMu.Unlock()
		respondEphemeral(s, i, "This hand is over.")
		return true
	}
	if action == "hit" {
		g.player = append(g.player, g.draw())
	}
	update := discord.MessageUpdate{AllowedMentions: &discord.AllowedMentions{}}
	var content string
	if action == "stand" || handValue(g.player) >= 21 {
		content = b.finishBlackjack(g)
		update.Components = &[]discord.LayoutComponent{}
	} else {
		content = g.render(false, "")
		g.timer.Reset(blackjackIdle)
	}
	b.blackjackMu.Unlock()
	update.Content = &content
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeUpdateMessage, Data: update,
	}); err != nil {
		b.Logger.Error("failed to update blackjack hand", zap.Error(err))
	}
	return true
}
