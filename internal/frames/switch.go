package frames

import "fmt"

// MaxSwitchSeq is the largest sequence number BuildStreamSwitch accepts.
//
// Past this a varint needs a second byte, which the app's own frames never
// showed. Wrapping instead of growing keeps the frame the length that was
// measured.
const MaxSwitchSeq = 127

// BuildStreamSwitch returns the message that turns on the device's fast stream.
//
// This is the one message anything here sends to a .../set topic, the topic
// through which a device can actually be changed. It is not derived from
// anyone's notes: it was captured off the wire by subscribing to that topic
// while operating the phone app, and these are those bytes, varying only the
// sequence number and the serial.
//
// That distinction earned itself. A version assembled from third-party notes
// came out wrong in four places — an obfuscated payload where the real one is
// plain, the wrong nesting, a six-digit sequence number where the app uses
// single digits, two invented fields and two missing ones.
//
// Field by field, as measured:
//
//	 1 pdata      08 01 10 01   two flags, both 1 - plain, not obfuscated
//	 2 src        32            the app
//	 3 dest       96            the energy management unit
//	 4 dSrc       1
//	 5 dDest      1
//	 7 (unknown)  3
//	 8 cmd_func   96
//	 9 cmd_id     97
//	10 dataLen    4
//	11 needAck    1
//	14 seq        a small counter
//	16 version    3
//	17 payloadVer 1
//	23 from       "ios" - what the captured app called itself
//	25 sn         the serial number as ASCII
func BuildStreamSwitch(serial string, seq int) ([]byte, error) {
	if seq < 1 || seq > MaxSwitchSeq {
		return nil, fmt.Errorf("sequence %d out of range (1-%d)", seq, MaxSwitchSeq)
	}
	if serial == "" || len(serial) > 60 {
		return nil, fmt.Errorf("serial %q has an unusable length", serial)
	}
	if !printable([]byte(serial)) {
		return nil, fmt.Errorf("serial %q is not printable ASCII", serial)
	}

	var inner []byte
	inner = appendBytes(inner, 1, []byte{0x08, 0x01, 0x10, 0x01})
	inner = appendVarint(inner, 2, 32)
	inner = appendVarint(inner, 3, 96)
	inner = appendVarint(inner, 4, 1)
	inner = appendVarint(inner, 5, 1)
	inner = appendVarint(inner, 7, 3)
	inner = appendVarint(inner, 8, 96)
	inner = appendVarint(inner, 9, 97)
	inner = appendVarint(inner, 10, 4)
	inner = appendVarint(inner, 11, 1)
	inner = appendVarint(inner, 14, uint64(seq))
	inner = appendVarint(inner, 16, 3)
	inner = appendVarint(inner, 17, 1)
	inner = appendBytes(inner, 23, []byte("ios"))
	inner = appendBytes(inner, 25, []byte(serial))

	return appendBytes(nil, 1, inner), nil
}

func appendKey(b []byte, number uint64, wire uint8) []byte {
	return appendUvarint(b, number<<3|uint64(wire))
}

func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendVarint(b []byte, number, value uint64) []byte {
	return appendUvarint(appendKey(b, number, wireVarint), value)
}

func appendBytes(b []byte, number uint64, value []byte) []byte {
	b = appendKey(b, number, wireBytes)
	b = appendUvarint(b, uint64(len(value)))
	return append(b, value...)
}
