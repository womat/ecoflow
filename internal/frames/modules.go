package frames

import (
	"fmt"
	"unicode"
)

// Module is one part of the installation, as the device lists it.
type Module struct {
	Kind   string
	Serial string
}

// moduleKinds maps the protobuf field number to the type the portal gives for
// the same serial under System information, so these names are read off
// EcoFlow's own interface rather than guessed from the serial prefixes.
var moduleKinds = map[uint64]string{
	1: "system",
	2: "converter",
	3: "battery",
}

// Modules decodes the parts list.
//
// It is byte for byte identical across a whole capture, so it is an inventory
// rather than a measurement, and it only arrives while the fast stream is
// being switched on.
func (f Frame) Modules() ([]Module, bool) {
	if f.Command != Modules {
		return nil, false
	}

	var out []Module
	for _, entry := range parse(f.Payload) {
		if entry.wire != wireBytes {
			continue
		}
		serial, ok := find(parse(entry.bytes), 1)
		if !ok || serial.wire != wireBytes || !printable(serial.bytes) {
			continue
		}
		kind, ok := moduleKinds[entry.number]
		if !ok {
			kind = fmt.Sprintf("field %d", entry.number)
		}
		out = append(out, Module{Kind: kind, Serial: string(serial.bytes)})
	}
	return out, len(out) > 0
}

func printable(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c > unicode.MaxASCII || !unicode.IsPrint(rune(c)) {
			return false
		}
	}
	return true
}
