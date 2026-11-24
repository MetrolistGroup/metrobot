package discord

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

func TestExtractJSONObject(t *testing.T) {
	cases := map[string]string{
		"```json\n{\"blocks\":[]}\n```": `{"blocks":[]}`,
		`sure: {"a":1} done`:            `{"a":1}`,
		"no json here":                  "{}",
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

func TestDownloadImageStaysOnDiscordMedia(t *testing.T) {
	var urls []string
	client := &http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		if r.URL.Host == "cdn.discordapp.com" {
			location := "https://media.discordapp.net/next.png"
			if strings.HasSuffix(r.URL.Path, "/escape.png") {
				location = "http://127.0.0.1:9999/private.png"
			}
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {location}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(tinyPNG(t))), Request: r}, nil
	})}
	if _, err := downloadImage(context.Background(), client, "https://cdn.discordapp.com/ok.png"); err != nil || len(urls) != 2 {
		t.Fatalf("Discord redirect = %v urls=%v", err, urls)
	}
	urls = nil
	for _, imageURL := range []string{"https://public.example/image.png", "https://cdn.discordapp.com/escape.png"} {
		if _, err := downloadImage(context.Background(), client, imageURL); err == nil {
			t.Fatalf("%s was fetched", imageURL)
		}
	}
	if len(urls) != 1 || urls[0] != "https://cdn.discordapp.com/escape.png" {
		t.Fatalf("untrusted destination was requested: %v", urls)
	}
	if client.CheckRedirect != nil {
		t.Fatal("shared client redirect policy changed")
	}
}

func TestDownloadImageRejectsOversizedDimensions(t *testing.T) {
	source := image.NewGray(image.Rect(0, 0, 8, 8))
	var pngData, gifData, jpegData bytes.Buffer
	if png.Encode(&pngData, source) != nil || gif.Encode(&gifData, source, nil) != nil || jpeg.Encode(&jpegData, source, nil) != nil {
		t.Fatal("encode fixtures")
	}
	// Patch only the declared dimensions to 5000x5000 so no large image is allocated.
	big := []byte{0, 0, 0x13, 0x88}
	pngBytes := pngData.Bytes()
	copy(pngBytes[16:20], big)
	copy(pngBytes[20:24], big)
	binary.BigEndian.PutUint32(pngBytes[29:33], crc32.ChecksumIEEE(pngBytes[12:29]))
	gifBytes := gifData.Bytes()
	binary.LittleEndian.PutUint16(gifBytes[6:8], 5000)
	binary.LittleEndian.PutUint16(gifBytes[8:10], 5000)
	jpegBytes := jpegData.Bytes()
	sof := bytes.Index(jpegBytes, []byte{0xff, 0xc0})
	binary.BigEndian.PutUint16(jpegBytes[sof+5:sof+7], 5000)
	binary.BigEndian.PutUint16(jpegBytes[sof+7:sof+9], 5000)
	for name, data := range map[string][]byte{"png": pngBytes, "gif": gifBytes, "jpeg": jpegBytes} {
		if config, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width != 5000 {
			t.Fatalf("%s fixture = %#v %v", name, config, err)
		}
		client := &http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
		})}
		if _, err := downloadImage(context.Background(), client, "https://cdn.discordapp.com/big."+name); err == nil || !strings.Contains(err.Error(), "dimensions") {
			t.Fatalf("%s oversized image = %v", name, err)
		}
	}
}
