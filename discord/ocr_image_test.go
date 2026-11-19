package discord

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

func TestExtractJSONObject(t *testing.T) {
	cases := map[string]string{
		"```json\n{\"blocks\":[]}\n```": `{"blocks":[]}`,
		`sure: {"a":1} done`:           `{"a":1}`,
		"no json here":                 "{}",
	}
	for in, want := range cases {
		if got := extractJSONObject(in); got != want {
			t.Errorf("extractJSONObject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	if c := parseHexColor("#ff0000", color.Black); c != (color.RGBA{R: 255, A: 255}) {
		t.Errorf("#ff0000 = %v", c)
	}
	if c := parseHexColor("0f0", color.Black); c != (color.RGBA{G: 255, A: 255}) {
		t.Errorf("0f0 = %v", c)
	}
	if c := parseHexColor("nonsense", color.White); c != color.White {
		t.Errorf("fallback not used: %v", c)
	}
}

func TestWrapText(t *testing.T) {
	face, err := opentype.NewFace(ocrOverlayFont, &opentype.FaceOptions{Size: 12, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	lines := wrapText(face, "the quick brown fox jumps", 40)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping, got %v", lines)
	}
}

func TestDrawOCROverlayCoversRegion(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	// The whole image starts black; a block should repaint its region with the given background.
	raw, err := drawOCROverlay(src, []ocrBlock{{Text: "hi", Box: [4]float64{0, 0, 0.5, 0.5}, TextColor: "#000000", BgColor: "#ffffff"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := out.At(2, 2).RGBA()
	if r>>8 < 200 {
		t.Fatalf("background not painted, red=%d", r>>8)
	}
	if _, err := drawOCROverlay(src, []ocrBlock{{Text: "x", Box: [4]float64{0, 0, 0, 0}}}); err == nil {
		t.Fatal("expected error when no block is drawable")
	}
}
