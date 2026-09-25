package main

import (
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/womat/ecoflow/internal/frames"
)

// fakeBroker records what was published instead of sending it anywhere.
type fakeBroker struct {
	mu   sync.Mutex
	sent []published
}

type published struct {
	topic    string
	retained bool
	payload  string
}

func (f *fakeBroker) Publish(topic string, _ byte, retained bool, payload any) mqtt.Token {
	f.mu.Lock()
	defer f.mu.Unlock()

	var s string
	switch v := payload.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	}
	f.sent = append(f.sent, published{topic, retained, s})
	return &doneToken{}
}

func (f *fakeBroker) Disconnect(uint) {}

func (f *fakeBroker) all() []published {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]published(nil), f.sent...)
}

// doneToken is a token that has already completed without error.
type doneToken struct{}

func (doneToken) Wait() bool                     { return true }
func (doneToken) WaitTimeout(time.Duration) bool { return true }
func (doneToken) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
func (doneToken) Error() error { return nil }

func newTestPublisher() (*publisher, *fakeBroker) {
	f := &fakeBroker{}
	return &publisher{
		client: f,
		prefix: "ecoflow",
		serial: "HC31XXXXXXXXXXXX",
		stderr: io.Discard,
	}, f
}

// decode reads a telegram back into a generic map, so the tests see the keys
// exactly as a consumer does rather than through the struct that wrote them.
func decode(t *testing.T, payload string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("telegram is not JSON: %v\n%s", err, payload)
	}
	return m
}

func keys(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

// TestStateTelegram pins the reading telegram as consumers see it: the keys,
// the device's signs, and the measurement time rather than the sending time.
func TestStateTelegram(t *testing.T) {
	p, f := newTestPublisher()

	at := time.Date(2026, 9, 22, 9, 13, 16, 0, time.UTC)
	p.energy(frames.Energy{
		PV: 975.2, House: -459.4, Battery: 515.3, Grid: 0, DCDC: 358, SoC: 63, Measured: at,
	})

	sent := f.all()
	if len(sent) != 1 {
		t.Fatalf("got %d messages for one reading, want 1", len(sent))
	}
	if sent[0].topic != "ecoflow/state" {
		t.Errorf("topic %q, want ecoflow/state", sent[0].topic)
	}
	if sent[0].retained {
		t.Error("the reading was published retained")
	}

	got := decode(t, sent[0].payload)
	want := map[string]any{
		"sn": "HC31XXXXXXXXXXXX", "timestamp": "2026-09-22T09:13:16Z",
		"pv": 975.0, "house": -459.0, "battery": 515.0, "grid": 0.0, "soc": 63.0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// TestDCDCIsNotPublished guards a decision rather than a mechanism: the role
// of dcdc is not settled, and a telegram carries only what is understood.
// When it is settled, this test goes together with the decision.
func TestDCDCIsNotPublished(t *testing.T) {
	p, f := newTestPublisher()
	p.energy(frames.Energy{PV: 1, DCDC: 358, Measured: time.Unix(0, 0).UTC()})

	if _, ok := decode(t, f.all()[0].payload)["dcdc"]; ok {
		t.Error("dcdc is in the telegram, but its role is not settled")
	}
}

// TestEveryReadingIsSent covers what replaced the heartbeat: each reading
// carries its own time, so an unchanged value is still news - it says the
// device is still there.
func TestEveryReadingIsSent(t *testing.T) {
	p, f := newTestPublisher()

	at := time.Now().UTC()
	p.energy(frames.Energy{PV: 970, Measured: at})
	p.energy(frames.Energy{PV: 970, Measured: at.Add(time.Minute)})

	if n := len(f.all()); n != 2 {
		t.Errorf("got %d messages for two readings, want 2", n)
	}
}

// TestEnergyTelegramFromCapture walks a real capture through the path the
// service takes and checks the totals the device reported.
func TestEnergyTelegramFromCapture(t *testing.T) {
	p, f := newTestPublisher()
	day := newDailyTotals()

	var when time.Time
	for _, fr := range captureFrames(t, "fast") {
		if part, ok := fr.Hourly(); ok {
			if parts, complete := day.add(part); complete {
				when = part.When
				p.totals(parts)
				break
			}
		}
	}

	sent := f.all()
	if len(sent) != 1 {
		t.Fatalf("got %d messages, want one energy telegram", len(sent))
	}
	if sent[0].topic != "ecoflow/energy" || sent[0].retained {
		t.Errorf("published on %q, retained %v; want ecoflow/energy, not retained",
			sent[0].topic, sent[0].retained)
	}

	got := decode(t, sent[0].payload)
	want := map[string]any{
		"sn": "HC31XXXXXXXXXXXX", "timestamp": when.Format(time.RFC3339),
		"pv": 3301.0, "house": 3206.0, "batteryIn": 1545.0, "batteryOut": 1562.0,
		"gridIn": 62.0, "gridOut": 175.0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// TestNoAvailabilityTopic states what went away: no status topic, no last
// will, and nothing retained. A consumer judges freshness by the timestamp.
func TestNoAvailabilityTopic(t *testing.T) {
	p, f := newTestPublisher()

	p.energy(frames.Energy{PV: 970, Measured: time.Now().UTC()})
	p.close()

	for _, s := range f.all() {
		if s.topic != "ecoflow/state" {
			t.Errorf("unexpected message on %q", s.topic)
		}
		if s.retained {
			t.Errorf("%s was published retained", s.topic)
		}
	}
}

// TestTelegramKeys keeps the key names in one place a reviewer can see: they
// are the contract with every consumer, and camelCase is the decided style.
func TestTelegramKeys(t *testing.T) {
	s, _ := json.Marshal(stateOf("x", frames.Energy{}))
	e, _ := json.Marshal(energyOf("x", map[frames.Flow]frames.HourlyPart{}))

	wantState := []string{"battery", "grid", "house", "pv", "sn", "soc", "timestamp"}
	wantEnergy := []string{"batteryIn", "batteryOut", "gridIn", "gridOut", "house", "pv", "sn", "timestamp"}

	if got := keys(decode(t, string(s))); !reflect.DeepEqual(got, wantState) {
		t.Errorf("state keys %v, want %v", got, wantState)
	}
	if got := keys(decode(t, string(e))); !reflect.DeepEqual(got, wantEnergy) {
		t.Errorf("energy keys %v, want %v", got, wantEnergy)
	}
}
