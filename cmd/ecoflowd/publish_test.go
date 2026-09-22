package main

import (
	"strings"
	"testing"
	"time"

	"github.com/womat/ecoflow/internal/frames"
)

func TestBrokerURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		bad  bool
	}{
		{name: "bare host and port", in: "127.0.0.1:1883", want: "tcp://127.0.0.1:1883"},
		{name: "bare host", in: "mosquitto", want: "tcp://mosquitto:1883"},
		{name: "explicit tcp", in: "tcp://127.0.0.1:1883", want: "tcp://127.0.0.1:1883"},
		{name: "tls defaults to 8883", in: "ssl://broker.lan", want: "ssl://broker.lan:8883"},
		{name: "websocket", in: "ws://127.0.0.1:9001", want: "ws://127.0.0.1:9001"},
		{name: "empty", in: "", bad: true},
		{name: "unknown scheme", in: "http://127.0.0.1", bad: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := brokerURL(tc.in)
			if tc.bad {
				if err == nil {
					t.Errorf("got %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("brokerURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTopicFor(t *testing.T) {
	tests := []struct {
		prefix, name, leaf, want string
	}{
		{"ecoflow", "mathe", "pv", "ecoflow/mathe/pv"},
		{"/ecoflow/", "mathe", "status", "ecoflow/mathe/status"},
		{"solar/dach", "HC31XXXXXXXXXXXX", "energy/pv", "solar/dach/HC31XXXXXXXXXXXX/energy/pv"},
	}

	for _, tc := range tests {
		if got := topicFor(tc.prefix, tc.name, tc.leaf); got != tc.want {
			t.Errorf("topicFor(%q, %q, %q) = %q, want %q",
				tc.prefix, tc.name, tc.leaf, got, tc.want)
		}
	}
}

func TestTopicPrefixIsChecked(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "a@b.c")
	t.Setenv("ECOFLOW_PASSWORD", "x")

	tests := []struct {
		name  string
		topic string
		user  string
		bad   bool
	}{
		{name: "plain", topic: "ecoflow"},
		{name: "nested", topic: "solar/dach"},
		// An empty prefix falls back to the default rather than failing: there
		// is no sensible topic without one, and refusing would only turn a
		// harmless omission into a stopped service.
		{name: "empty falls back", topic: ""},
		{name: "only slashes falls back", topic: "///"},
		{name: "wildcard", topic: "ecoflow/#", bad: true},
		{name: "user without broker", topic: "ecoflow", user: "mosquitto", bad: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := &options{serial: "HC31XXXXXXXXXXXX", topic: tc.topic, mqttUser: tc.user}
			_, err := buildConfig(opts, nil)
			if tc.bad && err == nil {
				t.Error("got no error, want one")
			}
			if !tc.bad && err != nil {
				t.Errorf("got %v, want it accepted", err)
			}
		})
	}
}

// TestWattsKeepsTheDeviceSigns pins the convention that consumers have to know
// about: the device reports positive grid as export and positive battery as
// charging, and nothing here turns that around.
func TestWattsKeepsTheDeviceSigns(t *testing.T) {
	tests := []struct {
		in   float32
		want string
	}{
		{970.4, "970"},
		{-415.6, "-416"},
		{0, "0"},
		{0.4, "0"},
	}

	for _, tc := range tests {
		if got := watts(tc.in); got != tc.want {
			t.Errorf("watts(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDailyTotalsWaitForAllSix covers the rule that a partial hourly set is
// worth nothing: the six flows share a timestamp, and publishing some of them
// would mean totals that contradict each other.
func TestDailyTotalsWaitForAllSix(t *testing.T) {
	when := time.Unix(1790068398, 0).UTC()
	d := newDailyTotals()

	for i, flow := range frames.Flows {
		parts, complete := d.add(frames.HourlyPart{Flow: flow, When: when, Hours: []uint64{1}})
		wantComplete := i == len(frames.Flows)-1
		if complete != wantComplete {
			t.Errorf("after %d of %d flows: complete = %v, want %v",
				i+1, len(frames.Flows), complete, wantComplete)
		}
		if complete && len(parts) != len(frames.Flows) {
			t.Errorf("got %d parts, want %d", len(parts), len(frames.Flows))
		}
	}

	// A new timestamp starts over rather than mixing two readings of the day.
	parts, complete := d.add(frames.HourlyPart{
		Flow: frames.FlowPV, When: when.Add(time.Minute), Hours: []uint64{2},
	})
	if complete {
		t.Error("a single flow of a new timestamp counted as a complete set")
	}
	if len(parts) != 1 {
		t.Errorf("got %d parts after the timestamp changed, want 1", len(parts))
	}
}

// TestReadingsFeedTheTotals walks a real capture through the same path the
// service uses, so the totals are the ones the device actually reported.
func TestReadingsFeedTheTotals(t *testing.T) {
	day := newDailyTotals()
	var complete map[frames.Flow]frames.HourlyPart

	for _, f := range captureFrames(t, "fast") {
		if part, ok := f.Hourly(); ok {
			if parts, done := day.add(part); done {
				complete = parts
			}
		}
	}

	if complete == nil {
		t.Fatal("the capture never yielded a complete hourly set")
	}
	if got := complete[frames.FlowPV].Total(); got == 0 {
		t.Error("the PV total came out zero, which the capture is not")
	}
	// The same arithmetic that established the flow numbering, here on the
	// path the service takes rather than in the decoder's own test.
	for hour := range frames.HoursPerDay {
		if d := frames.HourlyBalance(complete, hour); d < -1 || d > 1 {
			t.Errorf("hour %d: balance off by %d Wh", hour, d)
		}
	}
}

func TestStatusTopicNames(t *testing.T) {
	// Home Assistant's availability_topic defaults to these payloads, so the
	// names are not arbitrary: getting them wrong means every sensor stays
	// unavailable with no error anywhere.
	if payloadOnline != "online" || payloadOffline != "offline" {
		t.Errorf("availability payloads are %q and %q, want online and offline",
			payloadOnline, payloadOffline)
	}
	if !strings.Contains(topicFor("ecoflow", "mathe", "status"), "/status") {
		t.Error("the availability topic is not called status")
	}
}
