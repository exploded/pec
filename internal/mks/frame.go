package mks

import (
	"errors"
	"time"
)

// Frame is one decoded MKS message. On the wire (as seen in the capture):
//
//	64 | 00 | axis | seq | len16 | cmd16 (or status16 in a reply) | data... | sum16 | 00 | 5a
//
// where every 0x00 byte between the 0x64 and the 0x5a is sent doubled, the
// sum is the 16-bit sum of the decoded bytes from 0x64 up to the data, and
// len16 counts the decoded bytes from 0x64 through the checksum. The
// sequence byte increments per request and the reply carries it back, so
// replies can be paired with requests.
type Frame struct {
	First, Last time.Time // capture clock of the first and last byte
	Axis        int       // 0 = HA/RA, 1 = Dec
	Seq         byte
	Word        int // command (request) or status (reply)
	Data        []byte
	Reply       bool
}

// Command words seen in the capture.
const (
	CmdStatus  = 3   // no data; reply carries a u16 status word
	CmdRead16  = 200 // data: u16 register; reply u16 value
	CmdRead32  = 210 // data: u16 register; reply i32 value
	CmdWrite32 = 211 // data: u16 register, i32 value; reply empty
	StatusOK   = 1   // status word of a normal reply

	frameMin     = 8
	frameMaxScan = 512
)

// Register numbers observed on axis 0 (HA). Names come from the Bisque TCS
// "Show Status" tab, whose cells match these values.
const (
	Reg32Position = 4  // "Current Position": creeps about -0.15/s while tracking
	Reg32Encoder  = 10 // "Current Encoder": 133.5 counts/s while tracking
	Reg16Motor    = 16 // "Motor" 4000
	Reg16Index    = 9  // polled while the PEC tab is showing; consistent with the PEC index
)

// splitter reassembles a byte stream (which USB delivers in 1..64 byte
// pieces) into validated frames.
type splitter struct {
	buf   []timedByte
	reply bool
	junk  int
}

type timedByte struct {
	at time.Time
	b  byte
}

// feed appends bytes and returns every complete frame now available.
func (s *splitter) feed(at time.Time, data []byte) []Frame {
	for _, b := range data {
		s.buf = append(s.buf, timedByte{at, b})
	}
	var out []Frame
	for {
		f, used, ok := s.next()
		if used == 0 {
			break
		}
		s.buf = s.buf[used:]
		if ok {
			out = append(out, f)
		}
	}
	return out
}

// next tries to decode a frame at the start of the buffer. It returns the
// number of bytes to consume (0 = wait for more data).
func (s *splitter) next() (Frame, int, bool) {
	if len(s.buf) == 0 {
		return Frame{}, 0, false
	}
	if s.buf[0].b != 0x64 {
		s.junk++
		return Frame{}, 1, false
	}
	for j := 1; j < len(s.buf) && j < frameMaxScan; j++ {
		if s.buf[j].b != 0x5a {
			continue
		}
		raw := make([]byte, j)
		for i := range raw {
			raw[i] = s.buf[i].b
		}
		body, ok := unstuff(raw)
		if !ok || len(body) < frameMin+3 {
			continue
		}
		f, ok := parseBody(body)
		if !ok {
			continue
		}
		f.First, f.Last, f.Reply = s.buf[0].at, s.buf[j].at, s.reply
		return f, j + 1, true
	}
	// No valid frame yet. Give up on this start byte when the buffer already
	// holds more than the length field allows for, or when the header is
	// malformed; otherwise wait for more data.
	maxRaw, ok := expectedMax(s.buf)
	if !ok || len(s.buf) > maxRaw || len(s.buf) >= frameMaxScan {
		s.junk++
		return Frame{}, 1, false
	}
	return Frame{}, 0, false
}

// expectedMax reads the len16 field through the zero stuffing and returns
// the most raw bytes a frame of that length can occupy. ok is false when
// the header is malformed (a lone zero where a doubled one must be); a
// short buffer returns a large bound so the caller waits.
func expectedMax(buf []timedByte) (int, bool) {
	dec := 0
	var length int
	for i := 0; i < len(buf) && dec < 6; i++ {
		b := buf[i].b
		if b == 0 {
			if i+1 >= len(buf) {
				return frameMaxScan, true
			}
			if buf[i+1].b != 0 {
				return 0, false
			}
			i++
		}
		switch dec {
		case 1:
			if b != 0 { // the byte after 0x64 is always zero
				return 0, false
			}
		case 4:
			length = int(b)
		case 5:
			length |= int(b) << 8
		}
		dec++
	}
	if dec < 6 {
		return frameMaxScan, true
	}
	if length < frameMin+2 || length > frameMaxScan/2 {
		return 0, false
	}
	return 2*length + 4, true
}

// unstuff collapses doubled zeros. A single trailing zero is the pad before
// the 0x5a terminator.
func unstuff(raw []byte) ([]byte, bool) {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		out = append(out, b)
		if b == 0 {
			switch {
			case i+1 < len(raw) && raw[i+1] == 0:
				i++
			case i == len(raw)-1:
			default:
				return nil, false
			}
		}
	}
	return out, true
}

// parseBody validates the length and checksum of a decoded body
// (64 00 axis seq len16 word16 data... sum16 00).
func parseBody(body []byte) (Frame, bool) {
	n := len(body)
	if body[n-1] != 0 || body[1] != 0 {
		return Frame{}, false
	}
	payload := body[:n-3]
	sum := int(body[n-3]) | int(body[n-2])<<8
	want := 0
	for _, b := range payload {
		want += int(b)
	}
	if want&0xffff != sum {
		return Frame{}, false
	}
	length := int(body[4]) | int(body[5])<<8
	if length != len(payload)+2 {
		return Frame{}, false
	}
	return Frame{
		Axis: int(body[2]), Seq: body[3],
		Word: int(body[6]) | int(body[7])<<8,
		Data: append([]byte(nil), payload[8:]...),
	}, true
}

// errNoMount is returned when no device in the capture speaks the protocol.
var errNoMount = errors.New("no MKS traffic found in the capture (is the mount's USB adapter on the captured root hub?)")
