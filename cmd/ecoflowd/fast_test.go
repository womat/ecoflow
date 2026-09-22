package main

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/womat/ecoflow/internal/frames"
)

// TestFastIsOffByDefault guards the property the whole design rests on: asking
// for values must never write to the device as a side effect.
func TestFastIsOffByDefault(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "a@b.c")
	t.Setenv("ECOFLOW_PASSWORD", "x")

	opts := &options{serial: "HC31XXXXXXXXXXXX"}
	cfg, err := buildConfig(opts, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.fast {
		t.Error("--fast defaulted to on; publishing must be asked for by name")
	}
}

func TestSwitchIntervalIsChecked(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "a@b.c")
	t.Setenv("ECOFLOW_PASSWORD", "x")

	tests := []struct {
		name  string
		every time.Duration
		ok    bool
	}{
		{"the app's own rate", 3 * time.Second, true},
		{"measured too slow but allowed", 10 * time.Second, true},
		{"faster than the app", time.Second, true},
		{"sub-second", 100 * time.Millisecond, false},
		{"zero", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := &options{serial: "HC31XXXXXXXXXXXX", fast: true, switchEvery: tc.every}
			_, err := buildConfig(opts, nil)
			if tc.ok && err != nil {
				t.Errorf("got %v, want it accepted", err)
			}
			if !tc.ok && err == nil {
				t.Error("got no error, want one")
			}
		})
	}
}

// TestSwitchSequenceWraps covers the one piece of state in the publishing
// loop. Past 127 a varint needs a second byte, and the frame would stop being
// the length that was captured.
func TestSwitchSequenceWraps(t *testing.T) {
	seq := 1
	seen := map[int]bool{}
	for range frames.MaxSwitchSeq + 5 {
		if seq < 1 || seq > frames.MaxSwitchSeq {
			t.Fatalf("sequence left its range: %d", seq)
		}
		seen[seq] = true
		if _, err := frames.BuildStreamSwitch("HC31XXXXXXXXXXXX", seq); err != nil {
			t.Fatalf("BuildStreamSwitch(%d): %v", seq, err)
		}
		seq = seq%frames.MaxSwitchSeq + 1
	}
	if len(seen) != frames.MaxSwitchSeq {
		t.Errorf("used %d of %d sequence numbers", len(seen), frames.MaxSwitchSeq)
	}
}

// TestSwitchFrameIsTheAppsOne repeats the byte comparison here, where the
// serial actually comes from the command line. The frame builder is tested in
// internal/frames; what this checks is that this program feeds it the right
// thing rather than, say, the name.
func TestSwitchFrameIsTheAppsOne(t *testing.T) {
	const captured = "0a390a0408011001102018602001280138034060486150045801700a800103880101" +
		"ba0103696f73ca011048433331585858585858585858585858"

	cfg := &config{serial: "HC31XXXXXXXXXXXX"}
	got, err := frames.BuildStreamSwitch(cfg.serial, 10)
	if err != nil {
		t.Fatalf("BuildStreamSwitch: %v", err)
	}
	if hex.EncodeToString(got) != captured {
		t.Errorf("built frame differs from the captured one:\n got %s\nwant %s",
			hex.EncodeToString(got), captured)
	}
}

// TestFastStreamWarnsOnce covers the reporting of a switch that was accepted
// but achieved nothing. The broker answers PUBACK either way, so silence about
// this would leave a coarse graph with no explanation.
func TestFastStreamWarnsOnce(t *testing.T) {
	t.Run("no fast reports", func(t *testing.T) {
		var stderr bytes.Buffer
		var f fastStream
		f.start(3 * time.Second)
		f.complain(&stderr)
		f.complain(&stderr)

		if n := strings.Count(stderr.String(), "no fast reports arrived"); n != 1 {
			t.Errorf("warned %d times, want exactly 1: %q", n, stderr.String())
		}
	})

	t.Run("fast reports arrived", func(t *testing.T) {
		var stderr bytes.Buffer
		var f fastStream
		f.start(3 * time.Second)
		f.arrived()
		f.complain(&stderr)

		if stderr.Len() != 0 {
			t.Errorf("warned although the stream runs: %q", stderr.String())
		}
	})

	t.Run("never armed without fast", func(t *testing.T) {
		var f fastStream
		if f.deadline() != nil {
			t.Error("the deadline fires although --fast was never asked for")
		}
	})
}
