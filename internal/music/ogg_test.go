package music

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os/exec"
	"testing"
)

func oggPage(seq uint32, flags byte, laces []byte, payload []byte) []byte {
	h := make([]byte, 27)
	copy(h, "OggS")
	h[5] = flags
	binary.LittleEndian.PutUint32(h[14:18], 123)
	binary.LittleEndian.PutUint32(h[18:22], seq)
	h[26] = byte(len(laces))
	return append(append(h, laces...), payload...)
}

func oggHeaders() []byte {
	h := make([]byte, 19)
	copy(h, "OpusHead")
	h[8], h[9] = 1, 2
	binary.LittleEndian.PutUint32(h[12:16], 48000)
	tags := append([]byte("OpusTags"), make([]byte, 8)...)
	return append(oggPage(0, 2, []byte{19}, h), oggPage(1, 0, []byte{16}, tags)...)
}

func TestOggPacketsAcrossPages(t *testing.T) {
	packet := bytes.Repeat([]byte{0}, 300)
	packet[0] = 0xfc
	stream := append(oggHeaders(), oggPage(2, 0, []byte{255}, packet[:255])...)
	stream = append(stream, oggPage(3, 1|4, []byte{45}, packet[255:])...)
	o := newOggReader(bytes.NewReader(stream))
	got, err := o.Next()
	if err != nil || !bytes.Equal(got, packet) {
		t.Fatalf("packet = %x err = %v", got, err)
	}
	if _, err := o.Next(); err != io.EOF {
		t.Fatalf("EOF: %v", err)
	}
}

func TestOggRejectsInvalidBoundaries(t *testing.T) {
	valid := append(oggHeaders(), oggPage(2, 4, []byte{3}, []byte{0xfc, 1, 2})...)
	badSequence := append(oggHeaders(), oggPage(9, 4, []byte{3}, []byte{0xfc, 1, 2})...)
	badContinuation := append(oggHeaders(), oggPage(2, 1|4, []byte{3}, []byte{0xfc, 1, 2})...)
	non20 := append(oggHeaders(), oggPage(2, 4, []byte{3}, []byte{0xf4, 1, 2})...)
	unfinished := append(oggHeaders(), oggPage(2, 4, []byte{255}, make([]byte, 255))...)
	oversized := append(oggHeaders(), oggPage(2, 4, []byte{255, 255, 255, 255, 255, 126}, append([]byte{0xfc}, make([]byte, 1400)...))...)
	for idx, input := range [][]byte{valid[:len(valid)-1], valid[:10], []byte("not ogg"), badSequence, badContinuation, non20, unfinished, oversized, oggHeaders()} {
		o := newOggReader(bytes.NewReader(input))
		for {
			_, err := o.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					t.Errorf("fixture %d incorrectly accepted", idx)
				}
				break
			}
		}
	}
	for _, packet := range [][]byte{{}, {0xff}, {0xff, 0}, {0xf4, 0}, bytes.Repeat([]byte{0xfc}, 1401)} {
		if opus20ms(packet) {
			t.Errorf("invalid duration/size accepted: %x", packet[:min(len(packet), 4)])
		}
	}
}

func TestOggAcceptsRealFFmpegOutput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	cmd := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=0.1", "-ac", "2", "-ar", "48000", "-c:a", "libopus", "-b:a", "96k", "-frame_duration", "20", "-f", "ogg", "-page_duration", "20000", "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	o := newOggReader(bytes.NewReader(data))
	count := 0
	for {
		_, err := o.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count < 5 || count > 6 {
		t.Fatalf("expected 100ms (+ encoder padding), got %d packets", count)
	}
}
