package discord

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"math"
	"math/rand/v2"
	"strings"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	nukeWidth  = 640
	nukeHeight = 360
	nukeFrames = 64
	nukeImpact = 22
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
	nukeDarkBlood
	nukeWetBlood
	nukeDarkSmoke
	nukeDust
	nukeSteel
)

var nukePalette = append(color.Palette{
	color.RGBA{22, 23, 22, 255}, color.RGBA{144, 150, 143, 255},
	color.RGBA{91, 86, 70, 255}, color.RGBA{57, 70, 49, 255},
	color.RGBA{255, 211, 85, 255}, color.RGBA{243, 113, 24, 255},
	color.RGBA{183, 45, 21, 255}, color.RGBA{128, 10, 16, 255},
	color.RGBA{66, 64, 61, 255}, color.RGBA{116, 109, 95, 255},
	color.RGBA{255, 245, 209, 255}, color.RGBA{57, 5, 10, 255},
	color.RGBA{190, 26, 30, 255}, color.RGBA{38, 37, 36, 255},
	color.RGBA{157, 139, 106, 255}, color.RGBA{110, 118, 107, 255},
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

	head := image.NewPaletted(image.Rect(0, 0, 84, 84), nukePalette)
	if avatar != nil && !avatar.Bounds().Empty() {
		scaled := image.NewRGBA(head.Bounds())
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), avatar, avatar.Bounds(), draw.Src, nil)
		draw.FloydSteinberg.Draw(head, head.Bounds(), scaled, image.Point{})
	} else {
		nukeRect(head, head.Bounds(), nukeAsh)
		nukeEllipse(head, 29, 34, 4, 5, nukeInk)
		nukeEllipse(head, 55, 34, 4, 5, nukeInk)
		nukeRect(head, image.Rect(28, 57, 57, 61), nukeInk)
	}

	animation := &gif.GIF{LoopCount: 0}
	for frame := range nukeFrames {
		canvas := image.NewPaletted(image.Rect(0, 0, nukeWidth, nukeHeight), nukePalette)
		nukeRect(canvas, canvas.Bounds(), nukeSky)
		// Distant silhouettes and ground texture give the blast a scale reference.
		for building := range 19 {
			x, height := building*37-12, 12+(building*23)%49
			nukeRect(canvas, image.Rect(x, 293-height, x+25, 299), nukeAsh)
		}
		nukeRect(canvas, image.Rect(0, 299, nukeWidth, nukeHeight), nukeGround)
		for stone := range 90 {
			x, y := (stone*173)%nukeWidth, 301+(stone*37)%28
			nukeRect(canvas, image.Rect(x, y, x+2+stone%3, y+1), nukeAsh)
		}
		// Reset the seed per frame so every fragment follows one stable trajectory.
		rng := rand.New(rand.NewPCG(17, 93))
		label := "TARGET ACQUIRED"
		if frame < nukeImpact {
			nukeEllipse(canvas, 320, 303, 47, 8, nukeInk)
			nukeLine(canvas, 310, 273, 300, 301, 7, nukeInk)
			nukeLine(canvas, 330, 273, 341, 301, 7, nukeInk)
			nukeLine(canvas, 306, 241, 286, 268, 7, nukeGreen)
			nukeLine(canvas, 334, 241, 354, 268, 7, nukeGreen)
			nukeEllipse(canvas, 320, 253, 20, 31, nukeInk)
			nukeEllipse(canvas, 322, 251, 16, 27, nukeGreen)
			nukeRect(canvas, image.Rect(277, 149, 363, 235), nukeInk)
			draw.Draw(canvas, image.Rect(278, 150, 362, 234), head, image.Point{}, draw.Src)
			if frame >= 6 {
				t := frame - 6
				y := -42 + t*t*4/5
				nukeLine(canvas, 320, y-52, 320, y-32, 1, nukeWhite)
				nukeLine(canvas, 301, y-37, 310, y-18, 3, nukeInk)
				nukeLine(canvas, 339, y-37, 330, y-18, 3, nukeInk)
				nukeEllipse(canvas, 320, y, 16, 32, nukeInk)
				nukeEllipse(canvas, 318, y-2, 12, 28, nukeSteel)
				nukeRect(canvas, image.Rect(308, y-10, 333, y-3), nukeYellow)
				nukeEllipse(canvas, 320, y+7, 4, 5, nukeInk)
			}
		} else {
			t := frame - nukeImpact
			label = "DIRECT HIT"
			if t < 2 {
				nukeRect(canvas, image.Rect(0, 30, nukeWidth, 330), nukeWhite)
			} else {
				// A ground-hugging shock front outruns the rising fireball.
				ring := min(430, t*20)
				nukeEllipse(canvas, 320, 302, ring, 7+t/2, nukeDust)
				nukeEllipse(canvas, 320, 302, max(1, ring-18), max(1, t/2), nukeGround)
				nukeEllipse(canvas, 320, 304, 50+min(t*3, 85), 14, nukeInk)
				nukeEllipse(canvas, 320, 305, 36+min(t*2, 69), 8, nukeDarkBlood)
				for debris := range 44 {
					angle := rng.Float64() * 2 * math.Pi
					speed := float64(4 + rng.IntN(10))
					x := 320 + int(math.Cos(angle)*speed*float64(t))
					y := min(302+debris%20, 242-int(math.Abs(math.Sin(angle))*speed*float64(t))+t*t/2)
					nukeEllipse(canvas, x, y, 2+debris%4, 1+debris%3, nukeInk)
					if debris%3 == 0 && t < 23 {
						nukeEllipse(canvas, x-1, y-1, 1, 1, nukeOrange)
					}
				}
				// Uneven overlapping lobes, shaded underneath, avoid a flat mushroom outline.
				rise := min(t, 34)
				cloudY := 274 - rise*4
				nukeEllipse(canvas, 320, 254-rise, 14+rise, 42+rise, nukeDarkSmoke)
				for puff := range 24 {
					angle := rng.Float64() * 2 * math.Pi
					x := 320 + int(math.Cos(angle)*float64(18+rise*4)*(0.5+rng.Float64()/2))
					y := cloudY + int(math.Sin(angle)*float64(8+rise/2)) + rng.IntN(15) - 7
					radius := 13 + rise/2 + rng.IntN(10)
					outer, inner, light := nukeRed, nukeOrange, nukeYellow
					if t > 16+puff%8 {
						outer, inner, light = nukeDarkSmoke, nukeSmoke, nukeAsh
					}
					nukeEllipse(canvas, x, y+7, radius+6, radius, outer)
					nukeEllipse(canvas, x-3, y-3, radius, radius-3, inner)
					nukeEllipse(canvas, x-6, y-7, radius/2, radius/3, light)
				}
				if t < 25 {
					radius := 25 + min(t*5, 72)
					nukeEllipse(canvas, 320, 266-t*2, radius, radius*3/4, nukeRed)
					nukeEllipse(canvas, 315, 260-t*2, radius*4/5, radius*3/5, nukeOrange)
					nukeEllipse(canvas, 311, 254-t*2, radius/2, radius*2/5, nukeYellow)
					nukeEllipse(canvas, 307, 249-t*2, radius/4, radius/4, nukeWhite)
					fire := rand.New(rand.NewPCG(71, 22))
					for flame := 0; flame < 30; flame++ {
						angle, reach := fire.Float64()*2*math.Pi, fire.Float64()
						x := 315 + int(math.Cos(angle)*float64(radius)*reach)
						y := 260 - t*2 + int(math.Sin(angle)*float64(radius)*reach*0.7)
						ink := nukeOrange
						if reach < 0.6 {
							ink = nukeYellow
						}
						if reach < 0.25 {
							ink = nukeWhite
						}
						nukeEllipse(canvas, x, y, 6+fire.IntN(13), 4+fire.IntN(10), ink)
					}
				}
				// Avatar fragments and ballistic spray keep the effect tied to this target.
				for piece := range 36 {
					vx, vy := rng.IntN(29)-14, 6+rng.IntN(12)
					x := 320 + vx*t
					y := min(309+piece%15, 209-vy*t+t*t/2)
					previousY := min(309+piece%15, 209-vy*(t-1)+(t-1)*(t-1)/2)
					nukeLine(canvas, x-vx, previousY, x, y, 1+piece%3, nukeDarkBlood)
					nukeEllipse(canvas, x, y, 3+piece%5, 2+piece%3, nukeBlood)
					if t < 15 && piece%4 == 0 {
						source := image.Pt(piece%6*14, piece/6*14)
						draw.Draw(canvas, image.Rect(x-4, y-4, x+4, y+4), head, source, draw.Src)
					}
					if y >= 309 {
						nukeEllipse(canvas, x, y, 5+piece%7, 2, nukeWetBlood)
					}
				}
				// Foreground splashes settle into heavy, slow drips instead of vanishing dots.
				if t >= 5 {
					for splash := range 11 {
						x := rng.IntN(nukeWidth)
						y := 65 + rng.IntN(230)
						radius := 5 + rng.IntN(12)
						nukeEllipse(canvas, x, y, radius+2, radius, nukeDarkBlood)
						for spur := range 7 {
							angle := float64(spur)*math.Pi*2/7 + float64(splash)
							nukeEllipse(canvas, x+int(math.Cos(angle)*float64(radius)), y+int(math.Sin(angle)*float64(radius)), 3, 2, nukeBlood)
						}
						nukeEllipse(canvas, x-1, y-2, radius-1, radius-2, nukeBlood)
						drip := min(326-y, (t-5)*(2+splash%3)/3)
						nukeLine(canvas, x, y, x+1, y+drip, 1+splash%2, nukeBlood)
						nukeEllipse(canvas, x+1, y+drip, 2+splash%2, 3, nukeWetBlood)
						nukeEllipse(canvas, x-3, y-4, 2, 1, nukeWetBlood)
					}
				}
			}
			if t > 27 {
				label = "TARGET VAPORIZED"
			}
		}
		nukeRect(canvas, image.Rect(0, 0, nukeWidth, 30), nukeInk)
		nukeRect(canvas, image.Rect(0, 332, nukeWidth, nukeHeight), nukeInk)
		nukeText(canvas, "GARMIN / ATOMIC DELIVERY", 16, 20, nukeWhite)
		nukeText(canvas, label, 16, 51, nukeInk)
		nukeText(canvas, name, (nukeWidth-font.MeasureString(basicfont.Face7x13, name).Ceil())/2, 350, nukeWhite)
		animation.Image = append(animation.Image, canvas)
		delay := 6
		if frame == nukeFrames-1 {
			delay = 150
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

func nukeLine(dst *image.Paletted, x0, y0, x1, y1, radius int, ink uint8) {
	steps := max(1, int(math.Max(math.Abs(float64(x1-x0)), math.Abs(float64(y1-y0)))))
	for step := 0; step <= steps; step++ {
		nukeEllipse(dst, x0+(x1-x0)*step/steps, y0+(y1-y0)*step/steps, radius, radius, ink)
	}
}

func nukeText(dst *image.Paletted, text string, x, y int, ink uint8) {
	(&font.Drawer{Dst: dst, Src: image.NewUniform(nukePalette[ink]), Face: basicfont.Face7x13, Dot: fixed.P(x, y)}).DrawString(text)
}
