package telegram

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

const testChatID = -100123

// fakeGroup answers Bot API calls for one group. members maps a user ID to
// the JSON fields of its getChatMember result; a missing ID fails the lookup.
type fakeGroup struct {
	mu      sync.Mutex
	members map[int64]string
	calls   []string
	texts   []string
}

func (g *fakeGroup) setMember(id int64, fields string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.members[id] = fields
}

func (g *fakeGroup) count(method string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, call := range g.calls {
		if call == method {
			n++
		}
	}
	return n
}

func (g *fakeGroup) Do(r *http.Request) (*http.Response, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	method := path.Base(r.URL.Path)
	g.calls = append(g.calls, method)
	_ = r.ParseForm()
	body := `{"ok":true,"result":true}`
	switch method {
	case "getMe":
		body = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"metrobot"}}`
	case "getChatMember":
		if r.FormValue("chat_id") != strconv.Itoa(testChatID) {
			body = `{"ok":false,"error_code":400,"description":"wrong chat"}`
			break
		}
		id, _ := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
		fields, ok := g.members[id]
		if !ok {
			body = `{"ok":false,"error_code":400,"description":"Bad Request: user not found"}`
			break
		}
		body = fmt.Sprintf(`{"ok":true,"result":{"user":{"id":%d,"first_name":"user%d"},%s}}`, id, id, fields)
	case "sendMessage":
		g.texts = append(g.texts, r.FormValue("text"))
		body = `{"ok":true,"result":{"message_id":99,"chat":{"id":-100123,"type":"supergroup"}}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

const (
	creator       = `"status":"creator"`
	restrictAdmin = `"status":"administrator","can_restrict_members":true`
	infoAdmin     = `"status":"administrator","can_change_info":true`
	plainAdmin    = `"status":"administrator","can_manage_chat":true`
	member        = `"status":"member"`
)

func newPermissionBot(t *testing.T, members map[int64]string) (*Bot, *fakeGroup) {
	t.Helper()
	group := &fakeGroup{members: members}
	api, err := tgbotapi.NewBotAPIWithClient("test", "http://telegram.invalid/bot%s/%s", group)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.TempDir() + "/bot.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	moderation := &cmd.ModerationHandler{DB: database}
	return &Bot{
		API:        api,
		Config:     &config.Config{Telegram: config.TelegramConfig{ChatID: testChatID}},
		DB:         database,
		Logger:     zap.NewNop(),
		Notes:      &cmd.NotesHandler{DB: database},
		Moderation: moderation,
		Warn:       &cmd.WarnHandler{DB: database, Moderation: moderation},
	}, group
}

func hasNote(t *testing.T, bot *Bot) bool {
	t.Helper()
	content, err := bot.DB.GetNote("x")
	if err != nil {
		t.Fatal(err)
	}
	return content != ""
}

func groupMessage(from int64, text string) *tgbotapi.Message {
	msg := &tgbotapi.Message{
		MessageID: 10,
		From:      &tgbotapi.User{ID: from, FirstName: "caller"},
		Chat:      &tgbotapi.Chat{ID: testChatID, Type: "supergroup"},
		Text:      text,
	}
	if strings.HasPrefix(text, "/") {
		length := strings.IndexByte(text+" ", ' ')
		msg.Entities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: length}}
	}
	return msg
}

func TestTelegramBannerNativeModerationRights(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{
		10: creator, 11: restrictAdmin, 12: plainAdmin, 13: member,
		14: `"status":"restricted","can_change_info":true`, 15: `"status":"left"`, 16: `"status":"kicked"`,
		20: member, 21: plainAdmin, 22: creator,
	})
	banner := bot.newBanner()
	for _, test := range []struct {
		name, caller, target string
		allowed              bool
	}{
		{"creator without flags", "10", "20", true},
		{"admin with ban users", "11", "20", true},
		{"admin without ban users", "12", "20", false},
		{"member", "13", "20", false},
		{"restricted", "14", "20", false},
		{"left", "15", "20", false},
		{"kicked", "16", "20", false},
		{"lookup error", "404", "20", false},
		{"anonymous sender", "", "20", false},
		{"non-numeric caller", "abc", "20", false},
		{"chat ID as caller", strconv.Itoa(testChatID), "20", false},
		{"protected admin target", "10", "21", false},
		{"protected creator target", "11", "22", false},
		{"target lookup error", "10", "404", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := banner.CanManageMember(test.caller, test.target, "ban")
			if (err == nil) != test.allowed {
				t.Fatalf("CanManageMember(%q, %q) = %v, allowed %v", test.caller, test.target, err, test.allowed)
			}
		})
	}
	before := group.count("getChatMember")
	if banner.CanManageMember("-5", "20", "ban") == nil || group.count("getChatMember") != before {
		t.Fatal("negative ID reached the Bot API or was allowed")
	}

	for id, want := range map[string]bool{"10": true, "12": true, "13": false} {
		if got, err := banner.IsAdministrator(id); err != nil || got != want {
			t.Errorf("IsAdministrator(%s) = %v, %v", id, got, err)
		}
	}
	if _, err := banner.IsAdministrator("404"); err == nil {
		t.Error("IsAdministrator hid a lookup error")
	}
}

func TestTelegramRevokedRightsApplyImmediately(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{11: restrictAdmin, 20: member})
	banner := bot.newBanner()
	if err := banner.CanManageMember("11", "20", "mute"); err != nil {
		t.Fatal(err)
	}
	group.setMember(11, `"status":"administrator","can_restrict_members":false`)
	if banner.CanManageMember("11", "20", "mute") == nil {
		t.Fatal("revoked Ban users right still allowed moderation")
	}
	group.setMember(11, member)
	if banner.CanManageMember("11", "20", "mute") == nil || bot.hasNativeRight("11", canRestrictMembers) {
		t.Fatal("demoted admin kept moderation rights")
	}
}

func TestTelegramStoredAdminGrantsGiveNoPower(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{42: member, 77: member})
	if err := bot.DB.AddAdmin("telegram", "42", "1"); err != nil {
		t.Fatal(err)
	}
	bot.Config.PermaAdminTelegramIDs = []string{"42"}

	for _, text := range []string{"/addnote x | short | body", "/ban 77 spam", "/warn 77 spam", "/unwarn 77 1", "!ban 77 spam", "/addadmin 77", "/removeadmin 42"} {
		bot.handleMessage(groupMessage(42, text))
	}
	if group.count("banChatMember") != 0 || group.count("restrictChatMember") != 0 {
		t.Fatalf("stored admin moderated: %v", group.calls)
	}
	if hasNote(t, bot) {
		t.Fatal("stored admin added a note")
	}
	if n, _ := bot.DB.GetWarningCount("telegram", "77"); n != 0 {
		t.Fatal("stored admin warned a user")
	}
	if want := []string{
		"You need the Change group info permission to add notes.",
		noRestrictRights, noRestrictRights, noRestrictRights,
	}; strings.Join(group.texts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("replies = %q", group.texts)
	}
}

func TestTelegramNativeAdminModeration(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{11: restrictAdmin, 12: infoAdmin, 20: member, 21: plainAdmin})

	bot.handleMessage(groupMessage(11, "/ban 21 spam"))
	if group.count("banChatMember") != 0 || !strings.Contains(strings.Join(group.texts, "\n"), "I will not take action against a group admin.") {
		t.Fatalf("protected admin target: calls %v, replies %q", group.calls, group.texts)
	}

	bot.handleMessage(groupMessage(11, "!ban 20 spam"))
	if group.count("banChatMember") != 1 {
		t.Fatalf("prefix ban by native admin did not ban: %v", group.calls)
	}

	// Change group info grants notes but not moderation.
	bot.handleMessage(groupMessage(12, "/addnote x | short | body"))
	if !hasNote(t, bot) {
		t.Fatal("admin with Change group info could not add a note")
	}
	bot.handleMessage(groupMessage(12, "/ban 20 spam"))
	bot.handleMessage(groupMessage(11, "/delnote x"))
	if group.count("banChatMember") != 1 {
		t.Fatal("admin without Ban users banned")
	}
	if !hasNote(t, bot) {
		t.Fatal("admin without Change group info deleted a note")
	}
}

func TestTelegramUnattributedSendersAreDenied(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{1087968824: creator, testChatID: creator})
	anonymous := groupMessage(1087968824, "/addnote x | short | body")
	anonymous.From.IsBot = true
	anonymous.SenderChat = &tgbotapi.Chat{ID: testChatID, Type: "supergroup"}
	bot.handleMessage(anonymous)

	channel := groupMessage(0, "!ban 20 spam")
	channel.From = nil
	channel.SenderChat = &tgbotapi.Chat{ID: -100999, Type: "channel"}
	bot.handleMessage(channel)

	if group.count("getChatMember") != 0 || group.count("banChatMember") != 0 {
		t.Fatalf("unattributed sender reached a permission lookup: %v", group.calls)
	}
	if hasNote(t, bot) {
		t.Fatal("anonymous sender added a note")
	}
}

func TestTelegramPublicReadsSkipPermissionLookups(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{})
	if err := bot.DB.AddNote("x", "short", "body"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"/note x", "/notes", ".x"} {
		bot.handleMessage(groupMessage(13, text))
	}
	if group.count("getChatMember") != 0 || group.count("sendMessage") != 3 {
		t.Fatalf("public reads: %v", group.calls)
	}
}

func TestTelegramCommandRoutingPreservesArguments(t *testing.T) {
	bot, group := newPermissionBot(t, map[int64]string{12: infoAdmin, 20: member})
	bot.handleMessage(groupMessage(12, "/addnote@otherbot x | short | wrong bot"))
	if group.count("getChatMember") != 0 || hasNote(t, bot) {
		t.Fatal("command addressed to another bot was handled")
	}
	bot.handleMessage(groupMessage(12, "/addnote@METROBOT x | short | please stay safe"))
	if content, err := bot.DB.GetNote("x"); err != nil || content != "please stay safe" {
		t.Fatalf("note content = %q, %v", content, err)
	}
}
