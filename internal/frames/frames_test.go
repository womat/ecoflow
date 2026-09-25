package frames

import (
	"bufio"
	"encoding/hex"
	"math"
	"os"
	"strings"
	"testing"
)

// readCapture yields the frames of a capture file.
//
// The files hold the output of "ecoflow-api.sh live", one line per message:
// timestamp, topic, length, payload as hex. They are real captures from a
// PowerOcean DC Fit with the serial numbers replaced by placeholders of the
// same length, which leaves every length field and the obfuscation intact.
// Synthetic frames would only prove what was assumed while writing them.
func readCapture(t *testing.T, name string) [][]byte {
	t.Helper()

	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open capture: %v", err)
	}
	defer f.Close()

	var out [][]byte
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		parts := strings.Fields(s.Text())
		if len(parts) < 4 {
			continue
		}
		b, err := hex.DecodeString(parts[3])
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("capture %s held no frames", name)
	}
	return out
}

func TestParseCapture(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		want  map[Command]int
		least int
	}{
		{
			name: "fast stream running",
			file: "fast.txt",
			want: map[Command]int{Fast: 136, Minutely: 8, Modules: 83, Ack: 75},
		},
		{
			name: "subscription only",
			file: "slow.txt",
			// Nothing was published during this capture, and the fast stream
			// never appeared: the device keeps the minute cadence on its own.
			want: map[Command]int{Fast: 0, Minutely: 47, Modules: 0, Ack: 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := map[Command]int{}
			for _, b := range readCapture(t, tc.file) {
				f, err := Parse(b)
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				got[f.Command]++
			}
			for cmd, want := range tc.want {
				if got[cmd] != want {
					t.Errorf("%v: got %d frames, want %d", cmd, got[cmd], want)
				}
			}
		})
	}
}

// TestEnergyBalances is the check that established the field numbering in the
// first place, and the one that would catch a firmware renumbering them: the
// power flows have to add up on every single frame.
func TestEnergyBalances(t *testing.T) {
	for _, file := range []string{"fast.txt", "slow.txt"} {
		t.Run(file, func(t *testing.T) {
			var n int
			for _, b := range readCapture(t, file) {
				f, err := Parse(b)
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				e, ok := f.Energy()
				if !ok {
					continue
				}
				n++
				if d := math.Abs(float64(e.Balance())); d > 0.01 {
					t.Errorf("frame at %v: balance off by %.4f W", e.Measured, d)
				}
				if e.Measured.IsZero() {
					t.Errorf("frame with no timestamp: %+v", e)
				}
			}
			if n == 0 {
				t.Fatal("no energy reports in capture")
			}
			t.Logf("%d energy reports, all balanced", n)
		})
	}
}

// TestAbsentMeansZero pins why a missing power field is read as 0 and not as
// "unknown". The device leaves out a field whose value is zero - protobuf's
// default - and never sends an explicit 0.0: in the captures grid is absent
// from about half the reports, and on every one of those the balance closes
// with grid taken as 0. A firmware that starts sending zeros, or that drops
// fields for another reason, shows up here.
func TestAbsentMeansZero(t *testing.T) {
	powerFields := []uint64{energyGrid, energyDCDC, energyBattery, energyPV, energyHouse}

	var absent int
	for _, file := range []string{"fast.txt", "slow.txt"} {
		for _, b := range readCapture(t, file) {
			f, err := Parse(b)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			e, ok := f.Energy()
			if !ok {
				continue
			}

			body := parse(f.Payload)
			if first, ok := find(body, 1); ok && first.wire == wireBytes {
				body = parse(first.bytes)
			}

			for _, n := range powerFields {
				if fl, ok := find(body, n); ok && fl.float32At() == 0 {
					t.Errorf("%s at %v: field %d is on the wire as an explicit 0", file, e.Measured, n)
				}
			}

			if _, ok := find(body, energyGrid); ok {
				continue
			}
			absent++
			if e.Grid != 0 {
				t.Errorf("%s at %v: grid absent but read as %v", file, e.Measured, e.Grid)
			}
			if d := math.Abs(float64(e.Balance())); d > 0.01 {
				t.Errorf("%s at %v: grid absent and the balance is off by %.4f W", file, e.Measured, d)
			}
		}
	}
	if absent == 0 {
		t.Fatal("no report without grid; the captures no longer show the case this test is about")
	}
	t.Logf("%d reports without grid, all balanced with grid = 0", absent)
}

// TestFastAndMinutelyAgree checks the two packings against each other. Every
// minutely report shares its timestamp with a fast one, so where both exist
// they have to describe the same moment.
func TestFastAndMinutelyAgree(t *testing.T) {
	fast := map[int64]Energy{}
	var minutely []Energy

	for _, b := range readCapture(t, "fast.txt") {
		f, err := Parse(b)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		e, ok := f.Energy()
		if !ok {
			continue
		}
		switch f.Command {
		case Fast:
			fast[e.Measured.Unix()] = e
		case Minutely:
			minutely = append(minutely, e)
		}
	}

	if len(minutely) == 0 {
		t.Fatal("no minutely reports in capture")
	}
	for _, m := range minutely {
		if _, ok := fast[m.Measured.Unix()]; !ok {
			t.Errorf("minutely report at %v has no fast report at the same second",
				m.Measured)
		}
		if m.Measured.Second() != 0 {
			t.Errorf("minutely timestamp %v is not on the minute", m.Measured)
		}
	}
}

// TestHourlyBalances checks the other arithmetic: what flows into the house
// over an hour equals what flows out of it.
func TestHourlyBalances(t *testing.T) {
	parts := map[Flow]HourlyPart{}
	var when int64

	for _, b := range readCapture(t, "fast.txt") {
		f, err := Parse(b)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		p, ok := f.Hourly()
		if !ok {
			continue
		}
		if p.When.Unix() != when {
			when, parts = p.When.Unix(), map[Flow]HourlyPart{}
		}
		parts[p.Flow] = p
	}

	if len(parts) != len(Flows) {
		t.Fatalf("got %d flows in the last hourly report, want %d", len(parts), len(Flows))
	}
	for _, p := range parts {
		if len(p.Hours) != HoursPerDay {
			t.Errorf("%v: got %d hours, want %d", p.Flow, len(p.Hours), HoursPerDay)
		}
	}
	for hour := range HoursPerDay {
		if d := HourlyBalance(parts, hour); d < -1 || d > 1 {
			t.Errorf("hour %d: balance off by %d Wh", hour, d)
		}
	}
	if total := parts[FlowPV].Total(); total == 0 {
		t.Error("PV total for the day is zero, which the capture is not")
	}
}

func TestModules(t *testing.T) {
	var got []Module
	for _, b := range readCapture(t, "fast.txt") {
		f, err := Parse(b)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if m, ok := f.Modules(); ok {
			got = m
			break
		}
	}

	want := []Module{
		{"system", "HC31XXXXXXXXXXXX"},
		{"converter", "HC31YYYYYYYYYYYY"},
		{"battery", "HJ3AXXXXXXXXXXXX"},
		{"battery", "HJ3AYYYYYYYYYYYY"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d modules, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("module %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestBuildStreamSwitch compares the built frame with the one captured off the
// wire while the phone app was running. Byte for byte, because that is the
// only way to know it is the app's message and not a plausible imitation.
//
// The capture itself ended in the real serial; here it carries the placeholder
// of the same length, so every other byte including the two length fields is
// exactly as it came off the wire.
func TestBuildStreamSwitch(t *testing.T) {
	const captured = "0a390a0408011001102018602001280138034060486150045801700a800103880101" +
		"ba0103696f73ca011048433331585858585858585858585858"

	got, err := BuildStreamSwitch("HC31XXXXXXXXXXXX", 10)
	if err != nil {
		t.Fatalf("BuildStreamSwitch: %v", err)
	}
	if hex.EncodeToString(got) != captured {
		t.Errorf("built frame differs from the captured one:\n got %s\nwant %s",
			hex.EncodeToString(got), captured)
	}
}

func TestBuildStreamSwitchRejects(t *testing.T) {
	tests := []struct {
		name   string
		serial string
		seq    int
	}{
		{"sequence zero", "HC31XXXXXXXXXXXX", 0},
		{"sequence past one byte", "HC31XXXXXXXXXXXX", MaxSwitchSeq + 1},
		{"empty serial", "", 1},
		{"serial not ascii", "HC31\x00XXXXXXXXXXX", 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildStreamSwitch(tc.serial, tc.seq); err == nil {
				t.Error("got no error, want one")
			}
		})
	}
}

// TestParseRejectsGarbage makes sure a truncated or foreign message is a plain
// error, never a panic: these bytes come off a network from an undocumented
// protocol, and the daemon has to survive whatever turns up.
func TestParseRejectsGarbage(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{"empty", ""},
		{"one byte", "0a"},
		{"length beyond the buffer", "0a7f0102"},
		{"no header fields", "0a020801"},
		{"wrong wire type on field 1", "0801"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatalf("bad test input: %v", err)
			}
			if _, err := Parse(b); err == nil {
				t.Error("got no error, want one")
			}
		})
	}
}

// TestObfuscationIsSymmetric pins down what the XOR actually is: applying the
// sequence key twice has to give the bytes back. If that ever fails the
// deobfuscation is doing something other than what is documented.
func TestObfuscationIsSymmetric(t *testing.T) {
	for _, b := range readCapture(t, "fast.txt") {
		f, err := Parse(b)
		if err != nil || len(f.Payload) == 0 {
			continue
		}
		key := byte(f.Seq & 0xFF)
		again := make([]byte, len(f.Payload))
		for i, c := range f.Payload {
			again[i] = c ^ key
		}
		reparsed, err := Parse(b)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		for i := range again {
			if again[i]^key != reparsed.Payload[i] {
				t.Fatalf("XOR is not symmetric at byte %d", i)
			}
		}
		return
	}
	t.Fatal("no frame with a payload in the capture")
}
