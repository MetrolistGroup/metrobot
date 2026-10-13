package music

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// This parser consumes only our ffmpeg Ogg/Opus stdout, never user media directly.
// shortcut: CRC is omitted on our local ffmpeg pipe, validate it if accepting external Ogg.
type oggReader struct {
	r        io.Reader
	serial   uint32
	sequence uint32
	started  bool
	eos      bool
	stage    int
	partial  []byte
	packets  [][]byte
}

func newOggReader(r io.Reader) *oggReader { return &oggReader{r: r} }

func (o *oggReader) Next() ([]byte, error) {
	for {
		if len(o.packets) > 0 {
			p := o.packets[0]
			o.packets = o.packets[1:]
			switch o.stage {
			case 0:
				if len(p) != 19 || !bytes.Equal(p[:8], []byte("OpusHead")) || p[8] != 1 || p[9] != 2 || binary.LittleEndian.Uint32(p[12:16]) != 48000 || p[18] != 0 {
					return nil, errors.New("expected 48k stereo Opus header")
				}
				o.stage++
			case 1:
				if len(p) < 16 || !bytes.Equal(p[:8], []byte("OpusTags")) {
					return nil, errors.New("expected Opus tags")
				}
				o.stage++
			default:
				if !opus20ms(p) {
					return nil, errors.New("expected a bounded 20ms Opus packet")
				}
				return p, nil
			}
			continue
		}
		if o.eos {
			return nil, io.EOF
		}
		if err := o.page(); err != nil {
			return nil, err
		}
	}
}

func (o *oggReader) page() error {
	var h [27]byte
	if _, err := io.ReadFull(o.r, h[:]); err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	if string(h[:4]) != "OggS" || h[4] != 0 || h[5]&^byte(7) != 0 {
		return errors.New("invalid Ogg header")
	}
	serial, seq := binary.LittleEndian.Uint32(h[14:18]), binary.LittleEndian.Uint32(h[18:22])
	if !o.started {
		if h[5]&2 == 0 || seq != 0 {
			return errors.New("missing Ogg stream start")
		}
		o.serial = serial
		o.started = true
	} else if h[5]&2 != 0 || serial != o.serial || seq != o.sequence {
		return errors.New("unexpected Ogg stream or sequence")
	}
	o.sequence = seq + 1
	if (h[5]&1 != 0) != (len(o.partial) > 0) {
		return errors.New("invalid Ogg continuation")
	}
	laces := make([]byte, int(h[26]))
	if _, err := io.ReadFull(o.r, laces); err != nil {
		return err
	}
	for _, n := range laces {
		if len(o.partial)+int(n) > 64<<10 {
			return errors.New("Ogg packet too large")
		}
		old := len(o.partial)
		o.partial = append(o.partial, make([]byte, int(n))...)
		if _, err := io.ReadFull(o.r, o.partial[old:]); err != nil {
			return err
		}
		if n < 255 {
			o.packets = append(o.packets, o.partial)
			o.partial = nil
		}
	}
	if h[5]&4 != 0 {
		if len(o.partial) != 0 {
			return io.ErrUnexpectedEOF
		}
		o.eos = true
	}
	return nil
}

func opus20ms(p []byte) bool {
	if len(p) == 0 || len(p) > 1400 {
		return false
	}
	config := p[0] >> 3
	// Durations in quarter milliseconds, per RFC 6716 section 3.1.
	var duration int
	switch {
	case config < 12:
		duration = []int{40, 80, 160, 240}[config%4]
	case config < 16:
		duration = []int{40, 80}[config%2]
	default:
		duration = []int{10, 20, 40, 80}[config%4]
	}
	count := 1
	switch p[0] & 3 {
	case 1, 2:
		count = 2
	case 3:
		if len(p) < 2 {
			return false
		}
		count = int(p[1] & 63)
	}
	return duration*count == 80
}
