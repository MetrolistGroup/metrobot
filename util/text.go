package util

import "unicode/utf16"

// UTF16Len counts the code units used by chat API text limits.
func UTF16Len(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	return length
}
