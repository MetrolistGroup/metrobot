// Package kudplinko renders a finished Kud Plinko round as an animated GIF.
// The caller decides every path; the renderer only draws it.
package kudplinko

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	Rows     = 12
	Bins     = Rows + 1
	MaxBalls = 32
)

// Payouts maps a bin (number of right steps) to its multiplier.
var Payouts = [Bins]float64{25, 7, 2.5, 1.5, 1, 0.6, 0.5, 0.6, 1, 1.5, 2.5, 7, 25}

// Path turns a 64-bit random draw into a Rows-bit path (bit r = right on row r).
// Row 0 is 50/50; each later bounce keeps its previous direction with probability 9/16
// so balls spread further toward the outer bins than a pure binomial drop.
func Path(sample uint64) uint16 {
	dir := sample & 1
	path := uint16(dir)
	for row := 1; row < Rows; row++ {
		if (sample>>(1+4*(row-1)))&0xf >= 9 {
			dir ^= 1
		}
		path |= uint16(dir) << row
	}
	return path
}

type Ball struct {
	Name       string
	Steps      []bool // length Rows, true = right
	Multiplier float64
}

// Bin returns the payout bin a path lands in.
func Bin(steps []bool) int {
	n := 0
	for _, right := range steps {
		if right {
			n++
		}
	}
	return n
}

const (
	width, height = 640, 640
	maxFrames     = 64
	frameDelay    = 4   // centiseconds
	finalDelay    = 800 // centiseconds, held if the client loops
	framesPerSeg  = 3
	segments      = Rows + 1 // drop onto row 0, 11 bounces, final drop into the tray
	fallFrames    = segments * framesPerSeg
	maxStagger    = 4.0

	boardX     = 320.0
	pegDX      = 44.0
	dropY      = 44.0
	rowY0      = 74.0
	rowDY      = 25.0
	pegR       = 3.0
	ballR      = 6
	trayTop    = 362
	trayBottom = 428
	binTop     = 430
	binBottom  = 454
	pileStepX  = 10.0
	pileCols   = 4
	legendTop  = 482
	legendRowH = 17
	legendLeft = 24
	legendW    = width - 2*legendLeft
)

var (
	colBG      = color.RGBA{0x15, 0x12, 0x0e, 0xff}
	colLine    = color.RGBA{0x2b, 0x25, 0x1d, 0xff}
	colPeg     = color.RGBA{0x7a, 0x6a, 0x55, 0xff}
	colAmber   = color.RGBA{0xf0, 0xb5, 0x45, 0xff}
	colCream   = color.RGBA{0xed, 0xe3, 0xd1, 0xff}
	colMuted   = color.RGBA{0x8a, 0x7d, 0x6b, 0xff}
	colPending = color.RGBA{0x5c, 0x52, 0x46, 0xff}
	colInk     = color.RGBA{0x1a, 0x14, 0x0d, 0xff}

	// Bin fill and label colors by distance from the center bin.
	binFill = [7]color.RGBA{
		{0x3b, 0x32, 0x2a, 0xff}, {0x45, 0x39, 0x2d, 0xff}, {0x57, 0x43, 0x31, 0xff},
		{0x7a, 0x54, 0x30, 0xff}, {0xa8, 0x69, 0x2a, 0xff}, {0xd0, 0x8a, 0x2e, 0xff}, colAmber,
	}
	binText = [7]color.RGBA{
		{0x9c, 0x8e, 0x7a, 0xff}, {0xa8, 0x9a, 0x85, 0xff}, {0xc9, 0xb9, 0xa0, 0xff},
		{0xf0, 0xe2, 0xc8, 0xff}, colCream, colInk, colInk,
	}

	regularFont = mustParse(goregular.TTF)
	boldFont    = mustParse(gobold.TTF)
)

func mustParse(ttf []byte) *opentype.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

type point struct{ x, y float64 }

type ballPlan struct {
	name   string
	bin    int
	start  float64 // frame the ball leaves the chute
	points [segments + 1]point
	fill   color.RGBA
	shade  color.RGBA
	light  color.RGBA
}

func binX(bin int) float64 { return boardX + float64(bin-Rows/2)*pegDX }

func rowY(row int) float64 { return rowY0 + float64(row)*rowDY }

// path returns the drop point, the resting point above each peg row, and the tray slot.
func path(steps []bool, slot point) [segments + 1]point {
	var pts [segments + 1]point
	pts[0] = point{boardX, dropY}
	rights := 0
	for r := 0; r < Rows; r++ {
		pts[r+1] = point{boardX + (float64(rights)-float64(r)/2)*pegDX, rowY(r) - pegR - ballR - 1}
		if steps[r] {
			rights++
		}
	}
	pts[segments] = slot
	return pts
}

// pileSlots stacks the balls of each bin from the tray floor in landing order.
func pileSlots(bins []int) []point {
	count := make([]int, Bins)
	for _, b := range bins {
		count[b]++
	}
	seen := make([]int, Bins)
	slots := make([]point, len(bins))
	for i, b := range bins {
		j := seen[b]
		seen[b]++
		rows := (count[b] + pileCols - 1) / pileCols
		step := float64(2*ballR - 1)
		if rows > 1 {
			step = math.Min(step, float64(trayBottom-trayTop-2*ballR)/float64(rows-1))
		}
		row, col := j/pileCols, j%pileCols
		inRow := min(pileCols, count[b]-row*pileCols)
		slots[i] = point{
			binX(b) + (float64(col)-float64(inRow-1)/2)*pileStepX,
			float64(trayBottom-ballR) - float64(row)*step,
		}
	}
	return slots
}

func (b *ballPlan) at(frame int) (p point, landed bool) {
	s := (float64(frame) - b.start) / framesPerSeg
	if s >= segments {
		return b.points[segments], true
	}
	if s < 0 {
		s = 0
	}
	seg := int(s)
	t := s - float64(seg)
	a, c := b.points[seg], b.points[seg+1]
	hop := 0.0
	if seg > 0 {
		hop = 10 // bounce up off the peg before falling
	}
	return point{a.x + (c.x-a.x)*t, a.y + (c.y-a.y)*t*t - hop*t*(1-t)}, false
}

func formatMultiplier(m float64) string { return strconv.FormatFloat(m, 'f', -1, 64) + "x" }

func validate(balls []Ball) error {
	if len(balls) == 0 {
		return errors.New("kudplinko: no balls")
	}
	if len(balls) > MaxBalls {
		return fmt.Errorf("kudplinko: %d balls exceeds limit %d", len(balls), MaxBalls)
	}
	for i, b := range balls {
		if strings.TrimSpace(b.Name) == "" {
			return fmt.Errorf("kudplinko: ball %d has no name", i)
		}
		if len(b.Steps) != Rows {
			return fmt.Errorf("kudplinko: ball %d has %d steps, want %d", i, len(b.Steps), Rows)
		}
		if want := Payouts[Bin(b.Steps)]; b.Multiplier != want {
			return fmt.Errorf("kudplinko: ball %d multiplier %v does not match bin %d (%v)", i, b.Multiplier, Bin(b.Steps), want)
		}
	}
	return nil
}

// playerColors spreads hues by the golden angle at a muted saturation.
func playerColors(i int) (fill, shade, light color.RGBA) {
	h := math.Mod(205+float64(i)*137.508, 360)
	return hsl(h, 0.55, 0.60), hsl(h, 0.45, 0.34), hsl(h, 0.6, 0.82)
}

func hsl(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return color.RGBA{uint8(math.Round((r + m) * 255)), uint8(math.Round((g + m) * 255)), uint8(math.Round((b + m) * 255)), 0xff}
}

type faces struct{ title, small, label, legend, legendBold font.Face }

func newFaces() (*faces, error) {
	mk := func(f *opentype.Font, size float64) (font.Face, error) {
		return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}
	var fs faces
	var err error
	for _, x := range []struct {
		dst  *font.Face
		f    *opentype.Font
		size float64
	}{{&fs.title, boldFont, 18}, {&fs.small, regularFont, 12}, {&fs.label, boldFont, 12}, {&fs.legend, regularFont, 12}, {&fs.legendBold, boldFont, 12}} {
		if *x.dst, err = mk(x.f, x.size); err != nil {
			return nil, err
		}
	}
	return &fs, nil
}

func (fs *faces) close() {
	for _, f := range []font.Face{fs.title, fs.small, fs.label, fs.legend, fs.legendBold} {
		f.Close()
	}
}

func drawText(img draw.Image, face font.Face, s string, x, y int, c color.RGBA) {
	(&font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}).DrawString(s)
}

func textWidth(face font.Face, s string) int { return font.MeasureString(face, s).Ceil() }

// fitName replaces glyphs the font lacks and truncates to maxW pixels.
func fitName(face font.Face, name string, maxW int) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		if _, ok := face.GlyphAdvance(r); !ok {
			return '?'
		}
		return r
	}, strings.TrimSpace(name))
	if textWidth(face, name) <= maxW {
		return name
	}
	runes := []rune(name)
	for len(runes) > 0 && textWidth(face, string(runes)+"…") > maxW {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimRightFunc(string(runes), unicode.IsSpace) + "…"
}

func fillCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			a := r + 0.5 - math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if a <= 0 || !(image.Point{x, y}).In(img.Rect) {
				continue
			}
			a = math.Min(a, 1)
			o := img.PixOffset(x, y)
			p := img.Pix[o : o+3 : o+3]
			p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a)
			p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a)
			p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a)
		}
	}
}

func fillRect(img draw.Image, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

func binRect(bin int) image.Rectangle {
	x := int(math.Round(binX(bin)))
	return image.Rect(x-20, binTop, x+20, binBottom)
}

func binDistance(bin int) int { return abs(bin - Rows/2) }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

type legendLayout struct {
	cols, rows, colW int
}

func newLegend(n int) legendLayout {
	cols := (n + 7) / 8
	return legendLayout{cols: cols, rows: (n + cols - 1) / cols, colW: legendW / cols}
}

func (l legendLayout) rect(i int) image.Rectangle {
	x := legendLeft + (i/l.rows)*l.colW
	y := legendTop + (i%l.rows)*legendRowH
	return image.Rect(x, y, x+l.colW, y+legendRowH)
}

func drawStatic(img *image.RGBA, fs *faces, plans []ballPlan, legend legendLayout) {
	fillRect(img, img.Rect, colBG)

	drawText(img, fs.title, "KUD PLINKO", legendLeft, 29, colAmber)
	info := fmt.Sprintf("%d BALLS   %d ROWS", len(plans), Rows)
	drawText(img, fs.small, info, width-legendLeft-textWidth(fs.small, info), 28, colMuted)

	fillRect(img, image.Rect(int(boardX)-12, 34, int(boardX)+12, 37), colAmber) // chute
	for r := 0; r < Rows; r++ {
		for j := 0; j < r+3; j++ {
			fillCircle(img, boardX+(float64(j)-float64(r+2)/2)*pegDX, rowY(r), pegR, colPeg)
		}
	}

	for b := 0; b <= Bins; b++ {
		x := int(math.Round(binX(b) - pegDX/2))
		fillRect(img, image.Rect(x, trayTop, x+1, binBottom), colLine)
	}
	fillRect(img, image.Rect(int(binX(0)-pegDX/2), trayBottom, int(binX(Bins-1)+pegDX/2)+1, trayBottom+1), colLine)
	for b := 0; b < Bins; b++ {
		r, d := binRect(b), binDistance(b)
		fillRect(img, r, binFill[d])
		label := formatMultiplier(Payouts[b])
		drawText(img, fs.label, label, r.Min.X+(r.Dx()-textWidth(fs.label, label))/2, r.Max.Y-8, binText[d])
	}

	fillRect(img, image.Rect(legendLeft, legendTop-12, width-legendLeft, legendTop-11), colLine)
	for i := range plans {
		drawLegend(img, fs, &plans[i], legend.rect(i), false)
	}
}

func drawLegend(img *image.RGBA, fs *faces, p *ballPlan, r image.Rectangle, landed bool) {
	fillRect(img, r, colBG)
	fillCircle(img, float64(r.Min.X)+5.5, float64(r.Min.Y)+7.5, 5, p.fill)
	result, resultCol, resultFace, nameCol := "--", colPending, fs.legend, colMuted
	if landed {
		result, resultFace, nameCol = formatMultiplier(Payouts[p.bin]), fs.legendBold, colCream
		resultCol = binFill[binDistance(p.bin)]
		if binDistance(p.bin) < 3 {
			resultCol = colMuted
		}
	}
	resultW := textWidth(resultFace, result)
	rightX := r.Max.X - 12
	name := fitName(fs.legend, p.name, rightX-resultW-8-(r.Min.X+16))
	drawText(img, fs.legend, name, r.Min.X+16, r.Min.Y+12, nameCol)
	drawText(img, resultFace, result, rightX-resultW, r.Min.Y+12, resultCol)
}

func outlineRect(img draw.Image, r image.Rectangle, c color.RGBA) {
	fillRect(img, image.Rect(r.Min.X-2, r.Min.Y-2, r.Max.X+2, r.Min.Y), c)
	fillRect(img, image.Rect(r.Min.X-2, r.Max.Y, r.Max.X+2, r.Max.Y+2), c)
	fillRect(img, image.Rect(r.Min.X-2, r.Min.Y, r.Min.X, r.Max.Y), c)
	fillRect(img, image.Rect(r.Max.X, r.Min.Y, r.Max.X+2, r.Max.Y), c)
}

func outlinedBin(b int) image.Rectangle { return binRect(b).Inset(-2) }

// buildPalette keeps the ball colors exact and fills the rest with the most common board colors.
func buildPalette(plans []ballPlan, imgs ...*image.RGBA) color.Palette {
	pal := color.Palette{color.RGBA{}} // index 0 is transparent for frame diffs
	for _, p := range plans {
		pal = append(pal, p.fill, p.shade, p.light)
	}
	freq := map[color.RGBA]int{}
	for _, img := range imgs {
		for o := 0; o < len(img.Pix); o += 4 {
			freq[color.RGBA{img.Pix[o], img.Pix[o+1], img.Pix[o+2], 0xff}]++
		}
	}
	cols := make([]color.RGBA, 0, len(freq))
	for c := range freq {
		cols = append(cols, c)
	}
	sort.Slice(cols, func(i, j int) bool {
		if freq[cols[i]] != freq[cols[j]] {
			return freq[cols[i]] > freq[cols[j]]
		}
		a, b := cols[i], cols[j]
		return uint32(a.R)<<16|uint32(a.G)<<8|uint32(a.B) < uint32(b.R)<<16|uint32(b.G)<<8|uint32(b.B)
	})
	for _, c := range cols {
		if len(pal) == 256 {
			break
		}
		pal = append(pal, c)
	}
	return pal
}

func quantize(src *image.RGBA, pal color.Palette, cache map[color.RGBA]uint8) *image.Paletted {
	dst := image.NewPaletted(src.Rect, pal)
	for i, o := 0, 0; o < len(src.Pix); i, o = i+1, o+4 {
		c := color.RGBA{src.Pix[o], src.Pix[o+1], src.Pix[o+2], 0xff}
		idx, ok := cache[c]
		if !ok {
			idx = uint8(pal[1:].Index(c) + 1)
			cache[c] = idx
		}
		dst.Pix[i] = idx
	}
	return dst
}

func blit(dst, src *image.Paletted, r image.Rectangle) {
	r = r.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		o := dst.PixOffset(r.Min.X, y)
		copy(dst.Pix[o:o+r.Dx()], src.Pix[o:o+r.Dx()])
	}
}

func setPix(img *image.Paletted, x, y int, idx uint8) {
	if (image.Point{x, y}).In(img.Rect) {
		img.Pix[img.PixOffset(x, y)] = idx
	}
}

func drawBall(img *image.Paletted, p point, base uint8) {
	cx, cy := int(math.Round(p.x)), int(math.Round(p.y))
	for dy := -ballR; dy <= ballR; dy++ {
		for dx := -ballR; dx <= ballR; dx++ {
			d := math.Hypot(float64(dx), float64(dy))
			switch {
			case math.Hypot(float64(dx+2), float64(dy+2)) <= 1.5:
				setPix(img, cx+dx, cy+dy, base+2)
			case d <= ballR-1.2:
				setPix(img, cx+dx, cy+dy, base)
			case d <= ballR+0.3:
				setPix(img, cx+dx, cy+dy, base+1)
			}
		}
	}
}

func drawDot(img *image.Paletted, p point, r float64, idx uint8) {
	cx, cy := int(math.Round(p.x)), int(math.Round(p.y))
	ir := int(r) + 1
	for dy := -ir; dy <= ir; dy++ {
		for dx := -ir; dx <= ir; dx++ {
			if math.Hypot(float64(dx), float64(dy)) <= r {
				setPix(img, cx+dx, cy+dy, idx)
			}
		}
	}
}

// Render draws the round. Each ball's Steps decide its path and its bin; Multiplier must match Payouts for that bin.
func Render(balls []Ball) ([]byte, error) {
	if err := validate(balls); err != nil {
		return nil, err
	}
	fs, err := newFaces()
	if err != nil {
		return nil, err
	}
	defer fs.close()

	n := len(balls)
	stagger := maxStagger
	if n > 1 {
		stagger = math.Min(maxStagger, float64(maxFrames-1-fallFrames)/float64(n-1))
	}
	bins := make([]int, n)
	for i, b := range balls {
		bins[i] = Bin(b.Steps)
	}
	slots := pileSlots(bins)
	plans := make([]ballPlan, n)
	for i, b := range balls {
		p := &plans[i]
		p.name, p.bin, p.start = b.Name, bins[i], math.Floor(float64(i)*stagger)
		p.points = path(b.Steps, slots[i])
		p.fill, p.shade, p.light = playerColors(i)
	}
	lastFrame := int(plans[n-1].start) + fallFrames

	legend := newLegend(n)
	staticRGBA := image.NewRGBA(image.Rect(0, 0, width, height))
	drawStatic(staticRGBA, fs, plans, legend)
	finalRGBA := image.NewRGBA(staticRGBA.Rect)
	copy(finalRGBA.Pix, staticRGBA.Pix)
	for i := range plans {
		drawLegend(finalRGBA, fs, &plans[i], legend.rect(i), true)
		outlineRect(finalRGBA, binRect(plans[i].bin), colCream)
	}

	pal := buildPalette(plans, staticRGBA, finalRGBA)
	cache := map[color.RGBA]uint8{}
	static := quantize(staticRGBA, pal, cache)
	final := quantize(finalRGBA, pal, cache)
	staticRGBA, finalRGBA = nil, nil

	out := &gif.GIF{LoopCount: -1, Config: image.Config{ColorModel: pal, Width: width, Height: height}}
	prev := image.NewPaletted(static.Rect, pal)
	cur := image.NewPaletted(static.Rect, pal)
	for f := 0; f <= lastFrame; f++ {
		copy(cur.Pix, static.Pix)
		for i := range plans {
			if _, landed := plans[i].at(f); landed {
				blit(cur, final, legend.rect(i))
				blit(cur, final, outlinedBin(plans[i].bin))
			}
		}
		for i := range plans {
			p := &plans[i]
			pos, landed := p.at(f)
			if !landed && float64(f) > p.start {
				for k, lag := range []float64{2, 1} {
					tp, _ := p.at(f - int(lag))
					if float64(f)-lag >= p.start {
						drawDot(cur, tp, 1.5+float64(k), uint8(2+3*i))
					}
				}
			}
			drawBall(cur, pos, uint8(1+3*i))
		}

		delay := frameDelay
		if f == lastFrame {
			delay = finalDelay
		}
		if f == 0 {
			frame := image.NewPaletted(cur.Rect, pal)
			copy(frame.Pix, cur.Pix)
			out.Image, out.Delay, out.Disposal = append(out.Image, frame), append(out.Delay, delay), append(out.Disposal, gif.DisposalNone)
		} else if frame := diffFrame(prev, cur); frame == nil {
			out.Delay[len(out.Delay)-1] += delay
		} else {
			out.Image, out.Delay, out.Disposal = append(out.Image, frame), append(out.Delay, delay), append(out.Disposal, gif.DisposalNone)
		}
		prev, cur = cur, prev
	}

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// diffFrame crops cur to the pixels that changed since prev; unchanged pixels become transparent.
func diffFrame(prev, cur *image.Paletted) *image.Paletted {
	r := image.Rectangle{}
	w := cur.Rect.Dx()
	for y := 0; y < cur.Rect.Dy(); y++ {
		row := y * w
		minX, maxX := -1, -1
		for x := 0; x < w; x++ {
			if cur.Pix[row+x] != prev.Pix[row+x] {
				if minX < 0 {
					minX = x
				}
				maxX = x
			}
		}
		if minX >= 0 {
			r = r.Union(image.Rect(minX, y, maxX+1, y+1))
		}
	}
	if r.Empty() {
		return nil
	}
	frame := image.NewPaletted(r, cur.Palette)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if o := cur.PixOffset(x, y); cur.Pix[o] != prev.Pix[o] {
				frame.Pix[frame.PixOffset(x, y)] = cur.Pix[o]
			}
		}
	}
	return frame
}
