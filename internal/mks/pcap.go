// Package mks decodes the serial protocol between TheSkyX and a Paramount
// ME's MKS 4000 controller from a passive USB capture (USBPcap, saved by
// Wireshark or USBPcapCMD). It is read-only by construction: it parses
// files and never opens a port.
//
// What is known of the protocol was inferred from one capture on
// 2026-09-12 and is documented on the types below; anything not listed is
// unknown and left as raw bytes.
package mks

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// LinkTypeUSBPcap is the pcap link type for USBPcap frames.
const LinkTypeUSBPcap = 249

// Packet is one captured USB frame with its capture-clock timestamp.
type Packet struct {
	At       time.Time
	LinkType uint32
	Data     []byte
}

// ReadPackets streams every packet from a pcap or pcapng file to fn. It
// reads incrementally, so captures of hundreds of megabytes are fine.
func ReadPackets(r io.Reader, fn func(Packet) error) error {
	br := bufio.NewReaderSize(r, 1<<20)
	magic, err := br.Peek(4)
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	switch binary.LittleEndian.Uint32(magic) {
	case 0x0A0D0D0A:
		return readPcapng(br, fn)
	case 0xa1b2c3d4, 0xa1b23c4d:
		return readPcap(br, binary.LittleEndian, binary.LittleEndian.Uint32(magic) == 0xa1b23c4d, fn)
	case 0xd4c3b2a1, 0x4d3cb2a1:
		return readPcap(br, binary.BigEndian, binary.LittleEndian.Uint32(magic) == 0x4d3cb2a1, fn)
	}
	return errors.New("capture: not a pcap or pcapng file")
}

// readPcap handles the classic format USBPcapCMD writes.
func readPcap(br *bufio.Reader, bo binary.ByteOrder, nanos bool, fn func(Packet) error) error {
	hdr := make([]byte, 24)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return fmt.Errorf("pcap header: %w", err)
	}
	link := bo.Uint32(hdr[20:])
	rec := make([]byte, 16)
	for {
		if _, err := io.ReadFull(br, rec); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("pcap record: %w", err)
		}
		sec, frac, incl := bo.Uint32(rec), bo.Uint32(rec[4:]), bo.Uint32(rec[8:])
		if incl > 1<<26 {
			return errors.New("pcap: implausible record length")
		}
		data := make([]byte, incl)
		if _, err := io.ReadFull(br, data); err != nil {
			return fmt.Errorf("pcap record: %w", err)
		}
		ns := int64(frac) * 1000
		if nanos {
			ns = int64(frac)
		}
		if err := fn(Packet{At: time.Unix(int64(sec), ns).UTC(), LinkType: link, Data: data}); err != nil {
			return err
		}
	}
}

// readPcapng handles what Wireshark saves. Only the interface description
// (link type, timestamp resolution) and enhanced packet blocks matter.
func readPcapng(br *bufio.Reader, fn func(Packet) error) error {
	type iface struct {
		link  uint32
		scale float64 // seconds per timestamp unit
	}
	var ifaces []iface
	bo := binary.ByteOrder(binary.LittleEndian)
	head := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, head); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("pcapng block: %w", err)
		}
		btype := binary.LittleEndian.Uint32(head)
		if btype == 0x0A0D0D0A {
			// Section header: the byte-order magic decides the endianness
			// of everything that follows, including this block's length.
			bom := make([]byte, 4)
			if _, err := io.ReadFull(br, bom); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(bom) == 0x1A2B3C4D {
				bo = binary.LittleEndian
			} else {
				bo = binary.BigEndian
			}
			blen := bo.Uint32(head[4:])
			if blen < 12 || blen > 1<<20 {
				return errors.New("pcapng: implausible section header length")
			}
			rest := make([]byte, blen-12)
			if _, err := io.ReadFull(br, rest); err != nil {
				return err
			}
			ifaces = ifaces[:0]
			continue
		}
		blen := bo.Uint32(head[4:])
		if blen < 12 || blen > 1<<26 {
			return errors.New("pcapng: implausible block length")
		}
		body := make([]byte, blen-8)
		if _, err := io.ReadFull(br, body); err != nil {
			return fmt.Errorf("pcapng block: %w", err)
		}
		body = body[:len(body)-4] // trailing block length
		switch bo.Uint32(head) {
		case 1: // interface description
			if len(body) < 8 {
				return errors.New("pcapng: short interface block")
			}
			f := iface{link: uint32(bo.Uint16(body)), scale: 1e-6}
			for o := 8; o+4 <= len(body); {
				code, ln := bo.Uint16(body[o:]), int(bo.Uint16(body[o+2:]))
				if code == 0 {
					break
				}
				if code == 9 && ln >= 1 { // if_tsresol
					v := body[o+4]
					if v&0x80 != 0 {
						f.scale = 1 / float64(uint64(1)<<(v&0x7f))
					} else {
						f.scale = 1
						for i := 0; i < int(v); i++ {
							f.scale /= 10
						}
					}
				}
				o += 4 + (ln+3)&^3
			}
			ifaces = append(ifaces, f)
		case 6: // enhanced packet
			if len(body) < 20 {
				return errors.New("pcapng: short packet block")
			}
			id := bo.Uint32(body)
			if int(id) >= len(ifaces) {
				return errors.New("pcapng: packet for unknown interface")
			}
			ts := uint64(bo.Uint32(body[4:]))<<32 | uint64(bo.Uint32(body[8:]))
			capLen := bo.Uint32(body[12:])
			if int(capLen) > len(body)-20 {
				return errors.New("pcapng: packet longer than block")
			}
			secs := float64(ts) * ifaces[id].scale
			whole := int64(secs)
			at := time.Unix(whole, int64((secs-float64(whole))*1e9)).UTC()
			if err := fn(Packet{At: at, LinkType: ifaces[id].link, Data: body[20 : 20+capLen]}); err != nil {
				return err
			}
		}
	}
}

// USBHeader is the USBPcap pseudo-header in front of each frame.
type USBHeader struct {
	Status   uint32
	Function uint16
	Info     uint8 // bit 0: 1 = device to host (completion), 0 = host to device (request)
	Bus      uint16
	Device   uint16
	Endpoint uint8 // bit 7 set = IN
	Transfer uint8 // 0 isochronous, 1 interrupt, 2 control, 3 bulk
	Payload  []byte
}

// In reports the direction of the endpoint.
func (h USBHeader) In() bool { return h.Endpoint&0x80 != 0 }

// FromDevice reports whether this record is the completion (data flowing
// device to host) rather than the request.
func (h USBHeader) FromDevice() bool { return h.Info&1 != 0 }

// ParseUSB splits the USBPcap header from the payload.
func ParseUSB(data []byte) (USBHeader, error) {
	if len(data) < 27 {
		return USBHeader{}, errors.New("usbpcap: short header")
	}
	hlen := int(binary.LittleEndian.Uint16(data))
	h := USBHeader{
		Status:   binary.LittleEndian.Uint32(data[10:]),
		Function: binary.LittleEndian.Uint16(data[14:]),
		Info:     data[16],
		Bus:      binary.LittleEndian.Uint16(data[17:]),
		Device:   binary.LittleEndian.Uint16(data[19:]),
		Endpoint: data[21],
		Transfer: data[22],
	}
	dlen := int(binary.LittleEndian.Uint32(data[23:]))
	if hlen > len(data) || hlen+dlen > len(data) {
		return USBHeader{}, errors.New("usbpcap: header or payload longer than record")
	}
	h.Payload = data[hlen : hlen+dlen]
	return h, nil
}
