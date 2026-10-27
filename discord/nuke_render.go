package discord

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"math"
	"strings"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	nukeWidth  = 480
	nukeHeight = 320
	nukeFrames = 48
)

const (
	nukeInk uint8 = iota
	nukeSky
	nukeGround
	nukeGreen
	nukeYellow
	nukeOrange
	nukeRed
	nukeBlood
	nukeSmoke
	nukeAsh
	nukeWhite
)

var nukePalette = append(color.Palette{
	color.RGBA{28, 30, 24, 255}, color.RGBA{199, 194, 164, 255},
	color.RGBA{117, 116, 82, 255}, color.RGBA{65, 78, 48, 255},
	color.RGBA{255, 224, 91, 255}, color.RGBA{240, 134, 35, 255},
	color.RGBA{205, 51, 27, 255}, color.RGBA{145, 15, 25, 255},
	color.RGBA{74, 70, 57, 255}, color.RGBA{139, 130, 105, 255},
	color.RGBA{255, 246, 209, 255},
}, palette.WebSafe...)

func renderNukeGIF(name string, avatar image.Image) ([]byte, error) {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > 42 {
		name = string(runes[:39]) + "..."
	}
	if name == "" {
		name = "TARGET"
	}

	head := image.NewPaletted(image.Rect(0, 0, 72, 72), nukePalette)
	if avatar != nil && !avatar.Bounds().Empty() {
		scaled := image.NewRGBA(head.Bounds())
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), avatar, avatar.Bounds(), draw.Src, nil)
		draw.FloydSteinberg.Draw(head, head.Bounds(), scaled, image.Point{})
	} else {
		nukeRect(head, head.Bounds(), nukeAsh)
		nukeEllipse(head, 25, 29, 4, 4, nukeInk)
		nukeEllipse(head, 47, 29, 4, 4, nukeInk)
		nukeRect(head, image.Rect(24, 49, 49, 53), nukeInk)
	}

	animation := &gif.GIF{LoopCount: 0}
	for frame := range nukeFrames {
		canvas := image.NewPaletted(image.Rect(0, 0, nukeWidth, nukeHeight), nukePalette)
		nukeRect(canvas, canvas.Bounds(), nukeSky)
		nukeRect(canvas, image.Rect(0, 249, nukeWidth, nukeHeight), nukeGround)
		nukeRect(canvas, image.Rect(0, 0, nukeWidth, 30), nukeInk)
		nukeText(canvas, "GARMIN / ATOMIC DELIVERY", 16, 20, nukeWhite)
		nukeText(canvas, name, (nukeWidth-font.MeasureString(basicfont.Face7x13, name).Ceil())/2, 306, nukeInk)
		label := "TARGET ACQUIRED"
		if frame < 18 {
			// The avatar stays intact until impact so the target is recognizable.
			nukeRect(canvas, image.Rect(225, 202, 255, 244), nukeGreen)
			nukeRect(canvas, image.Rect(209, 209, 225, 234), nukeGreen)
			nukeRect(canvas, image.Rect(255, 209, 271, 234), nukeGreen)
			nukeRect(canvas, image.Rect(223, 239, 236, 265), nukeInk)
			nukeRect(canvas, image.Rect(245, 239, 258, 265), nukeInk)
			nukeRect(canvas, image.Rect(201, 126, 279, 204), nukeInk)
			draw.Draw(canvas, image.Rect(204, 129, 276, 201), head, image.Point{}, draw.Src)
			if frame >= 6 {
				y := -36 + (frame-6)*16
				nukeRect(canvas, image.Rect(229, y-25, 235, y-6), nukeInk)
				nukeRect(canvas, image.Rect(246, y-25, 252, y-6), nukeInk)
				nukeEllipse(canvas, 240, y, 16, 31, nukeInk)
				nukeEllipse(canvas, 240, y-1, 12, 26, nukeYellow)
				nukeRect(canvas, image.Rect(229, y-13, 252, y-7), nukeInk)
				nukeEllipse(canvas, 240, y+4, 4, 4, nukeInk)
			}
		} else {
			t := frame - 18
			label = "DIRECT HIT"
			nukeEllipse(canvas, 240, 263, 48+t*3, 10, nukeInk)
			nukeEllipse(canvas, 240, 263, 34+t*2, 6, nukeBlood)
			if t < 3 {
				nukeRect(canvas, image.Rect(0, 30, nukeWidth, 285), nukeWhite)
			} else {
				// Fixed particle trajectories keep rendering repeatable and bounded.
				for particle := range 30 {
					angle := float64(particle)*2.399963 + 3.4
					speed := float64(5 + particle%9)
					x := 240 + int(math.Cos(angle)*speed*float64(t))
					y := 221 - int(math.Abs(math.Sin(angle))*speed*float64(t)) + t*t/3
					y = min(y, 269+particle%12)
					nukeEllipse(canvas, x, y, 3+particle%4, 2+particle%3, nukeBlood)
				}
				rise := min(t, 20)
				cloudY := 209 - rise*5
				nukeEllipse(canvas, 240, 225-rise*2, 17+rise, 38+rise*2, nukeSmoke)
				nukeEllipse(canvas, 240, cloudY+14, 47+rise*4, 22+rise, nukeSmoke)
				for puff := range 7 {
					x := 240 + (puff-3)*(12+rise*2)
					y := cloudY - (3-int(math.Abs(float64(puff-3))))*7
					outer, inner := nukeRed, nukeOrange
					if t > 17 {
						outer, inner = nukeSmoke, nukeAsh
					}
					nukeEllipse(canvas, x, y, 24+rise*2, 23+rise, outer)
					nukeEllipse(canvas, x, y-5, 17+rise, 14+rise, inner)
				}
				if t < 18 {
					nukeEllipse(canvas, 240, 227, 32+t*3, 25+t, nukeOrange)
					nukeEllipse(canvas, 240, 228, 20+t*2, 20, nukeYellow)
				}
				// Ground-level spray remains visible in front of the cloud.
				for drop := range 18 {
					x := 240 + (drop-9)*min(t*2, 22)
					nukeEllipse(canvas, x, 267+drop%4*3, 3+drop%5, 2, nukeBlood)
				}
			}
			if t > 19 {
				label = "TARGET VAPORIZED"
			}
		}
		nukeText(canvas, label, 16, 51, nukeInk)
		animation.Image = append(animation.Image, canvas)
		delay := 8
		if frame == nukeFrames-1 {
			delay = 140
		}
		animation.Delay = append(animation.Delay, delay)
		animation.Disposal = append(animation.Disposal, gif.DisposalNone)
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, animation); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func nukeRect(dst *image.Paletted, rect image.Rectangle, ink uint8) {
	rect = rect.Intersect(dst.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(rect.Min.X, y):dst.PixOffset(rect.Max.X, y)]
		for x := range row {
			row[x] = ink
		}
	}
}

func nukeEllipse(dst *image.Paletted, cx, cy, rx, ry int, ink uint8) {
	for y := max(-ry, dst.Rect.Min.Y-cy); y <= min(ry, dst.Rect.Max.Y-1-cy); y++ {
		width := int(float64(rx) * math.Sqrt(max(0, 1-float64(y*y)/float64(ry*ry))))
		nukeRect(dst, image.Rect(cx-width, cy+y, cx+width+1, cy+y+1), ink)
	}
}

func nukeText(dst *image.Paletted, text string, x, y int, ink uint8) {
	(&font.Drawer{Dst: dst, Src: image.NewUniform(nukePalette[ink]), Face: basicfont.Face7x13, Dot: fixed.P(x, y)}).DrawString(text)
}
