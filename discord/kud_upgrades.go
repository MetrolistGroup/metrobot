package discord

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

// kudBlanketCooldown is the layer shared by every Kud game; kudGameCooldowns is each game's own layer.
const kudBlanketCooldown = 5 * time.Second

var kudGameCooldowns = map[string]time.Duration{
	"slots": slotsCooldown, "bj": blackjackCooldown, "mines": 10 * time.Second, "highlow": 10 * time.Second,
	"fight": 30 * time.Second, "plinko": 30 * time.Second, "crash": 30 * time.Second,
}

// kudLuck is the rescue chance in percent: 2 while in debt, plus one per Lucky charm level.
func kudLuck(debt bool, level int64) int {
	luck := int(level)
	if debt {
		luck += 2
	}
	return luck
}

// kudCooldownFor shortens a base cooldown by 5% per Swift hands level.
func (b *Bot) kudCooldownFor(userID string, base time.Duration) time.Duration {
	levels, err := b.DB.KudUpgradeLevels(userID)
	if err != nil {
		b.Logger.Warn("failed to read Kud upgrades", zap.Error(err))
		return base
	}
	return base * time.Duration(100-5*levels["swift"]) / 100
}

// checkKudRateLimit returns a reply while either the blanket or the game's own layer is cooling down.
// Callers hold kudMu or the game's own lock, and call it before reserving a stake.
func (b *Bot) checkKudRateLimit(userID, game string) error {
	if wait, on := b.onCooldown("kud", userID); on {
		return fmt.Errorf("🕙 Kud games share a short cooldown. Wait %s before your next stake.", wait)
	}
	if wait, on := b.onCooldown(game, userID); on {
		return fmt.Errorf("🕙 You can play %s again in %s.", game, wait)
	}
	return nil
}

// armKudRateLimit starts both layers. Call it only after a stake is held.
func (b *Bot) armKudRateLimit(userID, game string) {
	b.setCooldown("kud", userID, b.kudCooldownFor(userID, kudBlanketCooldown))
	b.setCooldown(game, userID, b.kudCooldownFor(userID, kudGameCooldowns[game]))
}

func (b *Bot) kudPanel(s *bot.Client, m *discord.Message, title, body string) {
	b.kudosReply(s, m, discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: kudLayout(title, body)})
}

func (b *Bot) handleKudShop(s *bot.Client, m *discord.Message, userID string) {
	levels, err := b.DB.KudUpgradeLevels(userID)
	balance, balanceErr := b.DB.Kudos(userID)
	if err != nil || balanceErr != nil {
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, "<@%s> · Balance **%s**\n", userID, kudMoney(balance))
	for _, up := range db.KudUpgrades {
		level := levels[up.Key]
		fmt.Fprintf(&body, "\n**%s** · Level %d/%d · %s\n", up.Title, level, up.Max, up.Detail)
		if level < up.Max {
			fmt.Fprintf(&body, "Next level **%s**. Buy with `>>buy %s`.\n", kudMoney(up.Base*(level+1)), up.Key)
		} else {
			body.WriteString("Maxed.\n")
		}
	}
	body.WriteString("\n-# Upgrades are permanent. Buying spends Kud from your balance.")
	b.kudPanel(s, m, kudEmoji+" Kud shop", body.String())
}

func (b *Bot) handleKudBuy(s *bot.Client, m *discord.Message, userID, arg string) {
	if arg == "" {
		b.handleKudShop(s, m, userID)
		return
	}
	key := strings.ToLower(arg)
	var title string
	for _, up := range db.KudUpgrades {
		if up.Key == key {
			title = up.Title
		}
	}
	if title == "" {
		b.kudosText(s, m, "Upgrades: `swift` or `luck`. See `>>shop`.")
		return
	}
	level, cost, err := b.DB.BuyKudUpgrade(userID, key)
	switch {
	case errors.Is(err, db.ErrKudMaxed):
		b.kudosText(s, m, title+" is already maxed.")
	case errors.Is(err, db.ErrKudFunds):
		b.kudosText(s, m, "Not enough "+kudEmoji+" Kud for the next level. Check `>>kud`.")
	case err != nil:
		b.Logger.Error("failed to buy Kud upgrade", zap.Error(err))
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
	default:
		b.kudPanel(s, m, kudEmoji+" Upgrade bought", fmt.Sprintf("<@%s> bought **%s** level %d for **%s**.\n-# See `>>shop` for what it does now.", userID, title, level, kudMoney(cost)))
	}
}
