package notefmt

import "strings"

// Normalize accepts legacy slash-command newline escapes without rewriting JSON string escapes.
func Normalize(source string) string {
	var out, line strings.Builder
	fenceSize := 0
	var fenceChar byte
	protectQuotes, quoted := false, false
	newline := func() {
		value := strings.TrimSuffix(line.String(), "\r")
		trimmed := strings.TrimLeft(value, " ")
		if len(value)-len(trimmed) <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			char := trimmed[0]
			size := len(trimmed) - len(strings.TrimLeft(trimmed, string(char)))
			rest := strings.TrimSpace(trimmed[size:])
			if fenceSize == 0 && size >= 3 {
				fenceSize, fenceChar = size, char
				protectQuotes = rest != "faq" // Preserve JSON/code string escapes in literal example fences too.
			} else if char == fenceChar && size >= fenceSize && rest == "" {
				fenceSize, protectQuotes = 0, false
			}
		}
		out.WriteByte('\n')
		line.Reset()
	}
	for i := 0; i < len(source); i++ {
		c := source[i]
		if protectQuotes && quoted {
			out.WriteByte(c)
			line.WriteByte(c)
			if c == '\\' && i+1 < len(source) {
				i++
				out.WriteByte(source[i])
				line.WriteByte(source[i])
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if protectQuotes && c == '"' {
			quoted = true
		}
		if c == '\r' && i+1 < len(source) && source[i+1] == '\n' {
			i++
			newline()
			continue
		}
		if c == '\n' {
			newline()
			continue
		}
		if c == '\\' {
			if strings.HasPrefix(source[i:], `\r\n`) {
				i += 3
				newline()
				continue
			}
			if strings.HasPrefix(source[i:], `\n`) {
				i++
				newline()
				continue
			}
		}
		out.WriteByte(c)
		line.WriteByte(c)
	}
	return out.String()
}
