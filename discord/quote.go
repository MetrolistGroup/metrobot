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
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	"github.com/yuin/goldmark"
	goldmarkast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	goldmarktext "github.com/yuin/goldmark/text"
	"go.uber.org/zap"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
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

type quoteTextStyle uint8

const (
	quoteBold quoteTextStyle = 1 << iota
	quoteItalic
	quoteUnderline
	quoteStrike
	quoteCode
	quoteLink
	quoteSpoiler
)

type quoteSpan struct {
	text  string
	style quoteTextStyle
}

type quoteLine []quoteSpan

type quoteFaceSet struct {
	regular    font.Face
	bold       font.Face
	italic     font.Face
	boldItalic font.Face
	mono       font.Face
}

var (
	quoteFont, _           = opentype.Parse(goregular.TTF)
	quoteBoldFont, _       = opentype.Parse(gobold.TTF)
	quoteItalicFont, _     = opentype.Parse(goitalic.TTF)
	quoteBoldItalicFont, _ = opentype.Parse(gobolditalic.TTF)
	quoteMonoFont, _       = opentype.Parse(gomono.TTF)
	quoteMarkdown          = goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
)

func isQuoteTrigger(content string) bool {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "ogc", "garmin clip that", "garmin clip this", "ok garmin video speichern", "garmin quote":
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

	interaction := i.ApplicationCommandData()
	var target *discordgo.Message
	if interaction.CommandType == discordgo.MessageApplicationCommand {
		if interaction.Resolved != nil {
			target = interaction.Resolved.Messages[interaction.TargetID]
		}
	} else {
		messages, err := s.ChannelMessages(i.ChannelID, 1, i.ID, "", "")
		if err == nil && len(messages) > 0 {
			target = messages[0]
		}
	}
	if target == nil {
		b.Logger.Error("failed to find message for quote")
		_ = editDeferredResponse(s, i, "I couldn't find a message to quote.")
		return
	}
	if target.GuildID == "" {
		target.GuildID = i.GuildID
	}
	if target.ChannelID == "" {
		target.ChannelID = i.ChannelID
	}
	data, err := b.makeQuoteImage(s, target)
	if err != nil {
		b.Logger.Error("failed to make quote", zap.String("message", target.ID), zap.Error(err))
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
	replyName := quoteReplyName(s, message)
	avatar, err := fetchQuoteAvatar(s.Client, avatarURL)
	if err != nil {
		b.Logger.Debug("failed to fetch quote avatar", zap.String("user", message.Author.ID), zap.Error(err))
	}
	return renderQuote(text, name, replyName, avatar)
}

func quoteReplyName(s *discordgo.Session, message *discordgo.Message) string {
	reply := message.ReferencedMessage
	if reply == nil && s != nil && message.MessageReference != nil && message.MessageReference.MessageID != "" {
		channelID := message.MessageReference.ChannelID
		if channelID == "" {
			channelID = message.ChannelID
		}
		reply, _ = s.ChannelMessage(channelID, message.MessageReference.MessageID)
	}
	if reply == nil || reply.Author == nil {
		return ""
	}
	copy := *reply
	if copy.GuildID == "" {
		copy.GuildID = message.GuildID
	}
	name, _ := quoteAuthor(s, &copy)
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "@" + name
}

func quoteAuthor(s *discordgo.Session, message *discordgo.Message) (string, string) {
	name, avatarURL := message.Author.Username, message.Author.AvatarURL("512")
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
		name := user.Username
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

func renderQuote(text, name, replyName string, avatar image.Image) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, quoteWidth, quoteHeight))
	background := color.RGBA{R: 17, G: 21, B: 24, A: 255}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	if avatar != nil {
		drawQuoteAvatar(canvas, avatar, background)
	} else {
		drawQuoteInitial(canvas, name)
	}

	spans := parseQuoteMarkdown(text)
	availableHeight := 390
	replyHeight := 0
	if replyName != "" {
		replyHeight = 34
		availableHeight -= replyHeight
	}
	var body *quoteFaceSet
	var lines []quoteLine
	for size := 52.0; size >= 26; size -= 2 {
		faces, err := newQuoteFaceSet(size)
		if err != nil {
			return nil, err
		}
		candidate := wrapQuoteText(faces, spans, quoteTextWidth)
		if body != nil {
			body.Close()
		}
		body, lines = faces, candidate
		if len(lines)*(body.regular.Metrics().Height.Ceil()+8) <= availableHeight {
			break
		}
	}
	defer body.Close()
	lineHeight := body.regular.Metrics().Height.Ceil() + 8
	maxLines := max(1, availableHeight/lineHeight)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsizeQuoteLine(body, lines[maxLines-1], quoteTextWidth)
	}

	authorFace, err := opentype.NewFace(quoteFont, &opentype.FaceOptions{Size: 27, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer authorFace.Close()
	replyFace, err := opentype.NewFace(quoteItalicFont, &opentype.FaceOptions{Size: 22, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer replyFace.Close()
	markFace, err := opentype.NewFace(quoteFont, &opentype.FaceOptions{Size: 110, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer markFace.Close()

	textHeight := len(lines) * lineHeight
	top := (quoteHeight - replyHeight - textHeight - 58) / 2
	startY := top + replyHeight + body.regular.Metrics().Ascent.Ceil()
	drawer := font.Drawer{Dst: canvas, Face: markFace, Src: image.NewUniform(color.RGBA{R: 75, G: 90, B: 86, A: 255})}
	drawer.Dot = fixed.P(515, max(115, startY-60))
	drawer.DrawString("“")

	if replyName != "" {
		drawer.Face = replyFace
		drawer.Src = image.NewUniform(color.RGBA{R: 125, G: 143, B: 138, A: 255})
		drawer.Dot = fixed.P(570, top+replyFace.Metrics().Ascent.Ceil())
		drawer.DrawString(fitQuoteText(replyFace, "Replying to "+replyName, quoteTextWidth))
	}
	for index, line := range lines {
		drawQuoteLine(canvas, body, line, 570, startY+index*lineHeight)
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

func newQuoteFaceSet(size float64) (*quoteFaceSet, error) {
	faces := &quoteFaceSet{}
	fonts := []*opentype.Font{quoteFont, quoteBoldFont, quoteItalicFont, quoteBoldItalicFont, quoteMonoFont}
	destinations := []*font.Face{&faces.regular, &faces.bold, &faces.italic, &faces.boldItalic, &faces.mono}
	for index, parsed := range fonts {
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			faces.Close()
			return nil, err
		}
		*destinations[index] = face
	}
	return faces, nil
}

func (f *quoteFaceSet) Close() {
	for _, face := range []font.Face{f.regular, f.bold, f.italic, f.boldItalic, f.mono} {
		if face != nil {
			_ = face.Close()
		}
	}
}

func (f *quoteFaceSet) face(style quoteTextStyle) font.Face {
	if style&quoteCode != 0 {
		return f.mono
	}
	if style&quoteBold != 0 && style&quoteItalic != 0 {
		return f.boldItalic
	}
	if style&quoteBold != 0 {
		return f.bold
	}
	if style&quoteItalic != 0 {
		return f.italic
	}
	return f.regular
}

func parseQuoteMarkdown(input string) []quoteSpan {
	source := []byte(strings.ReplaceAll(input, "\r\n", "\n"))
	document := quoteMarkdown.Parser().Parse(goldmarktext.NewReader(source))
	builder := quoteMarkdownBuilder{spoilers: strings.Count(string(source), "||") >= 2}
	builder.block(source, document, 0)
	for len(builder.spans) > 0 && strings.TrimSpace(builder.spans[len(builder.spans)-1].text) == "" {
		builder.spans = builder.spans[:len(builder.spans)-1]
	}
	return builder.spans
}

type quoteMarkdownBuilder struct {
	spans    []quoteSpan
	spoiler  bool
	spoilers bool
}

func (b *quoteMarkdownBuilder) add(text string, style quoteTextStyle) {
	if text == "" {
		return
	}
	if style&quoteCode != 0 {
		if b.spoiler {
			style |= quoteSpoiler
		}
		b.addRaw(text, style)
		return
	}
	if !b.spoilers {
		b.addRaw(text, style)
		return
	}
	for {
		before, after, found := strings.Cut(text, "||")
		if b.spoiler {
			style |= quoteSpoiler
		}
		b.addRaw(before, style)
		if !found {
			return
		}
		b.spoiler = !b.spoiler
		text = after
		style &^= quoteSpoiler
	}
}

func (b *quoteMarkdownBuilder) addRaw(text string, style quoteTextStyle) {
	if text == "" {
		return
	}
	if len(b.spans) > 0 && b.spans[len(b.spans)-1].style == style {
		b.spans[len(b.spans)-1].text += text
		return
	}
	b.spans = append(b.spans, quoteSpan{text: text, style: style})
}

func (b *quoteMarkdownBuilder) block(source []byte, node goldmarkast.Node, style quoteTextStyle) {
	switch n := node.(type) {
	case *goldmarkast.Document:
		b.blockChildren(source, n, style, "\n\n")
	case *goldmarkast.Paragraph, *goldmarkast.TextBlock:
		b.inlineChildren(source, node, style)
	case *goldmarkast.Heading:
		b.inlineChildren(source, n, style|quoteBold)
	case *goldmarkast.Blockquote:
		b.add("› ", style|quoteItalic)
		b.blockChildren(source, n, style|quoteItalic, "\n")
	case *goldmarkast.List:
		index := n.Start
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			if item != n.FirstChild() {
				b.add("\n", style)
			}
			marker := "• "
			if n.IsOrdered() {
				marker = strconv.Itoa(index) + ". "
				index++
			}
			b.add(marker, style)
			b.blockChildren(source, item, style, "\n")
		}
	case *goldmarkast.CodeBlock:
		b.add(strings.TrimSuffix(string(n.Lines().Value(source)), "\n"), style|quoteCode)
	case *goldmarkast.FencedCodeBlock:
		b.add(strings.TrimSuffix(string(n.Lines().Value(source)), "\n"), style|quoteCode)
	case *goldmarkast.ThematicBreak:
		b.add("────────", style)
	case *goldmarkast.HTMLBlock:
		b.add(string(n.Text(source)), style)
	default:
		if node.Type() == goldmarkast.TypeInline {
			b.inline(source, node, style)
		} else {
			b.blockChildren(source, node, style, "\n")
		}
	}
}

func (b *quoteMarkdownBuilder) blockChildren(source []byte, parent goldmarkast.Node, style quoteTextStyle, separator string) {
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if child != parent.FirstChild() {
			b.add(separator, style)
		}
		b.block(source, child, style)
	}
}

func (b *quoteMarkdownBuilder) inlineChildren(source []byte, parent goldmarkast.Node, style quoteTextStyle) {
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		b.inline(source, child, style)
	}
}

func (b *quoteMarkdownBuilder) inline(source []byte, node goldmarkast.Node, style quoteTextStyle) {
	switch n := node.(type) {
	case *goldmarkast.Text:
		text := string(n.Value(source))
		if style&quoteCode == 0 {
			text = unescapeQuoteMarkdown(text)
		}
		b.add(text, style)
		if n.SoftLineBreak() || n.HardLineBreak() {
			b.add("\n", style)
		}
	case *goldmarkast.String:
		text := string(n.Value)
		if style&quoteCode == 0 && !n.IsRaw() {
			text = unescapeQuoteMarkdown(text)
		}
		b.add(text, style)
	case *goldmarkast.Emphasis:
		b.inlineChildren(source, n, style|quoteEmphasisTextStyle(source, n))
	case *goldmarkast.CodeSpan:
		b.inlineChildren(source, n, style|quoteCode)
	case *goldmarkast.Link:
		b.inlineChildren(source, n, style|quoteLink|quoteUnderline)
	case *goldmarkast.Image:
		b.add("image: ", style|quoteItalic)
		b.inlineChildren(source, n, style|quoteItalic)
	case *goldmarkast.AutoLink:
		b.add(string(n.Label(source)), style|quoteLink|quoteUnderline)
	case *goldmarkast.RawHTML:
		b.add(string(n.Text(source)), style)
	case *extensionast.Strikethrough:
		b.inlineChildren(source, n, style|quoteStrike)
	default:
		b.inlineChildren(source, node, style)
	}
}

func quoteEmphasisTextStyle(source []byte, emphasis *goldmarkast.Emphasis) quoteTextStyle {
	markerStart := quoteMarkdownNodeStart(emphasis, source)
	marker := byte('*')
	if markerStart >= 0 && markerStart < len(source) {
		marker = source[markerStart]
	}
	if emphasis.Level == 2 {
		if marker == '_' {
			return quoteUnderline
		}
		return quoteBold
	}
	return quoteItalic
}

func quoteMarkdownNodeStart(node goldmarkast.Node, source []byte) int {
	if node == nil {
		return -1
	}
	switch n := node.(type) {
	case *goldmarkast.Text:
		return n.Segment.Start
	case *goldmarkast.Emphasis:
		return quoteMarkdownNodeStart(n.FirstChild(), source) - n.Level
	case *goldmarkast.CodeSpan:
		return quoteMarkdownNodeStart(n.FirstChild(), source) - 1
	case *goldmarkast.Link:
		return quoteMarkdownNodeStart(n.FirstChild(), source) - 1
	case *goldmarkast.Image:
		return quoteMarkdownNodeStart(n.FirstChild(), source) - 2
	case *extensionast.Strikethrough:
		return quoteMarkdownNodeStart(n.FirstChild(), source) - 2
	default:
		return quoteMarkdownNodeStart(node.FirstChild(), source)
	}
}

func unescapeQuoteMarkdown(text string) string {
	var output strings.Builder
	output.Grow(len(text))
	for index := 0; index < len(text); index++ {
		if text[index] == '\\' && index+1 < len(text) && strings.ContainsRune(`!"#$%&'()*+,-./:;<=>?@[\]^_`+"`"+`{|}~`, rune(text[index+1])) {
			index++
		}
		output.WriteByte(text[index])
	}
	return output.String()
}

type quoteToken struct {
	span    quoteSpan
	space   bool
	newline bool
}

func quoteTokens(spans []quoteSpan) []quoteToken {
	var tokens []quoteToken
	for _, span := range spans {
		runes := []rune(span.text)
		for len(runes) > 0 {
			if runes[0] == '\r' {
				runes = runes[1:]
				continue
			}
			if runes[0] == '\n' {
				tokens = append(tokens, quoteToken{newline: true})
				runes = runes[1:]
				continue
			}
			space := unicode.IsSpace(runes[0])
			end := 1
			for end < len(runes) && runes[end] != '\n' && runes[end] != '\r' && unicode.IsSpace(runes[end]) == space {
				end++
			}
			text := string(runes[:end])
			if space && span.style&quoteCode == 0 {
				text = " "
			} else if space {
				text = strings.ReplaceAll(text, "\t", "    ")
			}
			tokens = append(tokens, quoteToken{span: quoteSpan{text: text, style: span.style}, space: space})
			runes = runes[end:]
		}
	}
	return tokens
}

func wrapQuoteText(faces *quoteFaceSet, spans []quoteSpan, maxWidth int) []quoteLine {
	var lines []quoteLine
	var line quoteLine
	var pending *quoteSpan
	flush := func(force bool) {
		if len(line) > 0 || force {
			lines = append(lines, line)
		}
		line = nil
		pending = nil
	}
	for _, token := range quoteTokens(spans) {
		if token.newline {
			flush(true)
			continue
		}
		if token.space {
			if len(line) > 0 {
				copy := token.span
				pending = &copy
			}
			continue
		}
		spaceWidth := 0
		if pending != nil {
			spaceWidth = measureQuoteSpan(faces, *pending)
		}
		if len(line) > 0 && measureQuoteLine(faces, line)+spaceWidth+measureQuoteSpan(faces, token.span) > maxWidth {
			flush(false)
		}
		if pending != nil {
			appendQuoteRun(&line, *pending)
			pending = nil
		}
		remaining := []rune(token.span.text)
		for len(remaining) > 0 {
			available := maxWidth - measureQuoteLine(faces, line)
			cut := len(remaining)
			for cut > 0 && font.MeasureString(faces.face(token.span.style), string(remaining[:cut])).Ceil() > available {
				cut--
			}
			if cut == 0 {
				if len(line) > 0 {
					flush(false)
					continue
				}
				cut = 1
			}
			appendQuoteRun(&line, quoteSpan{text: string(remaining[:cut]), style: token.span.style})
			remaining = remaining[cut:]
			if len(remaining) > 0 {
				flush(false)
			}
		}
	}
	if len(line) > 0 {
		flush(false)
	}
	return lines
}

func appendQuoteRun(line *quoteLine, span quoteSpan) {
	if span.text == "" {
		return
	}
	if len(*line) > 0 && (*line)[len(*line)-1].style == span.style {
		(*line)[len(*line)-1].text += span.text
		return
	}
	*line = append(*line, span)
}

func measureQuoteSpan(faces *quoteFaceSet, span quoteSpan) int {
	return font.MeasureString(faces.face(span.style), span.text).Ceil()
}

func measureQuoteLine(faces *quoteFaceSet, line quoteLine) int {
	width := 0
	for _, span := range line {
		width += measureQuoteSpan(faces, span)
	}
	return width
}

func ellipsizeQuoteLine(faces *quoteFaceSet, line quoteLine, maxWidth int) quoteLine {
	ellipsis := quoteSpan{text: "…"}
	for len(line) > 0 && measureQuoteLine(faces, line)+measureQuoteSpan(faces, ellipsis) > maxWidth {
		last := len(line) - 1
		runes := []rune(line[last].text)
		if len(runes) <= 1 {
			line = line[:last]
		} else {
			line[last].text = string(runes[:len(runes)-1])
		}
	}
	appendQuoteRun(&line, ellipsis)
	return line
}

func drawQuoteLine(canvas *image.RGBA, faces *quoteFaceSet, line quoteLine, x, baseline int) {
	for _, span := range line {
		face := faces.face(span.style)
		width := font.MeasureString(face, span.text).Ceil()
		metrics := face.Metrics()
		if span.style&quoteCode != 0 {
			draw.Draw(canvas, image.Rect(x-3, baseline-metrics.Ascent.Ceil()-2, x+width+3, baseline+metrics.Descent.Ceil()+2), &image.Uniform{C: color.RGBA{R: 39, G: 46, B: 48, A: 255}}, image.Point{}, draw.Src)
		}
		if span.style&quoteSpoiler != 0 {
			draw.Draw(canvas, image.Rect(x, baseline-metrics.Ascent.Ceil()+3, x+width, baseline+metrics.Descent.Ceil()), &image.Uniform{C: color.RGBA{R: 75, G: 82, B: 82, A: 255}}, image.Point{}, draw.Src)
			x += width
			continue
		}
		textColor := color.RGBA{R: 238, G: 236, B: 229, A: 255}
		if span.style&quoteLink != 0 {
			textColor = color.RGBA{R: 159, G: 184, B: 175, A: 255}
		}
		drawer := font.Drawer{Dst: canvas, Face: face, Src: image.NewUniform(textColor), Dot: fixed.P(x, baseline)}
		drawer.DrawString(span.text)
		if span.style&quoteUnderline != 0 {
			draw.Draw(canvas, image.Rect(x, baseline+3, x+width, baseline+5), &image.Uniform{C: textColor}, image.Point{}, draw.Src)
		}
		if span.style&quoteStrike != 0 {
			y := baseline - metrics.Ascent.Ceil()/3
			draw.Draw(canvas, image.Rect(x, y, x+width, y+2), &image.Uniform{C: textColor}, image.Point{}, draw.Src)
		}
		x += width
	}
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
