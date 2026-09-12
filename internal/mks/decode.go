package mks

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"time"
)

// Reading is one answered register query.
type Reading struct {
	At    time.Time // midpoint of request and reply
	Axis  int
	Cmd   int // CmdStatus, CmdRead16, CmdRead32
	Reg   int // register number; 0 for CmdStatus
	Value int64
}

// Capture is a decoded USB capture.
type Capture struct {
	Start, End time.Time
	Bus        uint16
	Device     uint16
	Frames     int // valid frames, both directions
	Junk       int // bytes that belonged to no valid frame
	Unpaired   int // replies with no matching request
	Readings   []Reading
	Writes     []Reading // CmdWrite32 requests, for the record
}

// Decode reads a pcap or pcapng capture and pairs every register query
// with its reply. The mount is found automatically: the device whose bulk
// traffic decodes as valid frames.
func Decode(r io.Reader) (*Capture, error) {
	type devKey struct{ bus, dev uint16 }
	type dev struct {
		out, in splitter
		frames  []Frame
	}
	devs := map[devKey]*dev{}
	err := ReadPackets(r, func(p Packet) error {
		if p.LinkType != LinkTypeUSBPcap {
			return fmt.Errorf("capture: link type %d is not USBPcap", p.LinkType)
		}
		h, err := ParseUSB(p.Data)
		if err != nil || h.Transfer != 3 || len(h.Payload) == 0 {
			return nil
		}
		// Data flows host to device on the request record of an OUT and
		// device to host on the completion record of an IN.
		if h.In() != h.FromDevice() {
			return nil
		}
		k := devKey{h.Bus, h.Device}
		d := devs[k]
		if d == nil {
			d = &dev{}
			d.in.reply = true
			devs[k] = d
		}
		if h.In() {
			d.frames = append(d.frames, d.in.feed(p.At, h.Payload)...)
		} else {
			d.frames = append(d.frames, d.out.feed(p.At, h.Payload)...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var best *dev
	var bestKey devKey
	for k, d := range devs {
		if best == nil || len(d.frames) > len(best.frames) {
			best, bestKey = d, k
		}
	}
	if best == nil || len(best.frames) < 4 {
		return nil, errNoMount
	}
	c := &Capture{Bus: bestKey.bus, Device: bestKey.dev, Frames: len(best.frames), Junk: best.in.junk + best.out.junk}
	frames := best.frames
	sort.SliceStable(frames, func(i, j int) bool { return frames[i].First.Before(frames[j].First) })
	c.Start, c.End = frames[0].First, frames[len(frames)-1].Last

	type key struct{ axis, seq int }
	pending := map[key]Frame{}
	for _, f := range frames {
		k := key{f.Axis, int(f.Seq)}
		if !f.Reply {
			pending[k] = f
			if f.Word == CmdWrite32 && len(f.Data) >= 6 {
				c.Writes = append(c.Writes, Reading{At: f.First, Axis: f.Axis, Cmd: f.Word,
					Reg: int(binary.LittleEndian.Uint16(f.Data)), Value: int64(int32(binary.LittleEndian.Uint32(f.Data[2:])))})
			}
			continue
		}
		req, ok := pending[k]
		if !ok {
			c.Unpaired++
			continue
		}
		delete(pending, k)
		if f.Word != StatusOK {
			continue
		}
		rd := Reading{At: req.First.Add(f.Last.Sub(req.First) / 2), Axis: f.Axis, Cmd: req.Word}
		switch {
		case req.Word == CmdStatus && len(f.Data) == 2:
			rd.Value = int64(binary.LittleEndian.Uint16(f.Data))
		case req.Word == CmdRead16 && len(req.Data) >= 2 && len(f.Data) == 2:
			rd.Reg = int(binary.LittleEndian.Uint16(req.Data))
			rd.Value = int64(int16(binary.LittleEndian.Uint16(f.Data)))
		case req.Word == CmdRead32 && len(req.Data) >= 2 && len(f.Data) == 4:
			rd.Reg = int(binary.LittleEndian.Uint16(req.Data))
			rd.Value = int64(int32(binary.LittleEndian.Uint32(f.Data)))
		default:
			continue
		}
		c.Readings = append(c.Readings, rd)
	}
	return c, nil
}

// Series returns the readings of one register on one axis, in time order.
func (c *Capture) Series(axis, cmd, reg int) []Reading {
	var out []Reading
	for _, r := range c.Readings {
		if r.Axis == axis && r.Cmd == cmd && r.Reg == reg {
			out = append(out, r)
		}
	}
	return out
}

// Encoder is the HA encoder series.
func (c *Capture) Encoder() []Reading { return c.Series(0, CmdRead32, Reg32Encoder) }

// Index is the HA PEC-index series (present only while the TCS window's
// Periodic Error Correction tab is showing).
func (c *Capture) Index() []Reading { return c.Series(0, CmdRead16, Reg16Index) }

// Status is the HA status-word series.
func (c *Capture) Status() []Reading { return c.Series(0, CmdStatus, 0) }
