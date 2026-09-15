package discord

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	quoteWidth      = 1200
	quoteHeight     = 630
	quoteTextWidth  = 570
	quoteAvatarSize = 660
	quoteMaxAvatar  = 8 << 20
)

var quoteFont, _ = opentype.Parse(goregular.TTF)

func isQuoteTrigger(content string) bool {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "ogc", "garmin clip that", "ok garmin video speichern", "garmin quote":
		return true
	default:
		return false
	}
}

func (b *Bot) handleQuoteReply(s *discordgo.Session, command *discordgo.MessageCreate) {
	target := command.ReferencedMessage
	if target == nil && command.MessageReference != nil && command.MessageReference.MessageID != "" {
		target, _ = s.ChannelMessage(command.ChannelID, command.MessageReference.MessageID)
	}
	if target == nil {
		sendReply(s, command.ChannelID, command.ID, "Reply to a message to quote it.", false, b.Logger)
		return
	}
	if target.GuildID == "" {
		target.GuildID = command.GuildID
	}

	data, err := b.makeQuoteImage(s, target)
	if err != nil {
		b.Logger.Error("failed to make quote", zap.String("message", target.ID), zap.Error(err))
		sendReply(s, command.ChannelID, command.ID, "I couldn't quote that message.", false, b.Logger)
		return
	}
	_, err = s.ChannelMessageSendComplex(command.ChannelID, &discordgo.MessageSend{
		Files:           []*discordgo.File{{Name: "quote.png", ContentType: "image/png", Reader: bytes.NewReader(data)}},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Reference:       &discordgo.MessageReference{MessageID: target.ID},
	})
	if err != nil {
		b.Logger.Error("failed to send quote", zap.String("message", target.ID), zap.Error(err))
	}
}

func (b *Bot) handleQuoteInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := deferResponse(s, i, false); err != nil {
		b.Logger.Error("failed to defer quote response", zap.Error(err))
		return
	}

	messages, err := s.ChannelMessages(i.ChannelID, 1, i.ID, "", "")
	if err != nil || len(messages) == 0 {
		b.Logger.Error("failed to find message for quote", zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't find a message to quote.")
		return
	}
	data, err := b.makeQuoteImage(s, messages[0])
	if err != nil {
		b.Logger.Error("failed to make quote", zap.String("message", messages[0].ID), zap.Error(err))
		_ = editDeferredResponse(s, i, "I couldn't quote that message.")
		return
	}

	empty := ""
	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:         &empty,
		Files:           []*discordgo.File{{Name: "quote.png", ContentType: "image/png", Reader: bytes.NewReader(data)}},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		b.Logger.Error("failed to send quote interaction", zap.Error(err))
	}
}

func (b *Bot) makeQuoteImage(s *discordgo.Session, message *discordgo.Message) ([]byte, error) {
	if message.Author == nil {
		return nil, fmt.Errorf("message has no author")
	}
	text := strings.TrimSpace(message.Content)
	if text == "" && len(message.Embeds) > 0 {
		text = strings.TrimSpace(strings.TrimSpace(message.Embeds[0].Title) + "\n" + strings.TrimSpace(message.Embeds[0].Description))
	}
	if text == "" {
		return nil, fmt.Errorf("message has no text")
	}

	text = resolveQuoteMentions(s, message, text)
	name, avatarURL := quoteAuthor(s, message)
	avatar, err := fetchQuoteAvatar(s.Client, avatarURL)
	if err != nil {
		b.Logger.Debug("failed to fetch quote avatar", zap.String("user", message.Author.ID), zap.Error(err))
	}
	return renderQuote(text, name, avatar)
}

func quoteAuthor(s *discordgo.Session, message *discordgo.Message) (string, string) {
	name, avatarURL := message.Author.DisplayName(), message.Author.AvatarURL("512")
	member := garminCurrentGuildMember(s, message.GuildID, message.Author.ID)
	if member == nil {
		member = message.Member
	}
	if member != nil {
		member = copyQuoteMember(member, message.GuildID, message.Author)
		name, avatarURL = member.DisplayName(), member.AvatarURL("512")
	}
	if strings.TrimSpace(name) == "" {
		name = message.Author.Username
	}
	return name, avatarURL
}

func resolveQuoteMentions(s *discordgo.Session, message *discordgo.Message, text string) string {
	replacements := make([]string, 0, len(message.Mentions)*4)
	seen := make(map[string]struct{}, len(message.Mentions))
	for _, user := range message.Mentions {
		if user == nil || user.ID == "" {
			continue
		}
		if _, ok := seen[user.ID]; ok {
			continue
		}
		seen[user.ID] = struct{}{}
		name := user.DisplayName()
		if member := garminCurrentGuildMember(s, message.GuildID, user.ID); member != nil {
			name = copyQuoteMember(member, message.GuildID, user).DisplayName()
		}
		if strings.TrimSpace(name) != "" {
			replacements = append(replacements, "<@"+user.ID+">", "@"+name, "<@!"+user.ID+">", "@"+name)
		}
	}
	if len(replacements) == 0 {
		return text
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

func copyQuoteMember(member *discordgo.Member, guildID string, user *discordgo.User) *discordgo.Member {
	copy := *member
	copy.GuildID = guildID
	if copy.User == nil {
		copy.User = user
	}
	return &copy
}

func fetchQuoteAvatar(client *http.Client, avatarURL string) (image.Image, error) {
	if avatarURL == "" {
		return nil, fmt.Errorf("author has no avatar")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("avatar returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, quoteMaxAvatar+1))
	if err != nil {
		return nil, err
	}
	if len(data) > quoteMaxAvatar {
		return nil, fmt.Errorf("avatar exceeds %d bytes", quoteMaxAvatar)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, fmt.Errorf("invalid avatar dimensions %dx%d", cfg.Width, cfg.Height)
	}
	avatar, _, err := image.Decode(bytes.NewReader(data))
	return avatar, err
}

func renderQuote(text, name string, avatar image.Image) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, quoteWidth, quoteHeight))
	background := color.RGBA{R: 17, G: 21, B: 24, A: 255}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	if avatar != nil {
		drawQuoteAvatar(canvas, avatar, background)
	} else {
		drawQuoteInitial(canvas, name)
	}

	var body font.Face
	var lines []string
	for size := 52.0; size >= 26; size -= 2 {
		face, err := opentype.NewFace(quoteFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		candidate := wrapQuoteText(face, text, quoteTextWidth)
		if body != nil {
			_ = body.Close()
		}
		body, lines = face, candidate
		if len(lines)*(body.Metrics().Height.Ceil()+8) <= 390 {
			break
		}
	}
	defer body.Close()
	lineHeight := body.Metrics().Height.Ceil() + 8
	maxLines := 390 / lineHeight
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		ellipsisWidth := font.MeasureString(body, "…").Ceil()
		lines[maxLines-1] = fitQuoteText(body, strings.TrimSpace(lines[maxLines-1]), quoteTextWidth-ellipsisWidth) + "…"
	}

	authorFace, err := opentype.NewFace(quoteFont, &opentype.FaceOptions{Size: 27, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer authorFace.Close()
	markFace, err := opentype.NewFace(quoteFont, &opentype.FaceOptions{Size: 110, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer markFace.Close()

	textHeight := len(lines) * lineHeight
	startY := (quoteHeight-textHeight-58)/2 + body.Metrics().Ascent.Ceil()
	drawer := font.Drawer{Dst: canvas, Face: markFace, Src: image.NewUniform(color.RGBA{R: 75, G: 90, B: 86, A: 255})}
	drawer.Dot = fixed.P(515, max(115, startY-60))
	drawer.DrawString("“")

	drawer.Face = body
	drawer.Src = image.NewUniform(color.RGBA{R: 238, G: 236, B: 229, A: 255})
	for index, line := range lines {
		drawer.Dot = fixed.P(570, startY+index*lineHeight)
		drawer.DrawString(line)
	}
	drawer.Face = authorFace
	drawer.Src = image.NewUniform(color.RGBA{R: 159, G: 184, B: 175, A: 255})
	drawer.Dot = fixed.P(570, startY+textHeight+34)
	drawer.DrawString("- " + fitQuoteText(authorFace, name, quoteTextWidth-20))

	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func drawQuoteAvatar(canvas *image.RGBA, avatar image.Image, background color.RGBA) {
	bounds := avatar.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	source := image.Rect(bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2, bounds.Min.X+(bounds.Dx()+side)/2, bounds.Min.Y+(bounds.Dy()+side)/2)
	xdraw.CatmullRom.Scale(canvas, image.Rect(0, 0, quoteAvatarSize, quoteHeight), avatar, source, draw.Src, nil)
	for y := 0; y < quoteHeight; y++ {
		for x := 0; x < quoteAvatarSize; x++ {
			pixel := canvas.RGBAAt(x, y)
			blend := 0.34
			if x > 170 {
				blend += 0.66 * float64(x-170) / float64(quoteAvatarSize-170)
			}
			canvas.SetRGBA(x, y, color.RGBA{
				R: uint8(float64(pixel.R)*(1-blend) + float64(background.R)*blend),
				G: uint8(float64(pixel.G)*(1-blend) + float64(background.G)*blend),
				B: uint8(float64(pixel.B)*(1-blend) + float64(background.B)*blend),
				A: 255,
			})
		}
	}
}

func drawQuoteInitial(canvas *image.RGBA, name string) {
	face, err := opentype.NewFace(quoteFont, &opentype.FaceOptions{Size: 190, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return
	}
	defer face.Close()
	initial := "?"
	for _, r := range name {
		if !unicode.IsSpace(r) {
			initial = string(unicode.ToUpper(r))
			break
		}
	}
	drawer := font.Drawer{Dst: canvas, Face: face, Src: image.NewUniform(color.RGBA{R: 43, G: 51, B: 54, A: 255})}
	width := drawer.MeasureString(initial).Ceil()
	drawer.Dot = fixed.P((quoteAvatarSize-width)/2, (quoteHeight+face.Metrics().Ascent.Ceil())/2)
	drawer.DrawString(initial)
}

func wrapQuoteText(face font.Face, text string, maxWidth int) []string {
	words := strings.Fields(text)
	lines := make([]string, 0, len(words)/5+1)
	line := ""
	for _, word := range words {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if font.MeasureString(face, candidate).Ceil() <= maxWidth {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
			line = ""
		}
		runes := []rune(word)
		for len(runes) > 0 && font.MeasureString(face, string(runes)).Ceil() > maxWidth {
			cut := len(runes)
			for cut > 1 && font.MeasureString(face, string(runes[:cut])).Ceil() > maxWidth {
				cut--
			}
			lines = append(lines, string(runes[:cut]))
			runes = runes[cut:]
		}
		line = string(runes)
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func fitQuoteText(face font.Face, text string, maxWidth int) string {
	if font.MeasureString(face, text).Ceil() <= maxWidth {
		return text
	}
	runes := []rune(text)
	for len(runes) > 1 && font.MeasureString(face, string(runes)+"…").Ceil() > maxWidth {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimSpace(string(runes)) + "…"
}
