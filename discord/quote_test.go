package discord

import (
	"bytes"
	"image"
	"image/color"
	_ "image/png"
	"strings"
	"testing"
)

func TestQuoteTriggersAndImageOutput(t *testing.T) {
	for _, trigger := range []string{"ogc", " OGC ", "garmin clip that", "ok garmin video speichern", "garmin quote"} {
		if !isQuoteTrigger(trigger) {
			t.Errorf("isQuoteTrigger(%q) = false", trigger)
		}
	}
	for _, other := range []string{"ogc now", "quote", "garmin clip this"} {
		if isQuoteTrigger(other) {
			t.Errorf("isQuoteTrigger(%q) = true", other)
		}
	}

	avatar := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			avatar.SetRGBA(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 90, A: 255})
		}
	}
	data, err := renderQuote(strings.Repeat("a quoted message ", 80), "Quote Author", avatar)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || cfg.Width != quoteWidth || cfg.Height != quoteHeight {
		t.Fatalf("quote image = %s %dx%d", format, cfg.Width, cfg.Height)
	}
}
