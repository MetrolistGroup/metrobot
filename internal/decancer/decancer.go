// Package decancer converts Unicode confusables to readable ASCII.
//
// The translation format and data are adapted from decancer v3.3.3:
// https://github.com/null8626/decancer/tree/v3.3.3. Metrobot preserves existing
// ASCII capitalization because the result is displayed as a Discord nickname
// rather than used for case-insensitive matching.
package decancer

import (
	_ "embed"
	"encoding/binary"
	"strings"
	"unicode"
)

const (
	codepointMask        = uint32(0x000fffff)
	stringTranslationBit = uint32(0x10000000)
	recordSize           = 6
)

//go:embed codepoints.bin
var codepoints []byte

var (
	caseSensitiveOffset = int(binary.LittleEndian.Uint16(codepoints[0:2]))
	similarStart        = int(binary.LittleEndian.Uint16(codepoints[2:4]))
	stringsOffset       = int(binary.LittleEndian.Uint16(codepoints[4:6]))
	codepointsEnd       = ((caseSensitiveOffset - recordSize) / recordSize) - 1
	caseSensitiveEnd    = ((similarStart - caseSensitiveOffset) / recordSize) - 1
)

// Cure transliterates Unicode confusables using Jasper's decancer options:
// ASCII-only output with Greek and emoji translations disabled.
func Cure(input string) string {
	var output strings.Builder
	output.Grow(len(input))

	for _, r := range input {
		output.WriteString(cureRune(r))
	}

	return output.String()
}

func cureRune(r rune) string {
	code := uint32(r)
	if isRemovedCodepoint(code) {
		return ""
	}
	if code <= unicode.MaxASCII {
		return string(r)
	}
	if unicode.IsSpace(r) {
		return " "
	}

	lower := unicode.ToLower(r)
	if lower != r {
		if record, ok := findTranslation(code, caseSensitiveOffset, caseSensitiveEnd); ok {
			return translate(record, code)
		}
	}

	lowerCode := uint32(lower)
	record, ok := findTranslation(lowerCode, recordSize, codepointsEnd)
	if !ok {
		return ""
	}
	return translate(record, lowerCode)
}

func findTranslation(code uint32, offset, end int) ([]byte, bool) {
	start := 0
	for start <= end {
		mid := (start + end) / 2
		record := codepoints[offset+mid*recordSize : offset+(mid+1)*recordSize]
		packed := binary.LittleEndian.Uint32(record[0:4])
		first := packed & codepointMask
		last := first
		if packed < stringTranslationBit {
			last += uint32(record[4] & 0x7f)
		}

		switch {
		case code < first:
			end = mid - 1
		case code > last:
			start = mid + 1
		default:
			// With ASCII-only output, retaining these scripts means removing
			// them instead of replacing them with misleading lookalikes.
			locale := record[5] >> 2
			if locale == 3 || locale == 21 { // Greek or emoji
				return nil, false
			}
			return record, true
		}
	}

	return nil, false
}

func translate(record []byte, code uint32) string {
	packed := binary.LittleEndian.Uint32(record[0:4])
	if packed >= stringTranslationBit {
		start := stringsOffset + int((((packed>>20)&0x07)<<8)|uint32(record[4]))
		end := start + int((packed>>23)&0x1f)
		translation := codepoints[start:end]
		if !isASCII(translation) {
			return ""
		}
		return string(translation)
	}

	translated := (packed >> 20) & 0x7f
	if translated == 0 {
		return ""
	}
	if record[4] >= 0x80 {
		translated += code - (packed & codepointMask)
	}
	if translated > unicode.MaxASCII {
		return ""
	}
	return string(rune(translated))
}

func isASCII(value []byte) bool {
	for _, b := range value {
		if b > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func isRemovedCodepoint(code uint32) bool {
	return code <= 9 ||
		(code >= 14 && code <= 31) ||
		code == 127 ||
		(code >= 0xd800 && code <= 0xf8ff) ||
		code >= 0xe01f0
}
