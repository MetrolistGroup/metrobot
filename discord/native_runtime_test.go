package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

type lifecycleGateway struct {
	fixtureGateway
	open   func(context.Context) error
	closed int
}

func (g *lifecycleGateway) Open(ctx context.Context) error { return g.open(ctx) }
func (g *lifecycleGateway) Close(context.Context)          { g.closed++ }

func TestNativeStartupRegistersCommandsAfterReadyAndCleansFailures(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v10/gateway" {
			t.Errorf("unexpected client construction request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"url":"wss://gateway.example.invalid"}`)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	for _, failure := range []string{"", "gateway", "registration", "ready"} {
		t.Run("failure="+failure, func(t *testing.T) {
			b, err := New(&config.Config{DiscordToken: "MTIzNDU2Nzg5MDEyMzQ1Njc4.test.test", DiscordGuildID: "300"}, nil, zap.NewNop(), nil, nil, nil, nil, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(b.Stop)
			b.Client.Rest.Close(context.Background())
			var commandNames []string
			b.Client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/applications/"+b.Client.ApplicationID.String()+"/guilds/300/commands") {
					t.Errorf("unexpected registration request: %s %s", r.Method, r.URL)
				}
				if b.Client.ID() != 900 {
					t.Error("commands registered before READY populated self-user")
				}
				var commands []struct {
					Name        string  `json:"name"`
					Type        int     `json:"type"`
					Permissions *string `json:"default_member_permissions"`
					Options     []struct {
						Name string `json:"name"`
						Type int    `json:"type"`
					} `json:"options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&commands); err != nil {
					t.Error(err)
				}
				for _, command := range commands {
					commandNames = append(commandNames, command.Name)
					if command.Name == "Clip" && command.Type != 3 || command.Name != "Clip" && command.Type != 1 {
						t.Errorf("command %s has type %d", command.Name, command.Type)
					}
					if command.Name == "bulkrole" && (value(command.Permissions) != "268435456" || len(command.Options) != 2 || command.Options[0].Type != 8) {
						t.Errorf("bulkrole permissions/options lost: %#v", command)
					}
					if command.Name == "memory" && len(command.Options) != 4 {
						t.Errorf("memory subcommands lost: %#v", command.Options)
					}
				}
				status, body := http.StatusOK, "[]"
				if failure == "registration" {
					status, body = http.StatusForbidden, `{"code":50001,"message":"Missing Access"}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			g := &lifecycleGateway{}
			g.open = func(context.Context) error {
				if failure == "gateway" {
					return errors.New("fixture connection failure")
				}
				if failure == "ready" {
					b.cancelLifecycle()
					return nil
				}
				b.Client.EventManager.HandleGatewayEvent(g, gateway.EventTypeReady, 1, gateway.EventReady{User: discord.OAuth2User{User: discord.User{ID: 900, Username: "fixture bot"}}})
				return nil
			}
			b.Client.Gateway = g
			err = b.Start()
			if (err != nil) != (failure != "") {
				t.Fatalf("Start() error = %v, failure = %q", err, failure)
			}
			if failure == "gateway" || failure == "ready" {
				if len(commandNames) != 0 {
					t.Fatal("commands registered without readiness")
				}
			} else {
				want := strings.Fields("help gsm nanoreview technicalcity gsmcompare nanoreviewcompare technicalcitycompare ctx-reset quote Clip notes note addnote editnote renamenote delnote version latest actions ban dban tban sban kick mute timeout warn warnings unwarn dehoist approvenick bulkrole addadmin removeadmin memory ping purge scanreactions refreshstarboard play pause queue")
				if !slices.Equal(commandNames, want) {
					t.Fatalf("command set changed: %v", commandNames)
				}
			}
			if failure != "" && (g.closed != 1 || b.lifecycle.Err() == nil) {
				t.Fatal("failed startup did not close the gateway and cancel lifecycle")
			}
			b.Stop()
			b.Stop()
			if g.closed != 1 || b.lifecycle.Err() == nil {
				t.Fatal("shutdown is not idempotent")
			}
		})
	}
}

func TestNativeBannerSendsTimeoutRemovalNicknameAndBanPayloads(t *testing.T) {
	client := mustTestClient(t)
	var paths []string
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		var body []byte
		if r.Body != nil {
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
		}
		var payload map[string]any
		if len(body) != 0 {
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Error(err)
			}
		}
		switch len(paths) {
		case 1:
			if _, ok := payload["communication_disabled_until"].(string); !ok {
				t.Error("timeout end was not serialized")
			}
		case 2:
			if until, present := payload["communication_disabled_until"]; !present || until != nil {
				t.Errorf("timeout removal must explicitly send null: %s", body)
			}
		case 3:
			if nick, present := payload["nick"]; !present || nick != "" {
				t.Errorf("nickname removal must explicitly send empty string: %s", body)
			}
		case 4:
			if payload["delete_message_seconds"] != float64(604800) || r.Header.Get("X-Audit-Log-Reason") != "spam" {
				t.Errorf("ban duration/reason changed: %s, %s", body, r.Header.Get("X-Audit-Log-Reason"))
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})})
	banner := &DiscordBanner{session: client, guildID: "300"}
	for _, action := range []func() error{
		func() error { return banner.Restrict("400", time.Now().Add(time.Minute).Unix()) },
		func() error { return banner.Unrestrict("400") },
		func() error { return banner.SetNickname("400", "") },
		func() error { return banner.BanAndDeleteMessages("400", "spam") },
		func() error { return banner.Unban("400") },
	} {
		if err := action(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"PATCH /api/v10/guilds/300/members/400", "PATCH /api/v10/guilds/300/members/400", "PATCH /api/v10/guilds/300/members/400", "PUT /api/v10/guilds/300/bans/400", "DELETE /api/v10/guilds/300/bans/400"}
	if !slices.Equal(paths, want) {
		t.Fatalf("banner requests = %v", paths)
	}
}

func TestNativeNicknameHierarchyRespectsPermissionsOwnerAndEqualRoles(t *testing.T) {
	client := mustTestClient(t)
	var permission = discord.PermissionManageNicknames
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := json.Marshal(discord.RestGuild{Guild: discord.Guild{ID: 300, OwnerID: 400}, Roles: []discord.Role{{ID: 300}, {ID: 500, Position: 5, Permissions: permission}, {ID: 501, Position: 4}}})
		if err != nil {
			t.Error(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})})
	botMember := &discord.Member{User: discord.User{ID: 900}, RoleIDs: []snowflake.ID{500}}
	for _, test := range []struct {
		name  string
		user  snowflake.ID
		roles []snowflake.ID
		want  bool
	}{
		{"lower role", 401, []snowflake.ID{501}, true},
		{"equal role", 401, []snowflake.ID{500}, false},
		{"owner", 400, []snowflake.ID{501}, false},
		{"self", 900, []snowflake.ID{501}, false},
	} {
		if got := canManageMember(client, "300", botMember, &discord.Member{User: discord.User{ID: test.user}, RoleIDs: test.roles}); got != test.want {
			t.Errorf("%s: manage = %v", test.name, got)
		}
	}
	permission = 0
	if canManageMember(client, "300", botMember, &discord.Member{User: discord.User{ID: 401}, RoleIDs: []snowflake.ID{501}}) {
		t.Fatal("nickname edit allowed without Manage Nicknames")
	}
}
