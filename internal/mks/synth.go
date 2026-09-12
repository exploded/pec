package mks

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"
)

// SynthOptions describes a synthetic capture with known truth, for tests
// and demos: 20 s of slewing, then tracking with the HA encoder advancing
// at Rate counts/s from Encoder0, status polled at 7 Hz, the encoder at
// 3 Hz on both axes, and (if WithIndex) the PEC index at 1 Hz with floor
// rounding and the given offset in steps.
type SynthOptions struct {
	Start       time.Time
	Duration    float64 // s
	Rate        float64 // counts/s
	Encoder0    int64
	IndexOffset float64
	WithIndex   bool
	// PECTable, when set, is subtracted from the encoder reading at the
	// current index from PECFrom seconds on: what Apply PEC does.
	PECTable []int
	PECFrom  float64
}

// Synth returns a pcapng file for the options.
func Synth(o SynthOptions) []byte {
	s := &synth{}
	enc := func(t float64) float64 {
		if t < 20 {
			return float64(o.Encoder0) - 5000 + 250*t
		}
		return float64(o.Encoder0) + o.Rate*(t-20)
	}
	index := func(t float64) int {
		return int(math.Floor(math.Mod(enc(t)/CountsPerIndex+o.IndexOffset, 1250)))
	}
	encRead := func(t float64) float64 {
		v := enc(t)
		if o.PECTable != nil && t >= o.PECFrom && t >= 20 {
			v -= float64(o.PECTable[index(t)%len(o.PECTable)])
		}
		return v
	}
	for t := 0.0; t < o.Duration; t += 0.05 {
		at := o.Start.Add(time.Duration(t * float64(time.Second)))
		step := int(math.Round(t / 0.05))
		if step%3 == 0 {
			st := 768
			if t >= 20 {
				st = 4608
			}
			s.exchange(at, 0, CmdStatus, nil, u16(st))
		}
		if step%6 == 1 {
			s.exchange(at, 0, CmdRead32, u16(Reg32Encoder), i32(int64(math.Round(encRead(t)))))
			s.exchange(at.Add(20*time.Millisecond), 1, CmdRead32, u16(Reg32Encoder), i32(5))
		}
		if o.WithIndex && step%20 == 5 {
			s.exchange(at, 0, CmdRead16, u16(Reg16Index), u16(index(t)))
		}
		if step%120 == 7 {
			s.recs = append(s.recs, rec{at, false, encode(0, s.seq[0], CmdWrite32, append(u16(11), i32(-1)...))})
			s.recs = append(s.recs, rec{at.Add(5 * time.Millisecond), true, encode(0, s.seq[0], StatusOK, nil)})
			s.seq[0]++
		}
	}
	return pcapng(s.recs)
}

type rec struct {
	at   time.Time
	in   bool
	data []byte
}

type synth struct {
	recs []rec
	seq  [2]byte
}

// exchange emits a request and a reply fragmented into 1..3 byte pieces,
// the way the USB adapter delivers them.
func (s *synth) exchange(at time.Time, axis, cmd int, req []byte, replyData []byte) {
	seq := s.seq[axis]
	s.seq[axis]++
	s.recs = append(s.recs, rec{at, false, encode(axis, seq, cmd, req)})
	reply := encode(axis, seq, StatusOK, replyData)
	t := at.Add(6 * time.Millisecond)
	for i := 0; i < len(reply); {
		n := 1 + (i*7)%3
		if i+n > len(reply) {
			n = len(reply) - i
		}
		s.recs = append(s.recs, rec{t, true, reply[i : i+n]})
		t = t.Add(time.Millisecond)
		i += n
	}
}

// encode builds a wire frame: 64 00 axis seq len16 word16 data sum16 00 5a
// with zero bytes doubled. It exists for synthetic captures only; nothing
// in pec ever sends a frame.
func encode(axis int, seq byte, word int, data []byte) []byte {
	body := []byte{0x64, 0, byte(axis), seq, 0, 0, byte(word), byte(word >> 8)}
	body = append(body, data...)
	length := len(body) + 2
	body[4], body[5] = byte(length), byte(length>>8)
	sum := 0
	for _, b := range body {
		sum += int(b)
	}
	body = append(body, byte(sum), byte(sum>>8), 0)
	var out []byte
	for i, b := range body {
		out = append(out, b)
		if b == 0 && i != len(body)-1 {
			out = append(out, 0)
		}
	}
	return append(out, 0x5a)
}

func u16(v int) []byte { return []byte{byte(v), byte(v >> 8)} }

func i32(v int64) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(int32(v)))
	return b
}

// pcapng writes a minimal file with one USBPcap interface (bus 1, device 2,
// bulk endpoint 3).
func pcapng(recs []rec) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	block := func(btype uint32, body []byte) {
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
		l := uint32(len(body) + 12)
		_ = binary.Write(&b, le, btype)
		_ = binary.Write(&b, le, l)
		b.Write(body)
		_ = binary.Write(&b, le, l)
	}
	shb := make([]byte, 16)
	le.PutUint32(shb, 0x1A2B3C4D)
	le.PutUint16(shb[4:], 1)
	for i := 8; i < 16; i++ {
		shb[i] = 0xff
	}
	block(0x0A0D0D0A, shb)
	idb := make([]byte, 8)
	le.PutUint16(idb, LinkTypeUSBPcap)
	block(1, idb)
	for _, r := range recs {
		hdr := make([]byte, 27)
		le.PutUint16(hdr, 27)
		if r.in {
			hdr[16] = 1
			hdr[21] = 0x83
		} else {
			hdr[21] = 0x03
		}
		le.PutUint16(hdr[17:], 1)
		le.PutUint16(hdr[19:], 2)
		hdr[22] = 3
		le.PutUint32(hdr[23:], uint32(len(r.data)))
		pkt := append(hdr, r.data...)
		body := make([]byte, 20)
		us := uint64(r.at.UnixMicro())
		le.PutUint32(body[4:], uint32(us>>32))
		le.PutUint32(body[8:], uint32(us))
		le.PutUint32(body[12:], uint32(len(pkt)))
		le.PutUint32(body[16:], uint32(len(pkt)))
		block(6, append(body, pkt...))
	}
	return b.Bytes()
}
