package util

import (
	"strconv"
	"unicode/utf8"
)

// ValidJSONUnicode rejects Unicode that encoding/json would silently replace.
// JSON syntax must still be checked by the decoder.
func ValidJSONUnicode(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		if i+1 < len(data) && data[i+1] == 'u' && i+6 <= len(data) {
			value, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
			if err == nil && value >= 0xd800 && value <= 0xdfff {
				if value > 0xdbff || i+12 > len(data) || string(data[i+6:i+8]) != `\u` {
					return false
				}
				low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
			i += 5
		} else {
			i++
		}
	}
	return true
}
