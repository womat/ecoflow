package frames

import (
	"errors"
	"fmt"
)

// Header field numbers, measured at the device.
const (
	fieldPayload = 1
	fieldCmdFunc = 8
	fieldCmdID   = 9
	fieldSeq     = 14
)

// Command identifies a report. Every frame seen so far carries Func 96 except
// the hourly history, which uses 254.
type Command struct {
	Func uint64
	ID   uint64
}

// The commands this package understands. The two energy reports carry the same
// fields but arrive differently: Minutely comes on its own once a minute with
// its timestamp rounded to the minute, Fast every two to three seconds with a
// timestamp to the second, but only while something keeps the stream switched
// on. See api-status.md.
var (
	Fast     = Command{96, 33}
	Minutely = Command{96, 34}
	Modules  = Command{96, 3}
	Ack      = Command{96, 137}
	Hourly   = Command{254, 32}
)

func (c Command) String() string { return fmt.Sprintf("%d/%d", c.Func, c.ID) }

// Frame is one decoded message from the device.
type Frame struct {
	Command Command
	Seq     uint64
	Payload []byte // already deobfuscated
}

// ErrNotAFrame reports bytes that do not carry the expected wrapper.
var ErrNotAFrame = errors.New("not a device frame")

// Parse reads one frame and undoes the obfuscation of its payload.
//
// The payload is XORed with the low byte of the sequence number. That showed
// up because two frames one sequence apart differed by the same bit pattern in
// every byte; without undoing it the payload is not valid protobuf at all.
//
// It applies in this direction only. What the app sends to the device is
// plain — see BuildStreamSwitch.
func Parse(b []byte) (Frame, error) {
	outer, ok := find(parse(b), 1)
	if !ok || outer.wire != wireBytes {
		return Frame{}, ErrNotAFrame
	}
	header := parse(outer.bytes)

	cmdFunc, okFunc := find(header, fieldCmdFunc)
	cmdID, okID := find(header, fieldCmdID)
	if !okFunc || !okID {
		return Frame{}, ErrNotAFrame
	}

	f := Frame{
		Command: Command{Func: cmdFunc.varint, ID: cmdID.varint},
	}
	if seq, ok := find(header, fieldSeq); ok {
		f.Seq = seq.varint
	}

	if payload, ok := find(header, fieldPayload); ok && payload.wire == wireBytes {
		key := byte(f.Seq & 0xFF)
		f.Payload = make([]byte, len(payload.bytes))
		for i, c := range payload.bytes {
			f.Payload[i] = c ^ key
		}
	}
	return f, nil
}
