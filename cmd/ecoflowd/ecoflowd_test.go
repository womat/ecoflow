package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/womat/ecoflow/internal/frames"
)

// captureDir holds the recordings and the golden files. They live with
// internal/frames, where the decoding itself is tested against them; reading
// them from here rather than copying keeps one set of recordings for both.
const captureDir = "../../internal/frames/testdata/"

func exec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(append([]string{"ecoflowd"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUsageErrors(t *testing.T) {
	// The credentials come from the environment, so a test that checks their
	// absence must not inherit them from the machine it runs on.
	t.Setenv("ECOFLOW_EMAIL", "")
	t.Setenv("ECOFLOW_PASSWORD", "")

	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{
			name: "no serial",
			args: []string{"--stdout"},
			want: "--sn is required",
		},
		{
			name: "serial with a wildcard",
			args: []string{"--sn", "HC31#"},
			want: "serial number looks wrong",
		},
		{
			name: "no credentials",
			args: []string{"--sn", "HC31XXXXXXXXXXXX"},
			want: "ECOFLOW_EMAIL and ECOFLOW_PASSWORD",
		},
		{
			name: "stray argument",
			args: []string{"--sn", "HC31XXXXXXXXXXXX", "leftover"},
			want: "unexpected argument",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			code, _, stderr := exec(t, tc.args...)
			if code != exitUsage {
				t.Errorf("got exit %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("got stderr %q, want it to mention %q", stderr, tc.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := exec(t, "--version")
	if code != exitOK {
		t.Errorf("got exit %d, want %d", code, exitOK)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("--version printed nothing")
	}
	// This file began as a copy of modbusread's, which named itself in the
	// version string. A binary that reports the wrong program is the sort of
	// thing nobody notices until a bug report is filed against the wrong tool.
	if !strings.HasPrefix(stdout, "ecoflowd ") {
		t.Errorf("got %q, want it to start with the program's own name", stdout)
	}
}

func TestHelp(t *testing.T) {
	code, _, stderr := exec(t, "--help")
	if code != exitOK {
		t.Errorf("got exit %d, want %d", code, exitOK)
	}
	// The help has to say where the credentials come from: they are the one
	// thing a reader cannot discover from the flags.
	for _, want := range []string{"ECOFLOW_EMAIL", "ECOFLOW_PASSWORD", "exit status"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}

// TestTopicsCarryTheSerial pins the namespace. The serial is the only name a
// device has, and having a second one would mean two things to keep in step
// for no gain - the serial is already on the command line and in the systemd
// instance name anyway.
func TestTopicsCarryTheSerial(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "a@b.c")
	t.Setenv("ECOFLOW_PASSWORD", "x")

	cfg, err := buildConfig(&options{serial: "HC31XXXXXXXXXXXX", topic: "ecoflow"}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}

	want := "ecoflow/HC31XXXXXXXXXXXX/pv"
	if got := topicFor(cfg.topic, cfg.serial, "pv"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestOutputMatchesPythonDecoder is the check that this program agrees with
// the shell and python pair it replaces.
//
// The captures and the golden files live with internal/frames, which is where
// they are also used to test the decoding itself. Reading them from here
// rather than copying keeps one set of recordings for both.
func TestOutputMatchesPythonDecoder(t *testing.T) {
	for _, capture := range []string{"fast", "slow"} {
		t.Run(capture, func(t *testing.T) {
			want, err := os.ReadFile(captureDir + capture + ".golden")
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			f, err := os.Open(captureDir + capture + ".txt")
			if err != nil {
				t.Fatalf("open capture: %v", err)
			}
			defer f.Close()

			var got bytes.Buffer
			var seen readings

			s := bufio.NewScanner(f)
			s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for s.Scan() {
				parts := strings.Fields(s.Text())
				if len(parts) < 4 {
					continue
				}
				payload, err := hex.DecodeString(parts[3])
				if err != nil {
					continue
				}
				frame, err := frames.Parse(payload)
				if err != nil {
					continue
				}
				if line, _, ok := seen.add(frame); ok {
					got.WriteString(line + "\n")
				}
			}
			if err := s.Err(); err != nil {
				t.Fatalf("read capture: %v", err)
			}

			gotLines := strings.Split(strings.TrimRight(got.String(), "\n"), "\n")
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

// TestReadingsSuppressesRepeats uses a real frame rather than a made-up one:
// the device sends some frames twice, and two identical readings are one
// reading.
func TestReadingsSuppressesRepeats(t *testing.T) {
	f := firstEnergyFrame(t)

	var seen readings
	if _, _, ok := seen.add(f); !ok {
		t.Fatal("the first reading was suppressed")
	}
	if line, _, ok := seen.add(f); ok {
		t.Errorf("the same frame twice produced a second reading: %s", line)
	}
}

// TestMinutelyYieldsWhenFastIsRunning covers the other suppression: while the
// fast stream runs, the minutely report repeats a reading already shown, and
// its timestamp rounded to the minute makes it look like the clock stopped.
func TestMinutelyYieldsWhenFastIsRunning(t *testing.T) {
	var fast, minutely frames.Frame
	var haveFast, haveMinutely bool

	for _, f := range captureFrames(t, "fast") {
		switch f.Command {
		case frames.Fast:
			if !haveFast {
				fast, haveFast = f, true
			}
		case frames.Minutely:
			if !haveMinutely {
				minutely, haveMinutely = f, true
			}
		}
		if haveFast && haveMinutely {
			break
		}
	}
	if !haveFast || !haveMinutely {
		t.Fatal("the capture holds no pair of a fast and a minutely report")
	}

	var withFast readings
	withFast.add(fast)
	if line, _, ok := withFast.add(minutely); ok {
		t.Errorf("minutely report reported while the fast stream runs: %s", line)
	}

	// On its own it must come through, or a device that only reports once a
	// minute would look dead.
	var alone readings
	if _, _, ok := alone.add(minutely); !ok {
		t.Error("minutely report suppressed although no fast report arrived")
	}
}

func firstEnergyFrame(t *testing.T) frames.Frame {
	t.Helper()

	for _, f := range captureFrames(t, "fast") {
		if _, ok := f.Energy(); ok {
			return f
		}
	}
	t.Fatal("the capture holds no energy report")
	return frames.Frame{}
}

func captureFrames(t *testing.T, capture string) []frames.Frame {
	t.Helper()

	f, err := os.Open(captureDir + capture + ".txt")
	if err != nil {
		t.Fatalf("open capture: %v", err)
	}
	defer f.Close()

	var out []frames.Frame
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		parts := strings.Fields(s.Text())
		if len(parts) < 4 {
			continue
		}
		payload, err := hex.DecodeString(parts[3])
		if err != nil {
			continue
		}
		frame, err := frames.Parse(payload)
		if err != nil {
			continue
		}
		out = append(out, frame)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return out
}
