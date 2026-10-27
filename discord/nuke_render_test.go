package discord

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"testing"
)

func TestRenderNukeGIF(t *testing.T) {
	avatar := image.NewRGBA(image.Rect(10, 20, 74, 84))
	for y := 20; y < 84; y++ {
		for x := 10; x < 74; x++ {
			avatar.SetRGBA(x, y, color.RGBA{R: 20, G: 60, B: 245, A: 255})
		}
	}
	data, err := renderNukeGIF("Target", avatar)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 5<<20 {
		t.Fatalf("GIF too large: %d bytes", len(data))
	}
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if animation.Config.Width != nukeWidth || animation.Config.Height != nukeHeight || len(animation.Image) != nukeFrames || animation.LoopCount != 0 {
		t.Fatalf("unexpected GIF dimensions, frames or looping: %#v", animation.Config)
	}
	for index, delay := range animation.Delay {
		if delay <= 0 {
			t.Errorf("frame %d has no delay", index)
		}
	}
	if bytes.Equal(animation.Image[0].Pix, animation.Image[12].Pix) || bytes.Equal(animation.Image[0].Pix, animation.Image[30].Pix) {
		t.Fatal("bomb and blast did not animate")
	}
	if animation.Image[0].At(240, 160) == animation.Image[0].At(100, 160) {
		t.Fatal("target avatar was not drawn")
	}
	if !bytes.Contains(animation.Image[nukeFrames-1].Pix, []byte{nukeBlood}) {
		t.Fatal("aftermath lost blood splatter")
	}
	fallback, err := renderNukeGIF("Target", nil)
	if err != nil || bytes.Equal(data, fallback) {
		t.Fatalf("avatar fallback error=%v, same output=%v", err, bytes.Equal(data, fallback))
	}
	again, err := renderNukeGIF("Target", avatar)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatal("renderer is not deterministic")
	}
	t.Logf("GIF: %d bytes, %d frames, %dx%d", len(data), len(animation.Image), nukeWidth, nukeHeight)
}

func TestRenderNukeGIFBounds(t *testing.T) {
	for _, name := range []string{"", strings.Repeat("世界\n\x00", 100), "@everyone"} {
		data, err := renderNukeGIF(name, image.NewRGBA(image.Rectangle{}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gif.DecodeAll(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNukeTriggers(t *testing.T) {
	for _, content := range []string{"garmin nuke them", " GARMIN NUKE THEM ", "garmin, nuke them", "ok garmin nuke them", "ok garmin, nuke them", "garmin\t nuke   them"} {
		if !isNukeTrigger(content) {
			t.Errorf("did not recognize %q", content)
		}
	}
	for _, content := range []string{"garmin nuke", "please garmin nuke them", "garmin nuke them tomorrow", "garmin nuke their account"} {
		if isNukeTrigger(content) {
			t.Errorf("incorrectly recognized %q", content)
		}
	}
}
