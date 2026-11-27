package kudplinko

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"math"
	"math/bits"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

func stepsFor(bin, variant int) []bool {
	s := make([]bool, Rows)
	for k := 0; k < bin; k++ {
		s[(k*5+variant)%Rows] = true
	}
	for Bin(s) < bin { // collisions from the stride
		for k := range s {
			if !s[k] {
				s[k] = true
				break
			}
		}
	}
	return s
}

func testBalls(n int) []Ball {
	balls := make([]Ball, n)
	for i := range balls {
		bin := []int{0, 12, 6, 5, 7, 6, 4, 8, 3, 9, 6, 2, 10, 1, 11, 6}[i%16]
		steps := stepsFor(bin, i)
		balls[i] = Ball{Name: fmt.Sprintf("player-%02d", i), Steps: steps, Multiplier: Payouts[Bin(steps)]}
	}
	return balls
}

func TestPathMatchesStepsAndBin(t *testing.T) {
	for _, b := range testBalls(16) {
		slot := pileSlots([]int{Bin(b.Steps)})[0]
		pts := path(b.Steps, slot)
		for r := 1; r < Rows; r++ {
			dx := pts[r+1].x - pts[r].x
			want := -pegDX / 2
			if b.Steps[r-1] {
				want = pegDX / 2
			}
			if math.Abs(dx-want) > 1e-9 {
				t.Fatalf("%s row %d moved %v, want %v", b.Name, r, dx, want)
			}
		}
		last := pts[Rows].x
		if b.Steps[Rows-1] {
			last += pegDX / 2
		} else {
			last -= pegDX / 2
		}
		if math.Abs(last-binX(Bin(b.Steps))) > 1e-9 {
			t.Fatalf("%s exits at %v, want bin %d at %v", b.Name, last, Bin(b.Steps), binX(Bin(b.Steps)))
		}
		if r := binRect(Bin(b.Steps)); slot.x < float64(r.Min.X) || slot.x > float64(r.Max.X) {
			t.Fatalf("%s rests at x=%v outside bin %v", b.Name, slot.x, r)
		}
	}
}

func TestPileStaysInsideTray(t *testing.T) {
	bins := make([]int, MaxBalls)
	for i := range bins {
		bins[i] = 6
	}
	for i, p := range pileSlots(bins) {
		r := binRect(6)
		if p.y-ballR < trayTop || p.y+ballR > trayBottom || p.x-ballR < float64(r.Min.X-2) || p.x+ballR > float64(r.Max.X+2) {
			t.Fatalf("slot %d at %+v leaves the tray", i, p)
		}
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	good := testBalls(1)[0]
	cases := map[string][]Ball{
		"empty":      nil,
		"too many":   testBalls(MaxBalls + 1),
		"short":      {{Name: "a", Steps: make([]bool, Rows-1), Multiplier: 0.2}},
		"long":       {{Name: "a", Steps: make([]bool, Rows+1), Multiplier: 0.2}},
		"multiplier": {{Name: good.Name, Steps: good.Steps, Multiplier: good.Multiplier + 1}},
		"name":       {{Name: "  ", Steps: good.Steps, Multiplier: good.Multiplier}},
	}
	for name, balls := range cases {
		if _, err := Render(balls); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// composite plays the GIF and returns the last frame.
func composite(t *testing.T, data []byte) (*gif.GIF, *image.RGBA) {
	t.Helper()
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
	for _, f := range g.Image {
		draw.Draw(canvas, f.Bounds(), f, f.Bounds().Min, draw.Over)
	}
	return g, canvas
}

func TestRenderFullRound(t *testing.T) {
	balls := testBalls(MaxBalls)
	balls[3].Name = "名前 with a very long display name that will not fit"
	data, err := Render(balls)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 1<<20 {
		t.Fatalf("GIF is %d bytes, want <= 1 MiB", len(data))
	}
	if path := os.Getenv("KUD_PLINKO_SAMPLE"); path != "" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, final := composite(t, data)
	if g.Config.Width != 640 || g.Config.Height != 640 {
		t.Fatalf("size %dx%d", g.Config.Width, g.Config.Height)
	}
	if len(g.Image) > maxFrames || len(g.Image) < 10 {
		t.Fatalf("%d frames", len(g.Image))
	}
	if g.LoopCount != -1 || g.Delay[len(g.Delay)-1] < 100 {
		t.Fatalf("loop %d, final delay %d", g.LoopCount, g.Delay[len(g.Delay)-1])
	}

	// Each ball's center in the last frame is its own color, inside its own bin.
	bins := make([]int, len(balls))
	for i, b := range balls {
		bins[i] = Bin(b.Steps)
	}
	for i, slot := range pileSlots(bins) {
		fill, _, _ := playerColors(i)
		got := final.RGBAAt(int(math.Round(slot.x)), int(math.Round(slot.y)))
		if got != fill {
			t.Fatalf("ball %d center is %v, want %v", i, got, fill)
		}
	}
}

func TestRenderSingleBall(t *testing.T) {
	data, err := Render(testBalls(1)[:1])
	if err != nil {
		t.Fatal(err)
	}
	g, _ := composite(t, data)
	if len(g.Image) > fallFrames+1 {
		t.Fatalf("%d frames for one ball", len(g.Image))
	}
}

func TestFitName(t *testing.T) {
	fs, err := newFaces()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.close()
	got := fitName(fs.legend, "\x00名"+strings.Repeat("w", 80), 100)
	if strings.ContainsAny(got, "\x00名") || textWidth(fs.legend, got) > 100 || !strings.HasSuffix(got, "…") {
		t.Fatalf("fitName = %q", got)
	}
}

func TestPathMomentumAndRTP(t *testing.T) {
	if got := bits.OnesCount16(Path(0)); got != 0 {
		t.Fatalf("Path(0) landed in bin %d, want 0", got)
	}
	if got := bits.OnesCount16(Path(1)); got != Rows {
		t.Fatalf("Path(1) landed in bin %d, want %d", got, Rows)
	}
	if got := bits.OnesCount16(Path(^uint64(0))); got != Rows/2 {
		t.Fatalf("Path(all 1s) landed in bin %d, want %d", got, Rows/2)
	}

	// Exact distribution for 9/16 directional momentum.
	var dp [Bins][2]float64
	dp[0][0], dp[1][1] = 0.5, 0.5
	const keep = 9.0 / 16.0
	for range Rows - 1 {
		var next [Bins][2]float64
		for b := 0; b < Bins; b++ {
			for d := 0; d < 2; d++ {
				p := dp[b][d]
				if p == 0 {
					continue
				}
				next[b+d][d] += p * keep
				next[b+(d^1)][d^1] += p * (1 - keep)
			}
		}
		dp = next
	}
	var exactEV float64
	for b := 0; b < Bins; b++ {
		if Payouts[b] != Payouts[Bins-1-b] {
			t.Fatalf("Payouts not symmetric at bin %d", b)
		}
		exactEV += (dp[b][0] + dp[b][1]) * Payouts[b]
	}
	if exactEV < 1.0 || exactEV > 1.05 {
		t.Fatalf("exact RTP = %.4f, want in [1.00, 1.05]", exactEV)
	}

	// Verify Path sampling matches the exact model.
	rng := rand.New(rand.NewPCG(7, 42))
	const samples = 200_000
	var total float64
	for range samples {
		total += Payouts[bits.OnesCount16(Path(rng.Uint64()))]
	}
	if got := total / samples; math.Abs(got-exactEV) > 0.02 {
		t.Fatalf("sampled RTP = %.4f, exact = %.4f", got, exactEV)
	}
}
