package discord

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

// kudBlanketCooldown is the layer shared by every Kud game; kudGameCooldowns is each game's own layer.
const kudBlanketCooldown = 5 * time.Second
const kudShopPrefix = "kudshop:"

// Reject confirmations that quoted the previous per-level effects.
const kudShopConfirmAction = "confirm-v2"
const kudSwiftStep = 20
const kudLuckStep = 4

var kudGameCooldowns = map[string]time.Duration{
	"slots": slotsCooldown, "bj": blackjackCooldown, "mines": 10 * time.Second, "highlow": 10 * time.Second,
	"fight": 30 * time.Second, "plinko": 30 * time.Second, "crash": 30 * time.Second,
}

// kudLuck returns basis points so fractional percentage upgrades keep their full effect.
func kudLuck(debt bool, level int64) int {
	luck := int(level) * kudLuckStep
	if debt {
		luck += 200
	}
	return luck
}

// kudCooldownFor shortens a base cooldown by 0.2% per Swift hands level.
func (b *Bot) kudCooldownFor(userID string, base time.Duration) time.Duration {
	levels, err := b.DB.KudUpgradeLevels(userID)
	if err != nil {
		b.Logger.Warn("failed to read Kud upgrades", zap.Error(err))
		return base
	}
	return base * time.Duration(10_000-kudSwiftStep*levels["swift"]) / 10_000
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

func kudUpgradeEffect(key string, level int64) string {
	if key == "swift" {
		return fmt.Sprintf("%.1f%% shorter cooldowns", float64(level*kudSwiftStep)/100)
	}
	return fmt.Sprintf("+%.2f%% rescue chance", float64(level*kudLuckStep)/100)
}

func (b *Bot) kudShopComponents(userID, selected, notice string) ([]discord.LayoutComponent, error) {
	levels, err := b.DB.KudUpgradeLevels(userID)
	if err != nil {
		return nil, err
	}
	balance, err := b.DB.Kudos(userID)
	if err != nil {
		return nil, err
	}
	if up, ok := db.KudUpgradeByKey(selected); ok {
		level := levels[up.Key]
		cost := up.Cost(level + 1)
		switch {
		case level >= up.Max:
			notice = up.Title + " is already maxed."
		case balance < cost:
			notice = "You need " + kudMoney(cost-balance) + " more for " + up.Title + "."
		default:
			body := fmt.Sprintf("### Confirm upgrade\n<@%s> · %s\n\n**%s** · Level %d → %d\n%s → **%s**\n\nPrice **%s**\nBalance after purchase **%s**\n\nThis spends Kud permanently. Confirm to buy this level.",
				userID, kudMoney(balance), up.Title, level, level+1, kudUpgradeEffect(up.Key, level), kudUpgradeEffect(up.Key, level+1), kudMoney(cost), kudMoney(balance-cost))
			confirmID := fmt.Sprintf("%s%s:%s:%s:%d:%d:%d", kudShopPrefix, userID, kudShopConfirmAction, up.Key, level+1, cost, time.Now().Add(5*time.Minute).Unix())
			return []discord.LayoutComponent{discord.ContainerComponent{Components: []discord.ContainerSubComponent{
				discord.TextDisplayComponent{Content: body},
				discord.NewActionRow(
					discord.NewSuccessButton("Confirm purchase", confirmID),
					discord.NewSecondaryButton("Cancel", kudShopPrefix+userID+":cancel"),
				),
			}}}, nil
		}
	}
	header := fmt.Sprintf("### Kud shop\n<@%s> · Balance **%s**", userID, kudMoney(balance))
	if notice != "" {
		header += "\n\n" + notice
	}
	components := []discord.ContainerSubComponent{discord.TextDisplayComponent{Content: header}}
	for _, up := range db.KudUpgrades {
		level := levels[up.Key]
		body := fmt.Sprintf("**%s** · Level %d / %d\n%s\n%s", up.Title, level, up.Max, kudUpgradeEffect(up.Key, level), up.Detail)
		label := "Upgrade " + up.Title
		disabled := level >= up.Max
		if disabled {
			label = "Max level"
		} else {
			cost := up.Cost(level + 1)
			body += fmt.Sprintf("\nNext **%s** · %s", kudMoney(cost), kudUpgradeEffect(up.Key, level+1))
			disabled = balance < cost
			if disabled {
				body += "\nNeed " + kudMoney(cost-balance) + " more."
			}
		}
		button := discord.NewSecondaryButton(label, kudShopPrefix+userID+":select:"+up.Key)
		button.Disabled = disabled
		components = append(components, discord.SeparatorComponent{}, discord.SectionComponent{
			Components: []discord.SectionSubComponent{discord.TextDisplayComponent{Content: body}},
			Accessory:  button,
		})
	}
	components = append(components, discord.SeparatorComponent{},
		discord.TextDisplayComponent{Content: "Select an upgrade to review and confirm the purchase. Lucky charm stacks with the 2% loan rescue chance."},
		discord.NewActionRow(discord.NewSecondaryButton("Refresh balance", kudShopPrefix+userID+":view")))
	return []discord.LayoutComponent{discord.ContainerComponent{Components: components}}, nil
}

func (b *Bot) handleKudShop(s *bot.Client, m *discord.Message, userID string) {
	b.handleKudBuy(s, m, userID, "")
}

func (b *Bot) handleKudBuy(s *bot.Client, m *discord.Message, userID, arg string) {
	key := strings.ToLower(strings.TrimSpace(arg))
	if _, ok := db.KudUpgradeByKey(key); key != "" && !ok {
		b.kudosText(s, m, "Upgrades: `swift` or `luck`. See `>>shop`.")
		return
	}
	components, err := b.kudShopComponents(userID, key, "")
	if err != nil {
		b.Logger.Error("failed to load Kud shop", zap.Error(err))
		b.kudosText(s, m, "The Kud bank is down. Try again in a bit.")
		return
	}
	b.kudosReply(s, m, discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: components})
}

func (b *Bot) handleKudShopComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	customID, found := strings.CutPrefix(i.Data.CustomID(), kudShopPrefix)
	if !found {
		return false
	}
	parts := strings.Split(customID, ":")
	if len(parts) < 2 || parts[0] != i.User().ID.String() || i.User().Bot {
		respondEphemeral(s, i, "Open your own shop with `>>shop`.")
		return true
	}
	userID, action := parts[0], parts[1]
	var up db.KudUpgrade
	var level, cost, expires int64
	valid := (action == "view" || action == "cancel") && len(parts) == 2
	if (action == "select" && len(parts) == 3) || (action == kudShopConfirmAction && len(parts) == 6) {
		up, valid = db.KudUpgradeByKey(parts[2])
		if valid && action == kudShopConfirmAction {
			var levelErr, costErr, expiryErr error
			level, levelErr = strconv.ParseInt(parts[3], 10, 64)
			cost, costErr = strconv.ParseInt(parts[4], 10, 64)
			expires, expiryErr = strconv.ParseInt(parts[5], 10, 64)
			valid = levelErr == nil && costErr == nil && expiryErr == nil && level > 0 && level <= up.Max && cost > 0
		}
	}
	if !valid {
		respondEphemeral(s, i, "This shop control is unavailable. Open `>>shop` again.")
		return true
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{Type: discord.InteractionResponseTypeDeferredUpdateMessage}); err != nil {
		b.Logger.Warn("failed to acknowledge Kud shop button", zap.Error(err))
		return true
	}
	var selected, notice string
	switch action {
	case "select":
		selected = up.Key
	case "cancel":
		notice = "Purchase cancelled. No Kud spent."
	case kudShopConfirmAction:
		if !time.Now().Before(time.Unix(expires, 0)) {
			notice = "This confirmation expired. Select the upgrade again. No Kud spent."
			break
		}
		_, _, err := b.DB.BuyKudUpgrade(userID, up.Key, level, cost)
		switch {
		case errors.Is(err, db.ErrKudQuoteChanged):
			notice = "Your upgrade level or price changed. Select the upgrade again. No Kud spent."
		case errors.Is(err, db.ErrKudMaxed):
			notice = up.Title + " is already maxed. No Kud spent."
		case errors.Is(err, db.ErrKudFunds):
			notice = "You no longer have enough Kud. No Kud spent."
		case err != nil:
			b.Logger.Error("failed to buy Kud upgrade", zap.Error(err))
			notice = "The purchase could not be completed. Refresh the shop to check your level and balance."
		default:
			notice = fmt.Sprintf("Bought **%s level %d** for **%s**. You now have %s.", up.Title, level, kudMoney(cost), kudUpgradeEffect(up.Key, level))
		}
	}
	components, err := b.kudShopComponents(userID, selected, notice)
	if err != nil {
		b.Logger.Error("failed to refresh Kud shop", zap.Error(err))
		components = kudLayout("Kud shop", notice+"\nCouldn't load your balance. Open `>>shop` again.")
	}
	if _, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Components: &components, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
		b.Logger.Warn("failed to update Kud shop", zap.Error(err))
		if notice == "" {
			notice = "Couldn't refresh the shop. Open `>>shop` again."
		}
		if _, sendErr := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{Content: notice, Flags: discord.MessageFlagEphemeral, AllowedMentions: &discord.AllowedMentions{}}); sendErr != nil {
			b.Logger.Warn("failed to send Kud purchase result", zap.Error(sendErr))
		}
	}
	return true
}
