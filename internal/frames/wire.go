// Package frames decodes the protobuf frames an EcoFlow PowerOcean publishes
// on the consumer app's MQTT channel.
//
// Like internal/decode it is deliberately free of network and device access:
// everything here is a pure function over []byte. That matters for the same
// reason it matters there — a misread field produces a plausible wrong number
// rather than a crash, so the interpretation has to be directly testable.
//
// Nothing in here comes from EcoFlow documentation. The frame layout, the
// obfuscation and the field numbering were measured at a PowerOcean DC Fit in
// September 2026 and are written up in api-status.md. Other models number the
// same quantities differently.
package frames

import (
	"encoding/binary"
	"fmt"
	"math"
)

// field is one entry of a protobuf message on the wire.
type field struct {
	number uint64
	wire   uint8
	varint uint64 // wire type 0
	bytes  []byte // wire type 2
	fixed  []byte // wire types 1 and 5
}

// Protobuf wire types, as far as these frames use them.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// readVarint reads one base-128 varint and returns the offset after it.
func readVarint(b []byte, i int) (uint64, int, bool) {
	var value uint64
	for shift := uint(0); ; shift += 7 {
		if i >= len(b) || shift > 63 {
			return 0, i, false
		}
		c := b[i]
		i++
		value |= uint64(c&0x7F) << shift
		if c&0x80 == 0 {
			return value, i, true
		}
	}
}

// parse reads one protobuf message.
//
// A frame that stops making sense halfway is not an error worth a name: these
// are captured bytes from an undocumented protocol, and the caller decides
// what a missing field means. So parsing yields what it could read and stops.
func parse(b []byte) []field {
	var out []field
	for i := 0; i < len(b); {
		key, next, ok := readVarint(b, i)
		if !ok {
			return out
		}
		i = next

		f := field{number: key >> 3, wire: uint8(key & 7)}
		switch f.wire {
		case wireVarint:
			if f.varint, i, ok = readVarint(b, i); !ok {
				return out
			}
		case wireBytes:
			var n uint64
			if n, i, ok = readVarint(b, i); !ok || uint64(len(b)-i) < n {
				return out
			}
			f.bytes, i = b[i:i+int(n)], i+int(n)
		case wireFixed32:
			if len(b)-i < 4 {
				return out
			}
			f.fixed, i = b[i:i+4], i+4
		case wireFixed64:
			if len(b)-i < 8 {
				return out
			}
			f.fixed, i = b[i:i+8], i+8
		default:
			return out
		}
		out = append(out, f)
	}
	return out
}

// float32At returns the little-endian float in a fixed32 field.
func (f field) float32At() float32 {
	if f.wire != wireFixed32 || len(f.fixed) != 4 {
		return 0
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(f.fixed))
}

// find returns the first field with this number, and whether there was one.
func find(fs []field, number uint64) (field, bool) {
	for _, f := range fs {
		if f.number == number {
			return f, true
		}
	}
	return field{}, false
}

// varints reads a run of varints laid end to end, the way the hourly report
// stores its 24 values.
func varints(b []byte) ([]uint64, error) {
	var out []uint64
	for i := 0; i < len(b); {
		v, next, ok := readVarint(b, i)
		if !ok {
			return nil, fmt.Errorf("truncated varint at byte %d of %d", i, len(b))
		}
		out, i = append(out, v), next
	}
	return out, nil
}
