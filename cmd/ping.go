package cmd

import (
	"fmt"
	"net"
	"strings"
	"time"
)

type PingHandler struct{}

func (h *PingHandler) Ping() (string, error) {
	var sb strings.Builder
	sb.WriteString("📡 **Ping Results:**\n")

	// Ping 1.1.1.1
	cloudflare := pingHost("1.1.1.1:443")
	sb.WriteString(fmt.Sprintf("• Cloudflare (1.1.1.1): %s\n", cloudflare))

	// Ping Discord gateway
	discord := pingHost("gateway.discord.gg:443")
	sb.WriteString(fmt.Sprintf("• Discord Gateway: %s\n", discord))

	// Ping Telegram server
	telegram := pingHost("api.telegram.org:443")
	sb.WriteString(fmt.Sprintf("• Telegram API: %s\n", telegram))

	return sb.String(), nil
}

func pingHost(host string) string {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return fmt.Sprintf("❌ Unreachable (%s)", err)
	}
	defer conn.Close()

	elapsed := time.Since(start)
	return fmt.Sprintf("✅ %d ms", elapsed.Milliseconds())
}
