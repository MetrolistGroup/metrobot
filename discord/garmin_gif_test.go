package discord

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"strings"
	"testing"
)

func TestGarminGIFMiddleFrame(t *testing.T) {
	if !garminGIFURL("https://cdn.discordapp.com/attachments/1/2/caption-43.gif?ex=123") {
		t.Fatal("Discord GIF attachment was not recognized")
	}
	palette := color.Palette{color.Black, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	for index := range first.Pix {
		first.Pix[index] = 1
		second.Pix[index] = 2
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	dataURL, err := garminGIFMiddleFrame(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	jpegData, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		t.Fatal(err)
	}
	r, _, b, _ := frame.At(0, 0).RGBA()
	if b <= r {
		t.Fatalf("middle frame pixel = red %d blue %d, want blue frame", r, b)
	}
}
