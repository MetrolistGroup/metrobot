package telegram

import (
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func TestTelegramNoteReplyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, text string
		file       bool
	}{
		{name: "ASCII boundary", text: strings.Repeat("a", 4095) + "\n"},
		{name: "newline overflow", text: strings.Repeat("a", 4096) + "\n", file: true},
		{name: "BMP boundary", text: strings.Repeat("界", 4096)},
		{name: "supplementary boundary", text: strings.Repeat("𝄞", 2048)},
		{name: "Unicode overflow", text: strings.Repeat("𝄞", 2048) + "\n", file: true},
		{name: "serialized HTML boundary", text: strings.Repeat("&", 819) + "a"},
		{name: "serialized HTML overflow", text: strings.Repeat("&", 819) + "aa", file: true},
		{name: "raw Markdown file", text: strings.Repeat("**界𝄞**\r\n<https://example.com>\\n\n", 200), file: true},
		{name: "short formatted note", text: "## Question\n**answer** `code` https://example.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reply := newTelegramNoteReply(123, 456, test.text)
			if !test.file {
				want := newTelegramNoteMessage(123, test.text)
				want.ReplyToMessageID = 456
				if !reflect.DeepEqual(reply, want) {
					t.Fatal("short HTML note or preview/reply behavior changed")
				}
				return
			}
			document, ok := reply.(tgbotapi.DocumentConfig)
			if !ok || document.ChatID != 123 || document.ReplyToMessageID != 456 || document.Caption != "" || document.ParseMode != "" {
				t.Fatalf("attachment reply = %#v", reply)
			}
			if !document.File.NeedsUpload() {
				t.Fatal("attachment used external URL delivery")
			}
			name, reader, err := document.File.UploadData()
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(reader)
			if err != nil || name != "note.md" || string(content) != test.text {
				t.Fatalf("full raw attachment not preserved: %s, %v", name, err)
			}
		})
	}
}

type telegramNoteHTTPFunc func(*http.Request) (*http.Response, error)

func (f telegramNoteHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramNoteSendHelpersUploadFullReplyAndKeepCleanup(t *testing.T) {
	text := strings.Repeat("**Full 界𝄞**\n<https://example.com>\n", 200)
	for _, test := range []struct {
		name            string
		public, cleanup bool
	}{{"public persistent", true, false}, {"public cleanup", true, true}, {"ephemeral stay", false, false}, {"ephemeral cleanup", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				deletions := make(chan string, 2)
				sent := 0
				client := telegramNoteHTTPFunc(func(r *http.Request) (*http.Response, error) {
					body := `{"ok":true,"result":true}`
					switch {
					case strings.HasSuffix(r.URL.Path, "/getMe"):
						body = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test"}}`
					case strings.HasSuffix(r.URL.Path, "/deleteMessage"):
						if err := r.ParseForm(); err != nil {
							t.Error(err)
						}
						if r.FormValue("chat_id") != "123" {
							t.Error("cleanup changed chat")
						}
						deletions <- r.FormValue("message_id")
					case strings.HasSuffix(r.URL.Path, "/sendDocument"):
						sent++
						if err := r.ParseMultipartForm(256 * 1024); err != nil {
							t.Error(err)
						} else {
							defer r.MultipartForm.RemoveAll()
							if r.FormValue("chat_id") != "123" || r.FormValue("reply_to_message_id") != "456" || r.FormValue("caption") != "" || r.FormValue("parse_mode") != "" {
								t.Errorf("document lost reply or introduced rendered text: %v", r.Form)
							}
							file, header, err := r.FormFile("document")
							if err != nil {
								t.Error(err)
							} else {
								defer file.Close()
								content, err := io.ReadAll(file)
								if err != nil || header.Filename != "note.md" || string(content) != text {
									t.Error("full raw note.md was not uploaded")
								}
							}
						}
						body = `{"ok":true,"result":{"message_id":789,"chat":{"id":123,"type":"group"}}}`
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				bot, err := tgbotapi.NewBotAPIWithClient("test", "http://telegram.invalid/bot%s/%s", client)
				if err != nil {
					t.Fatal(err)
				}
				if test.public {
					sendPublicNoteReply(bot, 123, 456, text, test.cleanup, zap.NewNop())
				} else {
					sendEphemeralNoteReply(bot, 123, 456, text, !test.cleanup, zap.NewNop())
				}
				if sent != 1 {
					t.Fatalf("sent %d messages, want one attachment", sent)
				}
				time.Sleep(15*time.Minute - time.Nanosecond)
				synctest.Wait()
				if len(deletions) != 0 {
					t.Fatal("note cleanup ran early")
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				if !test.cleanup {
					if len(deletions) != 0 {
						t.Fatal("persistent note was deleted")
					}
					return
				}
				if len(deletions) != 2 {
					t.Fatal("cleanup did not delete both note and command")
				}
				ids := []string{<-deletions, <-deletions}
				slices.Sort(ids)
				if !slices.Equal(ids, []string{"456", "789"}) {
					t.Fatalf("deleted messages = %v", ids)
				}
			})
		})
	}
}
