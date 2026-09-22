package frames

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The golden files hold the output of scripts/ecoflow-frames.py over the same
// captures. That script is the implementation this package replaces, and it
// was checked against the device for days, so agreeing with it line for line
// is the strongest available evidence that this decoding is right.
//
// Regenerate with:
//
//	python3 scripts/ecoflow-frames.py < internal/frames/testdata/fast.txt \
//	  > internal/frames/testdata/fast.golden
//
// A difference here is not a formatting nit. It means the two disagree about
// what the device said, and one of them is wrong.
func TestAgreesWithPythonDecoder(t *testing.T) {
	for _, capture := range []string{"fast", "slow"} {
		t.Run(capture, func(t *testing.T) {
			want, err := os.ReadFile("testdata/" + capture + ".golden")
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			got := render(t, capture+".txt")

			gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			wantLines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")

			if len(gotLines) != len(wantLines) {
				t.Errorf("got %d readings, want %d", len(gotLines), len(wantLines))
			}
			for i := range min(len(gotLines), len(wantLines)) {
				if gotLines[i] != wantLines[i] {
					t.Fatalf("reading %d differs:\n got %s\nwant %s",
						i+1, gotLines[i], wantLines[i])
				}
			}
		})
	}
}

// render reproduces the python decoder's output for one capture, including its
// two suppression rules: a reading identical to the one before it is dropped,
// and the minutely report is skipped while a fast one arrived recently, since
// it repeats a reading already shown with its timestamp rounded to the minute.
func render(t *testing.T, name string) string {
	t.Helper()

	const fastStillRunning = 90

	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open capture: %v", err)
	}
	defer f.Close()

	var out strings.Builder
	var previous string
	var lastFast int64
	haveFast := false

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
		frame, err := Parse(b)
		if err != nil {
			continue
		}
		e, ok := frame.Energy()
		if !ok {
			continue
		}

		when := e.Measured.Unix()
		if frame.Command == Fast {
			lastFast, haveFast = when, true
		} else if haveFast && when-lastFast <= fastStillRunning {
			continue
		}

		line := reading(e)
		if line == previous {
			continue
		}
		previous = line
		fmt.Fprintln(&out, line)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return out.String()
}

func reading(e Energy) string {
	return fmt.Sprintf(
		"%s  PV %7.0f W | house %6.0f W | battery %6.0f W (%s) | grid %6.0f W (%s) | SoC %d %%",
		e.Measured.Format("15:04:05Z"),
		e.PV, abs(e.House),
		abs(e.Battery), direction(e.Battery, "charging", "discharging"),
		abs(e.Grid), direction(e.Grid, "export", "import"),
		e.SoC)
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func direction(v float32, positive, negative string) string {
	switch {
	case v > 0:
		return positive
	case v < 0:
		return negative
	}
	return "idle"
}
