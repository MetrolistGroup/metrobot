//go:build music

package discord

import (
	"context"
	"fmt"
	"time"

	"github.com/MetrolistGroup/metrobot/internal/music"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"
)

func musicVoiceOptions() []voice.ManagerConfigOpt {
	return []voice.ManagerConfigOpt{voice.WithDaveSessionCreateFunc(golibdave.NewSession)}
}

func (b *Bot) initMusic() {
	b.music = music.NewPlayer(b.lifecycle, func(ctx context.Context, channel uint64) (music.Sink, error) {
		conn := b.Client.VoiceManager.CreateConn(snowflakeID(b.Config.DiscordGuildID))
		sink := &musicVoiceSink{conn: conn, bot: b, failed: func() { b.music.Disconnect(channel) }}
		if err := conn.Open(ctx, snowflake.ID(channel), false, true); err != nil {
			sink.Close()
			return nil, fmt.Errorf("Connecting to voice failed: %w", err)
		}
		return sink, nil
	}, music.Run)
}

// One worker owns speaking/UDP writes and closes this connection after cancellation.
// Avoid disgo's default AudioSender: its Close can race Open and does not unblock a provider.
type musicVoiceSink struct {
	conn     voice.Conn
	bot      *Bot
	failed   func()
	speaking bool
}

func (s *musicVoiceSink) WriteFrame(ctx context.Context, packet []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.speaking {
		if err := s.conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
			s.failed()
			return err
		}
		s.speaking = true
	}
	return s.write(ctx, packet)
}

func (s *musicVoiceSink) write(ctx context.Context, packet []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.UDP().SetWriteDeadline(deadline); err != nil {
		s.failed()
		return err
	}
	if _, err := s.conn.UDP().Write(packet); err != nil {
		s.failed()
		return fmt.Errorf("Voice connection lost: %w", err)
	}
	return nil
}

func (s *musicVoiceSink) Silence(ctx context.Context) error {
	if !s.speaking {
		return nil
	}
	for i := 0; i < 5; i++ {
		if err := s.write(ctx, voice.SilenceAudioFrame); err != nil {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	if err := s.conn.SetSpeaking(ctx, voice.SpeakingFlagNone); err != nil {
		s.failed()
		return err
	}
	s.speaking = false
	return nil
}

func (s *musicVoiceSink) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.conn.Close(ctx)
	// Wait for the gateway's leave acknowledgement before allowing another session.
	for {
		state, ok := s.bot.Client.Caches.VoiceState(snowflakeID(s.bot.Config.DiscordGuildID), s.bot.Client.ID())
		if !ok || state.ChannelID == nil {
			return
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}
