package discord

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

const (
	defaultBet               = 10
	slotsCooldown            = 60 * time.Second
	blackjackCooldown        = 12 * time.Second
	blackjackIdle            = 5 * time.Minute
	blackjackComponentPrefix = "bj:"
)

func (b *Bot) handleCompoundSigmaCommands(s *bot.Client, m *discord.Message, content string) bool {
	handled := false
	for {
		_, rest, found := strings.Cut(content, "{{")
		if !found {
			return handled
		}
		command, rest, found := strings.Cut(rest, "}}")
		if !found {
			return handled
		}
		content = rest
		if b.handleSigmaCommand(s, m, strings.TrimSpace(command)) {
			handled = true
		}
	}
}

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
	case "starlb", "stars":
		b.handleStarLeaderboard(s, m)
	case "slots":
		b.handleSlots(s, m, userID, args)
	case "bj":
		b.handleBlackjack(s, m, userID, args)
	case "kud", "balance":
		b.handleKudAccount(s, m)
	case "mines", "highlow", "hl", "fight", "plinko", "loan":
		if strings.EqualFold(name, "hl") {
			name = "highlow"
		}
		b.handleKudGame(s, m, strings.ToLower(name), args)
	case "rmme":
		b.handleRemind(s, m, args)
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

// onCooldown reports the remaining wait if a per-user game cooldown is still active, without arming one.
func (b *Bot) onCooldown(game, userID string) (time.Duration, bool) {
	b.cooldownMu.Lock()
	defer b.cooldownMu.Unlock()
	if until, ok := b.cooldowns[game+":"+userID]; ok && time.Now().Before(until) {
		return time.Until(until).Round(time.Second), true
	}
	return 0, false
}

// setCooldown arms a per-user game cooldown. It is only called once a bet has actually been placed.
func (b *Bot) setCooldown(game, userID string, d time.Duration) {
	b.cooldownMu.Lock()
	defer b.cooldownMu.Unlock()
	if b.cooldowns == nil {
		b.cooldowns = make(map[string]time.Time)
	}
	b.cooldowns[game+":"+userID] = time.Now().Add(d)
}

func (b *Bot) handleDaily(s *bot.Client, m *discord.Message, userID string) {
	amount, streak, balance, next, ok, err := b.DB.ClaimDaily(userID, rand.Int64N(1000), time.Now())
	switch {
	case err != nil:
		b.Logger.Error("failed to claim daily kudos", zap.Error(err))
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
	case !ok:
		b.kudosText(s, m, fmt.Sprintf("Your next daily is <t:%d:R>. Balance: **%s**.", next.Unix(), kudMoney(balance)))
	default:
		b.kudosText(s, m, fmt.Sprintf("You got **%s** for a %d/10 streak. Balance: **%s**. Next daily <t:%d:R>. Active loan repayments apply automatically.", kudMoney(amount), min(int(streak), 10), kudMoney(balance), next.Unix()))
	}
}

func (b *Bot) handleKudosLeaderboard(s *bot.Client, m *discord.Message, userID string) {
	entries, err := b.DB.KudosLeaderboard(10)
	if err != nil {
		b.Logger.Error("failed to load kudos leaderboard", zap.Error(err))
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
		return
	}
	var out strings.Builder
	out.WriteString("**" + kudEmoji + " Kud leaderboard**\n")
	if len(entries) == 0 {
		out.WriteString("Nobody has " + kudEmoji + " Kud yet. Start with `>>daily`.\n")
	}
	for index, entry := range entries {
		fmt.Fprintf(&out, "%d. <@%s> · %s\n", index+1, entry.UserID, kudMoney(entry.Balance))
	}
	balance, balanceErr := b.DB.Kudos(userID)
	rank, rankErr := b.DB.KudosRank(userID)
	if balanceErr == nil && rankErr == nil && rank > 0 {
		fmt.Fprintf(&out, "-# You: #%d · %s", rank, kudMoney(balance))
	}
	b.kudosText(s, m, out.String())
}

func (b *Bot) handleStarLeaderboard(s *bot.Client, m *discord.Message) {
	entries, err := b.DB.StarLeaderboard(10)
	if err != nil {
		b.Logger.Error("failed to load star leaderboard", zap.Error(err))
		b.kudosText(s, m, "The starboard is down, try again in a bit.")
		return
	}
	var out strings.Builder
	out.WriteString("**Star leaderboard** (starboard + sobboard)\n")
	if len(entries) == 0 {
		out.WriteString("No starred messages yet.\n")
	}
	for index, entry := range entries {
		fmt.Fprintf(&out, "%d. <@%s> · %d ⭐ across %d post", index+1, entry.UserID, entry.Stars, entry.Posts)
		if entry.Posts != 1 {
			out.WriteByte('s')
		}
		out.WriteByte('\n')
	}
	b.kudosText(s, m, out.String())
}

func (b *Bot) parseBet(s *bot.Client, m *discord.Message, userID, arg, game string) (int64, bool) {
	bet, err := b.kudBet(userID, arg)
	if err != nil {
		b.kudosText(s, m, err.Error())
		return 0, false
	}
	if err := b.DB.HoldKud(game+":"+m.ID.String(), game, userID, bet); err != nil {
		b.kudosText(s, m, "Couldn't reserve "+kudMoney(bet)+". Check `>>kud` and try again.")
		return 0, false
	}
	return bet, true
}

func (b *Bot) payKudos(gameID, userID string, amount int64) (int64, string) {
	balances, err := b.DB.SettleKud(gameID, map[string]int64{userID: amount})
	if err != nil {
		b.Logger.Error("failed to pay kudos", zap.String("user", userID), zap.Int64("amount", amount), zap.Error(err))
		return 0, " Settlement failed. Your stake remains held for recovery; contact staff."
	}
	return balances[userID], ""
}

// --- Slots ---
// Sigma gives every symbol an equal chance; the rarity only scales the reward. Three of a kind pays
// bet*rarity*len² *0.95, a pair pays bet*rarity*(len/2) *0.9 (on the middle row), and the stake is lost first.

var slotSymbols = []struct {
	face   string
	reward float64
}{
	{"🍆", 0.525}, {"🍒", 0.55}, {"⚓", 0.575}, {"🏵", 0.6}, {"💖", 0.625},
	{"🏮", 0.65}, {"🍥", 0.675}, {"💵", 0.7}, {"💳", 0.725}, {"🎁", 0.75},
	{"🐬", 0.775}, {"🐦", 0.8}, {"🌟", 0.825}, {"🦊", 0.85}, {"🦋", 0.875},
	{"🐍", 0.9}, {"🍬", 0.925}, {"💎", 0.95}, {"🔰", 0.975}, {"⚜", 1.0},
}

// slotWinnings returns the winnings (stake already spent) for the deciding middle row.
func slotWinnings(row [3]int, bet int64) int64 {
	three := float64(len(slotSymbols) * len(slotSymbols))
	two := float64(len(slotSymbols) / 2)
	switch a, b, c := row[0], row[1], row[2]; {
	case a == b && b == c:
		return int64(float64(bet) * slotSymbols[a].reward * three * 0.95)
	case a == b || a == c:
		return int64(float64(bet) * slotSymbols[a].reward * two * 0.9)
	case b == c:
		return int64(float64(bet) * slotSymbols[b].reward * two * 0.9)
	}
	return 0
}

func (b *Bot) handleSlots(s *bot.Client, m *discord.Message, userID, arg string) {
	b.kudMu.Lock()
	defer b.kudMu.Unlock()
	if wait, on := b.onCooldown("slots", userID); on {
		b.kudosText(s, m, fmt.Sprintf("🕙 You can spin again in %s.", wait))
		return
	}
	bet, ok := b.parseBet(s, m, userID, arg, "slots")
	if !ok {
		return
	}
	b.setCooldown("slots", userID, slotsCooldown)
	var grid [3][3]int
	for row := range grid {
		for col := range grid[row] {
			grid[row][col] = rand.IntN(len(slotSymbols))
		}
	}
	winnings := slotWinnings(grid[1], bet)
	balance, failure := b.payKudos("slots:"+m.ID.String(), userID, winnings)
	display := fmt.Sprintf("⏸%s⏸\n▶%s◀\n⏸%s⏸", slotRow(grid[0]), slotRow(grid[1]), slotRow(grid[2]))
	outcome, accent := "You lost **"+kudMoney(bet)+"**.", 0x232323
	if winnings > 0 {
		outcome, accent = "Payout **"+kudMoney(winnings)+"**.", 0x5dadec
	}
	body := fmt.Sprintf("### Slots\n%s\n%s Balance: **%s**.%s\n-# Payouts include the stake. Active loan repayments apply automatically.", display, outcome, kudMoney(balance), failure)
	b.kudosReply(s, m, discord.MessageCreate{
		Flags: discord.MessageFlagIsComponentsV2,
		Components: []discord.LayoutComponent{discord.ContainerComponent{
			AccentColor: accent,
			Components:  []discord.ContainerSubComponent{discord.TextDisplayComponent{Content: body}},
		}},
	})
}

func slotRow(row [3]int) string {
	return slotSymbols[row[0]].face + slotSymbols[row[1]].face + slotSymbols[row[2]].face
}

// --- Blackjack ---

type blackjackGame struct {
	userID               string
	id                   string
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

// handValue counts the best hand total; soft reports whether an ace is still counted as 11.
func handValue(cards []int) (total int, soft bool) {
	aces := 0
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
	return total, aces > 0
}

func handTotal(cards []int) int { total, _ := handValue(cards); return total }

func isBlackjack(cards []int) bool { return len(cards) == 2 && handTotal(cards) == 21 }

// dealerShouldHit draws to 17 and hits a soft 17, matching Sigma's dealer.
func dealerShouldHit(cards []int) bool {
	total, soft := handValue(cards)
	return total < 17 || (total == 17 && soft)
}

func formatCards(cards []int) string {
	parts := make([]string, len(cards))
	for index, card := range cards {
		parts[index] = []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}[card%13] + string([]rune("♠♥♦♣")[card/13])
	}
	return strings.Join(parts, " ")
}

// blackjackPayout settles a finished hand; the returned amount includes the stake that parseBet already took,
// so 0 is a loss, bet is a push, 2*bet is an even-money win and bet + bet*6/5 is a natural blackjack (Sigma's 6:5).
func blackjackPayout(player, dealer []int, bet int64) (int64, string) {
	p, d := handTotal(player), handTotal(dealer)
	switch {
	case p > 21:
		return 0, "💣 Bust, you lose."
	case isBlackjack(player) && isBlackjack(dealer):
		return bet, "🔵 Both blackjack, push."
	case isBlackjack(player):
		return bet + bet*6/5, "🎉 Blackjack! Paid 6:5."
	case isBlackjack(dealer):
		return 0, "💣 Dealer blackjack, you lose."
	case d > 21:
		return 2 * bet, "🎉 Dealer busts, you win!"
	case p > d:
		return 2 * bet, "🎉 You beat the dealer!"
	case p == d:
		return bet, "🔵 Push, you broke even."
	}
	return 0, "💣 The dealer won."
}

func (g *blackjackGame) render(done bool, footer string) string {
	dealer := formatCards(g.dealer[:1]) + " ??"
	if done {
		dealer = fmt.Sprintf("%s (%d)", formatCards(g.dealer), handTotal(g.dealer))
	}
	return fmt.Sprintf("**Blackjack** · <@%s> bet %s\nDealer: %s\nYou: %s (%d)%s",
		g.userID, kudMoney(g.bet), dealer, formatCards(g.player), handTotal(g.player), footer)
}

// finishBlackjack plays the dealer, pays out and forgets the game. Callers hold blackjackMu.
func (b *Bot) finishBlackjack(g *blackjackGame) string {
	delete(b.blackjackGames, g.userID)
	if g.timer != nil {
		g.timer.Stop()
	}
	if handTotal(g.player) <= 21 && !isBlackjack(g.player) {
		for dealerShouldHit(g.dealer) {
			g.dealer = append(g.dealer, g.draw())
		}
	}
	payout, outcome := blackjackPayout(g.player, g.dealer, g.bet)
	balance, failure := b.payKudos(g.id, g.userID, payout)
	return g.render(true, fmt.Sprintf("\n**%s** Payout **%s**. Balance: **%s**.%s\n-# Active loan repayments apply automatically.", outcome, kudMoney(payout), kudMoney(balance), failure))
}

func blackjackButtons(userID string, canDouble bool) []discord.LayoutComponent {
	buttons := []discord.InteractiveComponent{
		discord.ButtonComponent{Label: "Hit", Style: discord.ButtonStylePrimary, CustomID: blackjackComponentPrefix + "hit:" + userID},
		discord.ButtonComponent{Label: "Stand", Style: discord.ButtonStyleSecondary, CustomID: blackjackComponentPrefix + "stand:" + userID},
	}
	if canDouble {
		buttons = append(buttons, discord.ButtonComponent{Label: "Double Down", Style: discord.ButtonStyleSuccess, CustomID: blackjackComponentPrefix + "double:" + userID})
	}
	return []discord.LayoutComponent{discord.ActionRowComponent{Components: buttons}}
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
	if wait, on := b.onCooldown("bj", userID); on {
		b.blackjackMu.Unlock()
		b.kudosText(s, m, fmt.Sprintf("🕙 You can play again in %s.", wait))
		return
	}
	b.blackjackGames[userID] = &blackjackGame{userID: userID}
	b.blackjackMu.Unlock()

	bet, ok := b.parseBet(s, m, userID, arg, "bj")
	b.blackjackMu.Lock()
	defer b.blackjackMu.Unlock()
	if !ok {
		delete(b.blackjackGames, userID)
		return
	}
	b.setCooldown("bj", userID, blackjackCooldown)
	g := newBlackjackGame(userID, bet, rand.Perm(52))
	g.id = "bj:" + m.ID.String()
	b.blackjackGames[userID] = g
	if isBlackjack(g.player) || isBlackjack(g.dealer) {
		b.kudosText(s, m, b.finishBlackjack(g))
		return
	}
	msg, err := b.kudosReply(s, m, discord.MessageCreate{Content: g.render(false, ""), Components: blackjackButtons(userID, true)})
	if err != nil {
		delete(b.blackjackGames, userID)
		if err := b.DB.RefundKud(g.id); err != nil {
			b.Logger.Error("failed to refund unpublished blackjack", zap.Error(err))
		}
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
	done := action == "stand"
	switch action {
	case "hit":
		g.player = append(g.player, g.draw())
		done = handTotal(g.player) >= 21
	case "double":
		if len(g.player) != 2 {
			b.blackjackMu.Unlock()
			respondEphemeral(s, i, "You can only double down on your first turn.")
			return true
		}
		if err := b.DB.DoubleKudHold(g.id, owner); err != nil {
			b.blackjackMu.Unlock()
			respondEphemeral(s, i, "You need more "+kudEmoji+" Kud to double down, within the stake limit.")
			return true
		}
		g.bet *= 2
		g.player = append(g.player, g.draw())
		done = true
	}
	update := discord.MessageUpdate{AllowedMentions: &discord.AllowedMentions{}}
	var content string
	if done {
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
