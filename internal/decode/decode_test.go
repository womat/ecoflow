package decode

import (
	"math"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name  string
		words []uint16
		typ   Type
		wo    WordOrder
		bo    ByteOrder
		want  any
	}{
		{"uint16", []uint16{100}, U16, HighWordFirst, BigEndian, uint16(100)},
		{"raw is the untouched word", []uint16{0xA462}, Raw, HighWordFirst, BigEndian, uint16(0xA462)},
		{"int16 negative", []uint16{0xFFFB}, I16, HighWordFirst, BigEndian, int16(-5)},
		{"uint16 byte swapped", []uint16{0x0064}, U16, HighWordFirst, LittleEndian, uint16(0x6400)},

		{"uint32 high word first", []uint16{0x0001, 0x0000}, U32, HighWordFirst, BigEndian, uint32(65536)},
		{"uint32 low word first", []uint16{0x0000, 0x0001}, U32, LowWordFirst, BigEndian, uint32(65536)},
		{"int32 negative", []uint16{0xFFFF, 0xFFFB}, I32, HighWordFirst, BigEndian, int32(-5)},

		// 1.0f is 0x3F800000; the same words in the other word order are
		// the case the EcoFlow devices need.
		{"float32 high word first", []uint16{0x3F80, 0x0000}, F32, HighWordFirst, BigEndian, float32(1)},
		{"float32 low word first", []uint16{0x0000, 0x3F80}, F32, LowWordFirst, BigEndian, float32(1)},
		{"float64", []uint16{0x3FF0, 0x0000, 0x0000, 0x0000}, F64, HighWordFirst, BigEndian, float64(1)},

		{"string", []uint16{0x4543, 0x4F46}, Str, HighWordFirst, BigEndian, "ECOF"},
		{"string skips non printable", []uint16{0x4100, 0x0042}, Str, HighWordFirst, BigEndian, "AB"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(tc.words, tc.typ, tc.wo, tc.bo)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// The same words read as both word orders must disagree — otherwise the flag
// would be silently doing nothing and every float reading would be suspect.
func TestWordOrderActuallyMatters(t *testing.T) {
	words := []uint16{0x0000, 0x3F80}
	high, _ := Decode(words, F32, HighWordFirst, BigEndian)
	low, _ := Decode(words, F32, LowWordFirst, BigEndian)
	if high == low {
		t.Fatalf("word order had no effect: both decoded to %v", high)
	}
	if low.(float32) != 1 {
		t.Fatalf("low word first: got %v, want 1", low)
	}
	if f := float64(high.(float32)); !math.IsInf(f, 0) && f > 1e-30 {
		t.Fatalf("high word first: got %v, expected a denormal-ish value", f)
	}
}

func TestDecodeWrongRegisterCount(t *testing.T) {
	if _, err := Decode([]uint16{1}, F32, HighWordFirst, BigEndian); err == nil {
		t.Fatal("expected an error for a float32 built from one register")
	}
}

func TestRegsPerValue(t *testing.T) {
	for typ, want := range map[Type]int{Raw: 1, U16: 1, I16: 1, U32: 2, I32: 2, F32: 2, F64: 4, Str: 1} {
		if got := RegsPerValue(typ); got != want {
			t.Errorf("%s: got %d, want %d", typ, got, want)
		}
	}
}

func TestParseAddr(t *testing.T) {
	ok := map[string]uint16{
		"42082":  42082,
		"0xA462": 42082,
		"0xa462": 42082,
		"0X1":    1,
		"0":      0,
		"65535":  65535,
		// Base 10 is enforced: base-0 parsing would read this as octal 34.
		"042": 42,
	}
	for in, want := range ok {
		got, err := ParseAddr(in)
		if err != nil {
			t.Errorf("ParseAddr(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseAddr(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"", "0x", "0xZZ", "12a", "-1", "65536", "1_0"} {
		if got, err := ParseAddr(in); err == nil {
			t.Errorf("ParseAddr(%q) = %d, want an error", in, got)
		}
	}
}

func TestParseType(t *testing.T) {
	if got, err := ParseType("UINT16"); err != nil || got != U16 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := ParseType("word"); err == nil {
		t.Fatal("expected an error for an unknown type")
	}
}

func TestParseOrders(t *testing.T) {
	if wo, err := ParseWordOrder("low"); err != nil || wo != LowWordFirst {
		t.Fatalf("got %v, %v", wo, err)
	}
	if bo, err := ParseByteOrder("little"); err != nil || bo != LittleEndian {
		t.Fatalf("got %v, %v", bo, err)
	}
	if _, err := ParseWordOrder("lo"); err == nil {
		t.Fatal("expected an error")
	}
}
