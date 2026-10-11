package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
)

func newTestClient(t *testing.T, opts ...bot.ConfigOpt) (*bot.Client, error) {
	t.Helper()
	opts = append([]bot.ConfigOpt{
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagsAll &^ cache.FlagMessages)),
		bot.WithRestClientConfigOpts(rest.WithHTTPClient(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("unexpected Discord request: %s %s", r.Method, r.URL)
		})})),
	}, opts...)
	client, err := disgo.New("MTIzNDU2Nzg5MDEyMzQ1Njc4.test.test", opts...)
	if err == nil {
		initialREST := client.Rest
		t.Cleanup(func() {
			client.Close(context.Background())
			if client.Rest != initialREST {
				initialREST.Close(context.Background())
			}
		})
	}
	return client, err
}

func mustTestClient(t *testing.T, opts ...bot.ConfigOpt) *bot.Client {
	t.Helper()
	client, err := newTestClient(t, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testCommandInteraction(t *testing.T, client *bot.Client, data string) *events.ApplicationCommandInteractionCreate {
	t.Helper()
	var interaction discord.ApplicationCommandInteraction
	if err := json.Unmarshal([]byte(data), &interaction); err != nil {
		t.Fatal(err)
	}
	return &events.ApplicationCommandInteractionCreate{
		ApplicationCommandInteraction: interaction,
		Respond: func(kind discord.InteractionResponseType, data discord.InteractionResponseData, opts ...rest.RequestOpt) error {
			return client.Rest.CreateInteractionResponse(interaction.ID(), interaction.Token(), discord.InteractionResponse{Type: kind, Data: data}, opts...)
		},
	}
}

func testREST(client *http.Client) rest.Rest {
	return rest.New(rest.NewClient("test", rest.WithHTTPClient(client)))
}

func cacheTestGuild(client *bot.Client, guild discord.GatewayGuild) {
	client.Caches.AddGuild(guild.Guild)
	for _, role := range guild.Roles {
		role.GuildID = guild.ID
		client.Caches.AddRole(role)
	}
	for _, member := range guild.Members {
		member.GuildID = guild.ID
		client.Caches.AddMember(member)
	}
	for _, emoji := range guild.Emojis {
		emoji.GuildID = guild.ID
		client.Caches.AddEmoji(emoji)
	}
}

func testChannel(t *testing.T, fields map[string]any) discord.GuildChannel {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var channel discord.UnmarshalChannel
	if err = json.Unmarshal(data, &channel); err != nil {
		t.Fatal(err)
	}
	return channel.Channel.(discord.GuildChannel)
}
