package discord

import (
	"bytes"
	"fmt"
	"math"
	"math/bits"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/kudplinko"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

const kudEmoji = "⚜️"
const kudIdle = 5 * time.Minute
const plinkoBallCost int64 = 50

type kudEntrant struct {
	userID, name string
}

type kudGame struct {
	id, kind, owner, lender string
	channelID, messageID    snowflake.ID
	bet                     int64
	expires                 time.Time
	timer                   *time.Timer
	mines                   int
	board, revealed         [16]bool
	picks, card, streak     int
	multiplier              float64
	luck                    int
	rescued                 bool
	entrants                []kudEntrant
	result                  string
	done                    bool
	version                 int
	settlement              map[string]int64
	settlementText          string
	plinkoBalls             []kudplinko.Ball
	plinkoPayouts           map[string]int64
	crash                   *kudCrash
}

func kudMoney(n int64) string { return fmt.Sprintf("%s %d Kud", kudEmoji, n) }

func kudCard(rank int) string {
	switch rank {
	case 11:
		return "J♠"
	case 12:
		return "Q♠"
	case 13:
		return "K♠"
	case 14:
		return "A♠"
	default:
		return fmt.Sprintf("%d♠", rank)
	}
}

func kudLayout(title, body string, rows ...discord.ContainerSubComponent) []discord.LayoutComponent {
	components := []discord.ContainerSubComponent{discord.TextDisplayComponent{Content: "### " + title + "\n" + body}}
	components = append(components, rows...)
	return []discord.LayoutComponent{discord.ContainerComponent{AccentColor: 0xd4af65, Components: components}}
}

func (b *Bot) handleKudAccount(s *bot.Client, m *discord.Message) {
	a, err := b.DB.KudAccount(m.Author.ID.String())
	if err != nil {
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
		return
	}
	rate := 0.0
	if a.Played > 0 {
		rate = 100 * float64(a.Wins) / float64(a.Played)
	}
	body := fmt.Sprintf("<@%s>\n**Balance** %s · **Held in games** %s\n**Games** %d · **Wins** %d · **Draws** %d · **Win rate** %.1f%%\n**Wagered** %s · **Payouts** %s · **Net** %s\n**Debt** %s · **Lent out** %s",
		m.Author.ID, kudMoney(a.Balance), kudMoney(a.Held), a.Played, a.Wins, a.Draws, rate, kudMoney(a.Wagered), kudMoney(a.Paid), kudMoney(a.Paid-a.Wagered), kudMoney(a.Debt), kudMoney(a.Lent))
	if a.Debt > 0 {
		body += fmt.Sprintf("\nOwed to <@%s>. 20%% of incoming rewards repay it automatically.\n-# Active debt gives Mines and High-Low a 2%% chance to rescue one losing pick per round. No fight advantage.", a.Lender)
	}
	body += "\n-# Stats count settled games since this update. A win means payout above the stake. `>>daily` claims Kud."
	b.kudosReply(s, m, discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: kudLayout(kudEmoji+" Kud account", body)})
}

func (b *Bot) kudBet(userID, arg string) (int64, error) {
	if arg == "" {
		return defaultBet, nil
	}
	if strings.EqualFold(arg, "all") || strings.EqualFold(arg, "max") {
		balance, err := b.DB.Kudos(userID)
		if err != nil {
			return 0, err
		}
		if balance <= 0 {
			return 0, fmt.Errorf("you have %s. Grab some with `>>daily`", kudMoney(0))
		}
		if balance > db.MaxKudBet {
			balance = db.MaxKudBet
		}
		return balance, nil
	}
	bet, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || bet < 1 || bet > db.MaxKudBet {
		return 0, fmt.Errorf("bet between 1 and %d Kud", db.MaxKudBet)
	}
	return bet, nil
}

func (b *Bot) handleKudGame(s *bot.Client, m *discord.Message, kind, args string) {
	fields := strings.Fields(args)
	userID := m.Author.ID.String()
	g := &kudGame{id: kind + ":" + m.ID.String(), kind: kind, owner: userID, channelID: m.ChannelID, multiplier: 1, mines: 3, card: rand.IntN(13) + 2}
	betArg := ""
	if len(fields) > 0 {
		betArg = fields[0]
	}
	if kind == "plinko" && betArg == "" {
		betArg = strconv.FormatInt(plinkoBallCost, 10)
	}
	if kind == "loan" {
		if len(fields) != 2 || len(m.Mentions) != 1 || strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(fields[0], "<@"), "!"), ">") != m.Mentions[0].ID.String() || m.Mentions[0].Bot || m.Mentions[0].ID == m.Author.ID {
			b.kudosText(s, m, "Usage: `>>loan @lender <amount>`. Choose another person.")
			return
		}
		g.lender, betArg = m.Mentions[0].ID.String(), fields[1]
	} else if len(fields) > 1 {
		if kind != "mines" || len(fields) != 2 {
			b.kudosText(s, m, "Usage: `>>"+kind+" [bet]`.")
			return
		}
		var err error
		g.mines, err = strconv.Atoi(fields[1])
		if err != nil || g.mines < 1 || g.mines > 10 {
			b.kudosText(s, m, "Pick between 1 and 10 mines. Usage: `>>mines [bet] [1..10]`.")
			return
		}
	}
	var err error
	if g.bet, err = b.kudBet(userID, betArg); err != nil {
		b.kudosText(s, m, err.Error())
		return
	}
	if kind == "plinko" {
		if strings.EqualFold(betArg, "all") || strings.EqualFold(betArg, "max") {
			g.bet -= g.bet % plinkoBallCost
		}
		if g.bet < plinkoBallCost || g.bet%plinkoBallCost != 0 {
			b.kudosText(s, m, "Each Plinko ball costs "+kudMoney(plinkoBallCost)+". Choose a multiple of 50, e.g. `>>plinko 500` for 10 balls.")
			return
		}
	}
	b.kudMu.Lock()
	defer b.kudMu.Unlock()
	if b.kudGames == nil {
		b.kudGames = make(map[string]*kudGame)
	}
	if len(b.kudGames) >= 100 {
		b.kudosText(s, m, "Too many rounds are open. Try again after a round ends.")
		return
	}
	for _, current := range b.kudGames {
		if current.owner == userID && current.kind == kind {
			b.kudosText(s, m, "Finish your current "+kind+" round first.")
			return
		}
		if (kind == "plinko" || kind == "crash") && current.kind == kind && current.channelID == m.ChannelID {
			b.kudosText(s, m, "A "+kind+" round is already open in this channel. Join that one.")
			return
		}
	}
	if kind != "loan" {
		if err := b.checkKudRateLimit(userID, kind); err != nil {
			b.kudosText(s, m, err.Error())
			return
		}
	}
	a, err := b.DB.KudAccount(userID)
	if err != nil {
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
		return
	}
	levels, err := b.DB.KudUpgradeLevels(userID)
	if err != nil {
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
		return
	}
	g.luck = kudLuck(a.Debt > 0, levels["luck"])
	if kind == "loan" && a.Debt > 0 {
		b.kudosText(s, m, "Repay your current loan before requesting another.")
		return
	}
	if kind == "crash" {
		g.crash = newKudCrash(userID)
	}
	if kind != "loan" {
		if err := b.DB.HoldKud(g.escrowID(userID), kind, userID, g.bet); err != nil {
			b.kudosText(s, m, "Couldn't reserve "+kudMoney(g.bet)+". Check `>>kud` and try again.")
			return
		}
		b.armKudRateLimit(userID, kind)
	}
	g.entrants = []kudEntrant{{userID: userID, name: discordMemberDisplayName(&discord.Member{User: m.Author, Nick: memberNick(m.Member)})}}
	g.expires = time.Now().Add(kudIdle)
	if kind == "plinko" {
		g.expires = time.Now().Add(30 * time.Second)
	}
	if kind == "crash" {
		g.expires = time.Now().Add(crashLobby)
	}
	msg, err := b.kudosReply(s, m, discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: g.render()})
	if err != nil {
		if kind != "loan" {
			if refundErr := b.DB.RefundKud(g.escrowID(userID)); refundErr != nil {
				b.Logger.Error("failed to refund unpublished Kud round", zap.Error(refundErr))
			}
		}
		return
	}
	g.messageID = msg.ID
	b.kudGames[g.id] = g
	g.timer = time.AfterFunc(time.Until(g.expires), func() { b.expireKudGame(s, g) })
}

func memberNick(member *discord.Member) *string {
	if member == nil {
		return nil
	}
	return member.Nick
}

func (g *kudGame) button(action, label string, style discord.ButtonStyle, disabled bool) discord.ButtonComponent {
	return discord.ButtonComponent{CustomID: fmt.Sprintf("kud:%s:%d:%s", g.id, g.version, action), Label: label, Style: style, Disabled: disabled || g.settlement != nil}
}

func (g *kudGame) payout() int64 { return int64(math.Floor(float64(g.bet) * g.multiplier)) }

func (g *kudGame) render() []discord.LayoutComponent {
	if g.kind == "crash" {
		return g.renderCrash()
	}
	title := map[string]string{"mines": "Mines", "highlow": "High-Low", "fight": "Fight", "plinko": "Plinko", "loan": "Loan request"}[g.kind]
	body := fmt.Sprintf("<@%s> · Stake **%s**", g.owner, kudMoney(g.bet))
	var rows []discord.ContainerSubComponent
	switch g.kind {
	case "mines":
		body += fmt.Sprintf("\nMines **%d** · Safe picks **%d/%d** · **%.2fx**\n", g.mines, g.picks, 16-g.mines, g.multiplier)
		if g.picks == 0 && !g.done {
			body += "Pick any tile to start. Your first pick is guaranteed safe and returns your stake."
		} else if !g.done {
			body += "Cash out for **" + kudMoney(g.payout()) + "**, or pick another tile."
		}
		for row := range 4 {
			buttons := make([]discord.InteractiveComponent, 0, 4)
			for col := range 4 {
				index := row*4 + col
				label, style := fmt.Sprintf("%c%d", 'A'+row, col+1), discord.ButtonStyleSecondary
				if g.revealed[index] || g.done {
					if g.board[index] {
						label, style = "💣", discord.ButtonStyleDanger
					} else {
						label, style = "💎", discord.ButtonStyleSuccess
					}
				}
				buttons = append(buttons, g.button("tile"+strconv.Itoa(index), label, style, g.done || g.revealed[index]))
			}
			rows = append(rows, discord.NewActionRow(buttons...))
		}
		if !g.done {
			rows = append(rows, discord.NewActionRow(g.button("cash", "Cash out · "+kudMoney(g.payout()), discord.ButtonStyleSuccess, g.picks == 0)))
		}
	case "highlow":
		body += fmt.Sprintf("\nCurrent card **%s** · Streak **%d** · **%.2fx**\n-# Ace is high. Ties push and keep your streak. Cards are drawn independently.", kudCard(g.card), g.streak, g.multiplier)
		if !g.done {
			higher, lower := highlowFactor(g.card, true), highlowFactor(g.card, false)
			rows = append(rows, discord.NewActionRow(
				g.button("higher", fmt.Sprintf("Higher · %.2fx", higher), discord.ButtonStylePrimary, g.card == 14),
				g.button("lower", fmt.Sprintf("Lower · %.2fx", lower), discord.ButtonStylePrimary, g.card == 2),
				g.button("cash", "Cash out · "+kudMoney(g.payout()), discord.ButtonStyleSuccess, g.streak == 0)))
		}
	case "fight":
		body += fmt.Sprintf("\nPot **%s** · Odds **50/50**\nAnother player can match the stake. Winner takes both stakes.\n-# Unmatched fights refund on expiry <t:%d:R>. No loan luck applies.", kudMoney(g.bet*2), g.expires.Unix())
		if !g.done {
			rows = append(rows, discord.NewActionRow(g.button("join", "Join fight · "+kudMoney(g.bet), discord.ButtonStylePrimary, false), g.button("cancel", "Cancel", discord.ButtonStyleSecondary, false)))
		}
	case "plinko":
		body += fmt.Sprintf("\n**%d balls each** · **%d balls total** · Players **%d/32**\nDrops <t:%d:R>\n", g.bet/plinkoBallCost, int64(len(g.entrants))*(g.bet/plinkoBallCost), len(g.entrants), g.expires.Unix())
		if !g.done {
			for index, entrant := range g.entrants {
				if index > 0 {
					body += ", "
				}
				body += "<@" + entrant.userID + ">"
			}
		}
		body += "\n-# Each 50 Kud buys one independent ball. Join once at the same stake. Slight bounce momentum favors the outer bins; each 25x edge is about 1 in 1121 per ball. No loan luck applies."
		if !g.done {
			rows = append(rows, discord.NewActionRow(g.button("join", fmt.Sprintf("Join · %d balls · %s", g.bet/plinkoBallCost, kudMoney(g.bet)), discord.ButtonStylePrimary, len(g.entrants) >= 32)))
		}
	case "loan":
		body = fmt.Sprintf("<@%s> asked <@%s> for **%s**.\n20%% of incoming rewards, including daily claims and game payouts, go to the lender until repaid. Refunds, new loans, and repayments received are excluded. No interest or fixed repayment date.\n-# Debt gives Mines and High-Low a 2%% chance to rescue one losing pick per round. Lending risks delayed repayment if the borrower stops playing. Expires <t:%d:R>.", g.owner, g.lender, kudMoney(g.bet), g.expires.Unix())
		if !g.done {
			rows = append(rows, discord.NewActionRow(g.button("fund", "Fund · "+kudMoney(g.bet), discord.ButtonStyleSuccess, false), g.button("cancel", "Decline / cancel", discord.ButtonStyleSecondary, false)))
		}
	}
	if g.luck > 0 && (g.kind == "mines" || g.kind == "highlow") {
		body += fmt.Sprintf("\n-# Luck: %d%% chance to rescue one losing pick this round.", g.luck)
	}
	if g.result != "" {
		body += "\n\n" + g.result
	}
	return kudLayout(title, body, rows...)
}

func highlowFactor(card int, higher bool) float64 {
	wins := card - 2
	if higher {
		wins = 14 - card
	}
	if wins <= 0 {
		return 0
	}
	// Ties leave the stake unchanged; a correct call pays according to non-tie odds.
	return 0.97 * 12 / float64(wins)
}

func (g *kudGame) pickMine(index int) bool {
	if g.picks == 0 {
		cells := rand.Perm(15)
		for _, cell := range cells[:g.mines] {
			if cell >= index {
				cell++
			}
			g.board[cell] = true
		}
	}
	if g.board[index] && !g.rescued && rand.IntN(100) < g.luck {
		for cell := range 16 {
			if cell != index && !g.revealed[cell] && !g.board[cell] {
				g.board[cell], g.board[index], g.rescued = true, false, true
				break
			}
		}
	}
	g.revealed[index] = true
	if g.board[index] {
		return false
	}
	g.picks++
	if g.picks > 1 {
		g.multiplier = 0.97
		for pick := 1; pick < g.picks; pick++ {
			g.multiplier *= float64(16-pick) / float64(16-g.mines-pick)
		}
	}
	return true
}

func (b *Bot) settleSoloKud(g *kudGame, payout int64, result string) error {
	return b.settleKudGame(g, map[string]int64{g.owner: payout}, result)
}

func (b *Bot) settleKudGame(g *kudGame, payouts map[string]int64, result string) error {
	g.settlement, g.settlementText = payouts, result
	balances, err := b.DB.SettleKud(g.id, payouts)
	if err != nil {
		b.Logger.Error("failed to settle Kud game", zap.String("game", g.id), zap.Error(err))
		g.result = "Settlement delayed. The outcome is fixed and your stake remains held. Retrying shortly."
		g.expires = time.Now().Add(15 * time.Second)
		g.timer.Reset(15 * time.Second)
		return fmt.Errorf("settlement delayed. The outcome is fixed; retrying shortly")
	}
	g.done = true
	g.result = result
	for _, entrant := range g.entrants {
		g.result += fmt.Sprintf("\n<@%s> balance **%s**.", entrant.userID, kudMoney(balances[entrant.userID]))
	}
	g.result += "\n-# Payouts include the stake. Active loan repayments apply automatically."
	delete(b.kudGames, g.id)
	g.timer.Stop()
	return nil
}

func (b *Bot) handleKudComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	if input := b.queueCrashInput(i); input != nil {
		b.handleCrashComponent(s, i, input)
		return true
	}
	customID, found := strings.CutPrefix(i.Data.CustomID(), "kud:")
	if !found {
		return false
	}
	parts := strings.Split(customID, ":")
	if len(parts) != 4 {
		respondEphemeral(s, i, "This round is already over.")
		return true
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeDeferredUpdateMessage}); err != nil {
		return true
	}
	b.kudMu.Lock()
	defer b.kudMu.Unlock()
	errText := func(message string) {
		_, err := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{Content: message, Flags: discord.MessageFlagEphemeral, AllowedMentions: &discord.AllowedMentions{}})
		if err != nil {
			b.Logger.Warn("failed to send Kud interaction error", zap.Error(err))
		}
	}
	g := b.kudGames[parts[0]+":"+parts[1]]
	if g == nil || g.messageID != i.Message.ID || g.channelID != i.Channel().ID() || !time.Now().Before(g.expires) {
		errText("This round is already over.")
		return true
	}
	version, err := strconv.Atoi(parts[2])
	if err != nil || version != g.version {
		components := g.render()
		if _, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
			b.Logger.Warn("failed to refresh Kud board", zap.Error(err))
		}
		errText("The board changed. Refreshed it; choose again.")
		return true
	}
	action, userID := parts[3], i.User().ID.String()
	if i.User().Bot {
		errText("Bots cannot join Kud games.")
		return true
	}
	if (g.kind == "mines" || g.kind == "highlow") && userID != g.owner {
		errText("This is not your round. Start one with `>>" + g.kind + " [bet]`.")
		return true
	}
	err = b.playKudAction(g, action, userID, i.User().Username)
	if err != nil {
		b.Logger.Warn("Kud action failed", zap.String("game", g.id), zap.Error(err))
		errText(err.Error())
		return true
	}
	// A fixed lobby deadline prevents late joins; solo rounds reset their idle timer.
	if !g.done && (g.kind == "mines" || g.kind == "highlow") {
		g.expires = time.Now().Add(kudIdle)
		g.timer.Reset(kudIdle)
	}
	if g.kind == "mines" || g.kind == "highlow" {
		g.version++
	}
	components := g.render()
	if _, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
		b.Logger.Warn("failed to update Kud round", zap.Error(err))
	}
	return true
}

func (b *Bot) playKudAction(g *kudGame, action, userID, name string) error {
	if g.kind == "crash" {
		return b.playCrashAction(g, action, userID, name, time.Now())
	}
	if g.settlement != nil {
		return fmt.Errorf("this round is waiting for settlement. The outcome is fixed")
	}
	switch g.kind {
	case "mines":
		if action == "cash" && g.picks > 0 {
			return b.settleSoloKud(g, g.payout(), fmt.Sprintf("Cashed out at **%.2fx** for **%s**.", g.multiplier, kudMoney(g.payout())))
		}
		index, err := strconv.Atoi(strings.TrimPrefix(action, "tile"))
		if !strings.HasPrefix(action, "tile") || err != nil || index < 0 || index >= 16 || g.revealed[index] {
			return fmt.Errorf("pick an unopened tile")
		}
		if !g.pickMine(index) {
			return b.settleSoloKud(g, 0, "Hit a mine. Lost **"+kudMoney(g.bet)+"**.")
		}
		if g.picks == 16-g.mines {
			return b.settleSoloKud(g, g.payout(), "Cleared every safe tile for **"+kudMoney(g.payout())+"**.")
		}
	case "highlow":
		if action == "cash" && g.streak > 0 {
			return b.settleSoloKud(g, g.payout(), fmt.Sprintf("Cashed out on a **%d-card** streak for **%s**.", g.streak, kudMoney(g.payout())))
		}
		if action != "higher" && action != "lower" {
			return fmt.Errorf("choose Higher or Lower")
		}
		higher := action == "higher"
		factor := highlowFactor(g.card, higher)
		if factor == 0 {
			return fmt.Errorf("that call cannot win. Choose the other direction")
		}
		previous, next := g.card, rand.IntN(13)+2
		correct := (higher && next > previous) || (!higher && next < previous)
		if next != previous && !correct && !g.rescued && rand.IntN(100) < g.luck {
			if higher {
				next = previous + 1 + rand.IntN(14-previous)
			} else {
				next = 2 + rand.IntN(previous-2)
			}
			correct, g.rescued = true, true
		}
		g.card = next
		if next == previous {
			g.result = "Same rank. Your streak stays unchanged."
			return nil
		}
		if !correct {
			return b.settleSoloKud(g, 0, fmt.Sprintf("Called %s on **%s**, drew **%s**. Lost **%s**.", action, kudCard(previous), kudCard(next), kudMoney(g.bet)))
		}
		g.multiplier = math.Min(100_000, g.multiplier*factor)
		g.streak++
		g.result = fmt.Sprintf("Correct. **%s** to **%s**.", kudCard(previous), kudCard(next))
		if g.multiplier >= 100_000 || g.streak >= 50 {
			return b.settleSoloKud(g, g.payout(), "Reached the round limit. Cashed out **"+kudMoney(g.payout())+"**.")
		}
	case "fight", "plinko":
		if action == "cancel" && g.kind == "fight" && userID == g.owner {
			if err := b.DB.RefundKud(g.id); err != nil {
				return err
			}
			g.result, g.done = "Cancelled. Refunded **"+kudMoney(g.bet)+"**.", true
			delete(b.kudGames, g.id)
			g.timer.Stop()
			return nil
		}
		if action != "join" {
			return fmt.Errorf("only the host can cancel a fight")
		}
		if g.kind == "fight" && len(g.entrants) != 1 {
			return fmt.Errorf("someone already took this fight")
		}
		for _, entrant := range g.entrants {
			if entrant.userID == userID {
				return fmt.Errorf("you are already in this round")
			}
		}
		if len(g.entrants) >= 32 {
			return fmt.Errorf("this Plinko lobby is full")
		}
		if err := b.checkKudRateLimit(userID, g.kind); err != nil {
			return err
		}
		if err := b.DB.HoldKud(g.id, g.kind, userID, g.bet); err != nil {
			return fmt.Errorf("couldn't reserve %s. Check `>>kud`", kudMoney(g.bet))
		}
		b.armKudRateLimit(userID, g.kind)
		g.entrants = append(g.entrants, kudEntrant{userID: userID, name: name})
		if g.kind == "fight" {
			winner := g.entrants[rand.IntN(2)].userID
			payouts := map[string]int64{g.owner: 0, userID: 0}
			payouts[winner] = g.bet * 2
			return b.settleKudGame(g, payouts, fmt.Sprintf("<@%s> won **%s**.", winner, kudMoney(g.bet*2)))
		}
	case "loan":
		if action == "cancel" && (userID == g.owner || userID == g.lender) {
			g.done, g.result = true, "Loan request closed. No Kud moved."
		} else if action == "fund" && userID == g.lender {
			if err := b.DB.FundKudLoan(g.lender, g.owner, g.bet); err != nil {
				return err
			}
			g.done, g.result = true, "Loan funded. Transferred **"+kudMoney(g.bet)+"** to <@"+g.owner+">."
		} else {
			return fmt.Errorf("only the named lender can fund this loan; the lender or borrower can close it")
		}
		delete(b.kudGames, g.id)
		g.timer.Stop()
	default:
		return fmt.Errorf("unknown game")
	}
	return nil
}

func (b *Bot) expireKudGame(s *bot.Client, g *kudGame) {
	if g.kind == "crash" {
		b.tickCrash(s, g)
		return
	}
	b.kudMu.Lock()
	defer b.kudMu.Unlock()
	if b.kudGames[g.id] != g || g.done {
		return
	}
	if delay := time.Until(g.expires); delay > 0 {
		g.timer.Reset(delay)
		return
	}
	if g.kind == "plinko" {
		b.finishPlinko(s, g)
		return
	}
	var err error
	if g.settlement != nil {
		err = b.settleKudGame(g, g.settlement, g.settlementText)
	} else if (g.kind == "mines" && g.picks > 0) || (g.kind == "highlow" && g.streak > 0) {
		err = b.settleSoloKud(g, g.payout(), "Timed out. Auto-cashed out **"+kudMoney(g.payout())+"**.")
	} else {
		if g.kind != "loan" {
			err = b.DB.RefundKud(g.id)
		}
		if err == nil {
			g.done, g.result = true, "Expired. Any held stake was refunded."
			delete(b.kudGames, g.id)
		}
	}
	if err != nil {
		b.Logger.Error("failed to settle expired Kud round", zap.Error(err))
		g.timer.Reset(time.Minute)
		return
	}
	components := g.render()
	if _, err := s.Rest.UpdateMessage(g.channelID, g.messageID, discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
		b.Logger.Warn("failed to close expired Kud round", zap.Error(err))
	}
}

func (g *kudGame) rollPlinko(draw func() uint64) {
	if g.plinkoPayouts != nil {
		return
	}
	g.plinkoPayouts = make(map[string]int64, len(g.entrants))
	perPlayer := g.bet / plinkoBallCost
	var prizes [kudplinko.Bins]int64
	for bin, factor := range kudplinko.Payouts {
		prizes[bin] = int64(math.Round(float64(plinkoBallCost) * factor))
	}
	for index, entrant := range g.entrants {
		visible := int64((kudplinko.MaxBalls + len(g.entrants) - 1 - index) / len(g.entrants))
		for ball := int64(0); ball < perPlayer; ball++ {
			path := kudplinko.Path(draw())
			bin := bits.OnesCount16(path)
			g.plinkoPayouts[entrant.userID] += prizes[bin]
			if ball < visible {
				steps := make([]bool, kudplinko.Rows)
				for row := range steps {
					steps[row] = path&(1<<row) != 0
				}
				g.plinkoBalls = append(g.plinkoBalls, kudplinko.Ball{Name: fmt.Sprintf("%s #%d", entrant.name, ball+1), Steps: steps, Multiplier: kudplinko.Payouts[bin]})
			}
		}
	}
}

func (b *Bot) finishPlinko(s *bot.Client, g *kudGame) {
	g.rollPlinko(rand.Uint64)
	balls, payouts := g.plinkoBalls, g.plinkoPayouts
	gif, err := kudplinko.Render(balls)
	if err != nil {
		b.Logger.Error("failed to render Plinko", zap.Error(err))
		if refundErr := b.DB.RefundKud(g.id); refundErr != nil {
			g.timer.Reset(time.Minute)
			return
		}
		g.result = "The drop could not render. All stakes were refunded."
	} else {
		balances, settleErr := b.DB.SettleKud(g.id, payouts)
		if settleErr != nil {
			b.Logger.Error("failed to settle Plinko", zap.Error(settleErr))
			g.timer.Reset(time.Minute)
			return
		}
		g.result = "**Results**\n"
		for _, entrant := range g.entrants {
			g.result += fmt.Sprintf("<@%s> · %s · balance %s\n", entrant.userID, kudMoney(payouts[entrant.userID]), kudMoney(balances[entrant.userID]))
		}
		g.result += fmt.Sprintf("-# Showing %d of %d balls. Payouts sum every ball's result.\n", len(balls), int64(len(g.entrants))*(g.bet/plinkoBallCost))
		g.result += "-# Payouts include the stake. Active loan repayments apply automatically."
	}
	g.done = true
	delete(b.kudGames, g.id)
	components := g.render()
	update := discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}
	if err == nil {
		components = append(components, discord.MediaGalleryComponent{Items: []discord.MediaGalleryItem{{Media: discord.UnfurledMediaItem{URL: "attachment://plinko.gif"}}}})
		update.Files = []*discord.File{{Name: "plinko.gif", Reader: bytes.NewReader(gif)}}
	}
	if _, err := s.Rest.UpdateMessage(g.channelID, g.messageID, update); err != nil {
		b.Logger.Error("failed to publish Plinko results", zap.Error(err))
	}
}
