package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestPermissionCommandsAcknowledgeBeforeLookups(t *testing.T) {
	for _, command := range []string{"ctx-reset", "addnote", "dehoist", "purge", "scanreactions", "refreshstarboard", "version", "latest", "actions"} {
		t.Run(command, func(t *testing.T) {
			authority := testModerationAuthority()
			authority.members["42"] = nil
			rec := &interactionRecorder{}
			client := mustTestClient(t)
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				key := rec.record(r)
				if response, ok := authority.response(r); ok {
					return response, nil
				}
				status, body := http.StatusOK, `{"id":"18","channel_id":"5"}`
				switch key {
				case "POST /api/v10/interactions/17/test/callback", "DELETE /api/v10/webhooks/1/test/messages/@original":
					status, body = http.StatusNoContent, ""
				case "GET /api/v10/channels/5":
					body = `{"id":"5","guild_id":"12","type":0}`
				case "PATCH /api/v10/webhooks/1/test/messages/@original", "POST /api/v10/webhooks/1/test":
				default:
					t.Errorf("unexpected request %s", key)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Config: testConfig(config.Config{DiscordGuildID: "12"}), Logger: zap.NewNop(), Moderation: &cmd.ModerationHandler{}}
			i := testCommandInteraction(t, client, fmt.Sprintf(`{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"12","channel":{"id":"5","type":0},"member":{"user":{"id":"42"}},"data":{"name":%q,"type":1,"options":[{"name":"stay","type":5,"value":true}]}}`, command))
			b.onInteractionCreate(client, i)
			if len(rec.requests) < 3 || rec.requests[0] != "POST /api/v10/interactions/17/test/callback" {
				t.Fatalf("command did not acknowledge before lookups: %v", rec.requests)
			}
			var response struct{ Type int }
			_ = json.Unmarshal([]byte(rec.bodies[rec.requests[0]]), &response)
			if response.Type != 5 {
				t.Fatalf("initial response type = %d, want defer", response.Type)
			}
		})
	}
}

func TestNoteStayChecksCurrentPermissionsAfterAcknowledgement(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		for _, rejectRich := range []bool{false, true} {
			t.Run(fmt.Sprintf("allowed=%v/rejectRich=%v", allowed, rejectRich), func(t *testing.T) {
				database, err := db.Open(t.TempDir() + "/notes.db")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { database.Close() })
				if err := database.AddNote("x", "short", notefmt.Fence("faq", "# Help\n## Question\nHelp <@123> @everyone.")); err != nil {
					t.Fatal(err)
				}
				authority := testModerationAuthority()
				authority.roles[0].permissions = discord.PermissionViewChannel
				if allowed {
					authority.roles[0].permissions |= discord.PermissionManageMessages
				}
				rec := &interactionRecorder{}
				var delivered discord.MessageCreate
				completed := false
				client := mustTestClient(t)
				client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					key := rec.record(r)
					if response, ok := authority.response(r); ok {
						return response, nil
					}
					status, body := http.StatusOK, `{"id":"18","channel_id":"5"}`
					switch key {
					case "POST /api/v10/interactions/17/test/callback", "DELETE /api/v10/webhooks/1/test/messages/@original":
						status, body = http.StatusNoContent, ""
					case "GET /api/v10/channels/5":
						body = `{"id":"5","guild_id":"12","type":0}`
					case "PATCH /api/v10/webhooks/1/test/messages/@original":
						completed = true
					case "POST /api/v10/webhooks/1/test":
						if err := json.Unmarshal([]byte(rec.bodies[key]), &delivered); err != nil {
							t.Error(err)
						}
						if !completed {
							delivered.Flags |= discord.MessageFlagEphemeral
							t.Error("first follow-up would edit the ephemeral original instead of creating a note")
						}
						if rejectRich && delivered.Flags.Has(discord.MessageFlagIsComponentsV2) {
							status, body = http.StatusBadRequest, `{"code":50035,"message":"Invalid Form Body"}`
						}
					default:
						t.Errorf("unexpected request %s", key)
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})})
				b := &Bot{Config: testConfig(config.Config{DiscordGuildID: "12"}), Logger: zap.NewNop(), Notes: &cmd.NotesHandler{DB: database}}
				i := testCommandInteraction(t, client, `{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"12","channel":{"id":"5","type":0},"member":{"user":{"id":"42"}},"data":{"name":"note","type":1,"options":[{"name":"name","type":3,"value":"x"},{"name":"stay","type":5,"value":true}]}}`)
				b.onInteractionCreate(client, i)
				if rec.requests[0] != "POST /api/v10/interactions/17/test/callback" {
					t.Fatalf("note checked permissions before acknowledging: %v", rec.requests)
				}
				if delivered.Flags.Has(discord.MessageFlagEphemeral) == allowed {
					t.Fatalf("ephemeral=%v, allowed=%v", delivered.Flags.Has(discord.MessageFlagEphemeral), allowed)
				}
				if delivered.Flags.Has(discord.MessageFlagIsComponentsV2) == rejectRich {
					t.Fatalf("rich=%v, rejected=%v", delivered.Flags.Has(discord.MessageFlagIsComponentsV2), rejectRich)
				}
				if delivered.AllowedMentions == nil || len(delivered.AllowedMentions.Parse) != 0 || len(delivered.AllowedMentions.Users) != 0 {
					t.Fatalf("note lost its mention policy: %+v", delivered.AllowedMentions)
				}
			})
		}
	}
}

func TestNicknameApprovalRespectsNativeHierarchy(t *testing.T) {
	for _, outranks := range []bool{false, true} {
		t.Run(fmt.Sprint(outranks), func(t *testing.T) {
			database, err := db.Open(t.TempDir() + "/approvals.db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { database.Close() })
			authority := testModerationAuthority()
			authority.roles[0].permissions = discord.PermissionManageNicknames
			authority.roles[4].permissions = discord.PermissionManageNicknames
			if outranks {
				authority.members["56"] = []string{testHighRoleID}
			}
			client := mustTestClient(t)
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if response, ok := authority.response(r); ok {
					return response, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"18","channel_id":"5"}`)), Request: r}, nil
			})})
			b := &Bot{Config: testConfig(config.Config{DiscordGuildID: "12"}), DB: database, Logger: zap.NewNop()}
			i := testCommandInteraction(t, client, `{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"12","channel":{"id":"5","type":0},"member":{"user":{"id":"42"}},"data":{"name":"approvenick","type":1,"options":[{"name":"user","type":6,"value":"56"}]}}`)
			b.onInteractionCreate(client, i)
			approved, err := database.ConsumeNicknameApproval("12", "56")
			if err != nil || approved == outranks {
				t.Fatalf("approval=%v, target outranks caller=%v, error=%v", approved, outranks, err)
			}
		})
	}
}
