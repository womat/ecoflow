package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/womat/ecoflow/internal/frames"
)

// TestStatusFromCapture feeds the fast capture through the status as listen
// does, and checks what the page would show against the last line of the
// golden file.
func TestStatusFromCapture(t *testing.T) {
	now := time.Date(2026, 9, 22, 9, 14, 0, 0, time.UTC)
	s := newStatus(&config{serial: "HC31XXXXXXXXXXXX", topic: "ecoflow", fast: true, switchEvery: 3 * time.Second})
	s.now = func() time.Time { return now }
	s.cloudUp("mqtt-e.ecoflow.com:8883")

	var seen readings
	reports, readingsSent := 0, 0
	for _, f := range captureFrames(t, "fast") {
		if _, ok := f.Energy(); ok {
			s.report()
			reports++
		}
		if _, e, ok := seen.add(f); ok {
			s.reading(e, true)
			readingsSent++
		}
	}
	if reports == 0 {
		t.Fatal("no energy frames in the capture")
	}

	v := s.view()
	if !v.Cloud.Connected || v.Cloud.Server != "mqtt-e.ecoflow.com:8883" || !v.Cloud.Fast || v.Cloud.SwitchEvery != 3 {
		t.Errorf("cloud: got %+v", v.Cloud)
	}
	if v.Cloud.ReportsPerMinute != reports || v.MQTT.TelegramsPerMinute != readingsSent {
		t.Errorf("per minute: got %d reports, %d telegrams; want %d, %d",
			v.Cloud.ReportsPerMinute, v.MQTT.TelegramsPerMinute, reports, readingsSent)
	}
	if v.State == nil {
		t.Fatal("no state")
	}
	// The last reading of the capture, as the golden file has it.
	golden, err := os.ReadFile(captureDir + "fast.golden")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(golden)), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "SoC "+strconv.FormatUint(v.State.SoC, 10)+" %") {
		t.Errorf("SoC %d does not match %q", v.State.SoC, last)
	}
	if strings.Contains(last, "(charging)") != (v.State.Battery > 0) {
		t.Errorf("battery %d W does not match %q", v.State.Battery, last)
	}

	now = now.Add(2 * rateWindow)
	if v := s.view(); v.Cloud.ReportsPerMinute != 0 || v.MQTT.TelegramsPerMinute != 0 {
		t.Errorf("after two minutes: got %d reports, %d telegrams per minute; want none",
			v.Cloud.ReportsPerMinute, v.MQTT.TelegramsPerMinute)
	}

	s.cloudDown()
	if v := s.view(); v.Cloud.Connected || v.Cloud.Since == "" || v.State == nil {
		t.Errorf("after the connection ended: got %+v, state %v", v.Cloud, v.State)
	}
}

// TestNilStatusIsHarmless: without --listen there is no status, and the
// cloud side feeds it all the same.
func TestNilStatusIsHarmless(t *testing.T) {
	var s *status
	s.cloudUp("x")
	s.cloudDown()
	s.report()
	s.reading(frames.Energy{}, true)
	s.totals(nil)
}

func TestBrokerHostHidesCredentials(t *testing.T) {
	if got := brokerHost("tcp://user:secret@192.168.1.5:1883"); got != "192.168.1.5:1883" {
		t.Errorf("got %q, want host and port only", got)
	}
}
