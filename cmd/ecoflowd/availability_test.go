package main

import (
	"io"
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

// last returns the most recent payload on a topic, and whether there was one.
func (f *fakeBroker) last(topic string) (published, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].topic == topic {
			return f.sent[i], true
		}
	}
	return published{}, false
}

func (f *fakeBroker) count(topic string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n int
	for _, s := range f.sent {
		if s.topic == topic {
			n++
		}
	}
	return n
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
		prefix: "ecoflow/HC31XXXXXXXXXXXX",
		stderr: io.Discard,
		last:   map[string]string{},
		done:   make(chan struct{}),
	}, f
}

func sample(pv float32, at time.Time) frames.Energy {
	return frames.Energy{PV: pv, House: -400, Battery: 500, Grid: pv - 900, Measured: at}
}

// TestNothingIsOnlineBeforeAReading covers the state the service starts in.
// Announcing availability before anything has arrived would be a retained
// claim that nothing backs - and one that never gets corrected if the cloud is
// never reached at all.
func TestNothingIsOnlineBeforeAReading(t *testing.T) {
	p, f := newTestPublisher()

	p.announce(f)

	got, ok := f.last("ecoflow/HC31XXXXXXXXXXXX/status")
	if !ok {
		t.Fatal("nothing was published on the status topic")
	}
	if got.payload != payloadOffline {
		t.Errorf("got %q before any reading, want %q", got.payload, payloadOffline)
	}
	if !got.retained {
		t.Error("availability was published without the retain flag")
	}
}

// TestStalenessIsReportedOnce is the behaviour the whole non-retained design
// rests on: when readings stop, a consumer has to be told, or it keeps
// controlling on a value that stopped being true.
func TestStalenessIsReportedOnce(t *testing.T) {
	p, f := newTestPublisher()
	const status = "ecoflow/HC31XXXXXXXXXXXX/status"

	now := time.Now()
	p.energy(sample(970, now), now)

	if got, _ := f.last(status); got.payload != payloadOnline {
		t.Fatalf("got %q after a reading, want %q", got.payload, payloadOnline)
	}

	// Still fresh a minute later.
	p.check(now.Add(time.Minute))
	if got, _ := f.last(status); got.payload != payloadOnline {
		t.Errorf("got %q one minute in, want it to stay %q", got.payload, payloadOnline)
	}

	before := f.count(status)
	p.check(now.Add(staleAfter + time.Second))
	if got, _ := f.last(status); got.payload != payloadOffline {
		t.Errorf("got %q after %s without a reading, want %q", got.payload, staleAfter, payloadOffline)
	}

	// Saying it again on every tick would be noise on the broker.
	p.check(now.Add(staleAfter + time.Minute))
	if n := f.count(status) - before; n != 1 {
		t.Errorf("published availability %d times for one change, want 1", n)
	}

	// And it comes back when readings do.
	later := now.Add(staleAfter + 2*time.Minute)
	p.energy(sample(980, later), later)
	if got, _ := f.last(status); got.payload != payloadOnline {
		t.Errorf("got %q after readings resumed, want %q", got.payload, payloadOnline)
	}
}

// TestReconnectDoesNotResurrectAvailability guards the trap that made the
// first attempt at this wrong: the connect handler used to publish "online"
// unconditionally, so a broker restart while the device was quiet would undo
// the staleness report and leave a stale value looking current.
func TestReconnectDoesNotResurrectAvailability(t *testing.T) {
	p, f := newTestPublisher()
	const status = "ecoflow/HC31XXXXXXXXXXXX/status"

	now := time.Now()
	p.energy(sample(970, now), now)
	p.check(now.Add(staleAfter + time.Second))

	if got, _ := f.last(status); got.payload != payloadOffline {
		t.Fatalf("setup: got %q, want %q", got.payload, payloadOffline)
	}

	p.announce(f) // as the connect handler does after a reconnect

	if got, _ := f.last(status); got.payload != payloadOffline {
		t.Errorf("reconnect published %q for a device that had gone quiet, want %q",
			got.payload, payloadOffline)
	}
}

// TestValuesAreNotRetained states the other half of the same decision. A
// retained reading outlives what it describes; availability does not.
func TestValuesAreNotRetained(t *testing.T) {
	p, f := newTestPublisher()

	now := time.Now()
	p.energy(sample(970, now), now)

	f.mu.Lock()
	defer f.mu.Unlock()

	var checked int
	for _, s := range f.sent {
		if s.topic == "ecoflow/HC31XXXXXXXXXXXX/status" {
			continue
		}
		checked++
		if s.retained {
			t.Errorf("%s was published retained", s.topic)
		}
	}
	if checked == 0 {
		t.Fatal("no measurements were published at all")
	}
}

// TestHeartbeatRepublishesUnchangedValues covers why the heartbeat exists: a
// consumer cannot tell "unchanged" from "gone" without one.
func TestHeartbeatRepublishesUnchangedValues(t *testing.T) {
	p, f := newTestPublisher()
	const pv = "ecoflow/HC31XXXXXXXXXXXX/pv"

	now := time.Now()
	p.energy(sample(970, now), now)
	first := f.count(pv)

	// The same value again straight away adds nothing.
	p.energy(sample(970, now.Add(2*time.Second)), now.Add(2*time.Second))
	if f.count(pv) != first {
		t.Error("an unchanged value was published again before the heartbeat was due")
	}

	// Once the heartbeat is due it goes out regardless.
	later := now.Add(heartbeat + time.Second)
	p.energy(sample(970, later), later)
	if f.count(pv) != first+1 {
		t.Errorf("got %d publishes after the heartbeat, want %d", f.count(pv), first+1)
	}
}
