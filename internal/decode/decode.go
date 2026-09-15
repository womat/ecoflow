// Package decode turns raw Modbus register words into typed values.
//
// It is deliberately free of any network or device specifics: everything here
// is a pure function over []uint16, which keeps the interpretation of a
// register — the part that silently produces wrong numbers instead of
// crashing — directly testable.
package decode

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Type is the interpretation applied to one or more registers.
type Type string

const (
	Raw Type = "raw"
	U16 Type = "uint16"
	I16 Type = "int16"
	U32 Type = "uint32"
	I32 Type = "int32"
	F32 Type = "float32"
	F64 Type = "float64"
	Str Type = "string"
)

// Types lists every supported type, in the order shown in the usage text.
var Types = []Type{Raw, U16, I16, U32, I32, F32, F64, Str}

// ParseType maps a command line argument to a Type.
func ParseType(s string) (Type, error) {
	for _, t := range Types {
		if strings.EqualFold(s, string(t)) {
			return t, nil
		}
	}
	names := make([]string, len(Types))
	for i, t := range Types {
		names[i] = string(t)
	}
	return "", fmt.Errorf("unknown type %q (known: %s)", s, strings.Join(names, ", "))
}

// WordOrder is the order of registers within a multi-register value.
type WordOrder int

const (
	HighWordFirst WordOrder = iota
	LowWordFirst
)

func ParseWordOrder(s string) (WordOrder, error) {
	switch strings.ToLower(s) {
	case "high":
		return HighWordFirst, nil
	case "low":
		return LowWordFirst, nil
	}
	return 0, fmt.Errorf("unknown word order %q (known: high, low)", s)
}

// ByteOrder is the order of the two bytes within a single register.
type ByteOrder int

const (
	BigEndian ByteOrder = iota
	LittleEndian
)

func ParseByteOrder(s string) (ByteOrder, error) {
	switch strings.ToLower(s) {
	case "big":
		return BigEndian, nil
	case "little":
		return LittleEndian, nil
	}
	return 0, fmt.Errorf("unknown byte order %q (known: big, little)", s)
}

// RegsPerValue is how many registers one value of type t occupies.
// For Str the whole block read is a single value, so the caller decides the
// length; RegsPerValue reports 1 to keep the arithmetic uniform.
func RegsPerValue(t Type) int {
	switch t {
	case U32, I32, F32:
		return 2
	case F64:
		return 8 / 2
	default:
		return 1
	}
}

// ParseAddr parses a register address, decimal by default and hexadecimal
// with a 0x prefix.
//
// Base 10 is enforced rather than using strconv's base-0 auto-detection on
// purpose: base 0 reads "042" as octal 34, which would be a silent
// misinterpretation in the one place this tool has to be exact.
func ParseAddr(s string) (uint16, error) {
	digits := strings.TrimSpace(s)
	base := 10
	if strings.HasPrefix(digits, "0x") || strings.HasPrefix(digits, "0X") {
		base, digits = 16, digits[2:]
	}
	if digits == "" {
		return 0, fmt.Errorf("invalid address %q", s)
	}
	v, err := strconv.ParseUint(digits, base, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid address %q (decimal, or hexadecimal with 0x prefix)", s)
	}
	if v > 0xFFFF {
		return 0, fmt.Errorf("address %q out of range (0-65535)", s)
	}
	return uint16(v), nil
}

// Decode interprets words as a single value of type t.
//
// The returned value is one of uint16, int16, uint32, int32, float32,
// float64 or string, matching t.
func Decode(words []uint16, t Type, wo WordOrder, bo ByteOrder) (any, error) {
	if t == Str {
		if len(words) == 0 {
			return nil, fmt.Errorf("string needs at least one register")
		}
		return decodeString(words, bo), nil
	}
	if want := RegsPerValue(t); len(words) != want {
		return nil, fmt.Errorf("type %s needs %d register(s), got %d", t, want, len(words))
	}

	b := toBytes(words, wo, bo)
	switch t {
	case Raw, U16:
		return binary.BigEndian.Uint16(b), nil
	case I16:
		return int16(binary.BigEndian.Uint16(b)), nil
	case U32:
		return binary.BigEndian.Uint32(b), nil
	case I32:
		return int32(binary.BigEndian.Uint32(b)), nil
	case F32:
		return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
	case F64:
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	}
	return nil, fmt.Errorf("unhandled type %s", t)
}

// toBytes flattens words into a big-endian byte slice, applying the word and
// byte order first. Single-register types are unaffected by the word order.
func toBytes(words []uint16, wo WordOrder, bo ByteOrder) []byte {
	w := make([]uint16, len(words))
	copy(w, words)
	if wo == LowWordFirst {
		for i, j := 0, len(w)-1; i < j; i, j = i+1, j-1 {
			w[i], w[j] = w[j], w[i]
		}
	}
	b := make([]byte, 0, len(w)*2)
	for _, x := range w {
		if bo == LittleEndian {
			b = append(b, byte(x), byte(x>>8))
		} else {
			b = append(b, byte(x>>8), byte(x))
		}
	}
	return b
}

// decodeString reads the words as ASCII and keeps printable characters only.
// The word order is not applied: text runs from the first register onwards.
func decodeString(words []uint16, bo ByteOrder) string {
	var sb strings.Builder
	for _, c := range toBytes(words, HighWordFirst, bo) {
		if c >= 32 && c <= 126 {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}
