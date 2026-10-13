//go:build !music

package discord

import "github.com/disgoorg/disgo/voice"

// No connections are created: the registered commands explicitly report unavailable.
func musicVoiceOptions() []voice.ManagerConfigOpt { return nil }
func (b *Bot) initMusic()                         {}
