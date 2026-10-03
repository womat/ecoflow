package main

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/womat/ecoflow/internal/frames"
)

// window2230 is 22:30-23:00 as the device encodes it, taken from the capture.
var window2230 = []byte{0xc6, 0x8a, 0x90, 0x2b}

func chargeTask(number uint64, enabled bool) frames.Task {
	return frames.Task{Number: number, Config: 2, Enabled: enabled, Type: frames.TypeCharge,
		Mode: 129, Window: window2230}
}

func TestPickTask(t *testing.T) {
	other := frames.Task{Number: 4, Type: 2, Mode: 129, Window: window2230}

	tests := []struct {
		name  string
		tasks []frames.Task
		fixed uint64
		want  uint64
		err   error
	}{
		{"the only charge task", []frames.Task{chargeTask(7, false)}, 0, 7, nil},
		{"other types do not count", []frames.Task{other, chargeTask(7, false)}, 0, 7, nil},
		{"none", []frames.Task{other}, 0, 0, errNoTask},
		{"empty list", nil, 0, 0, errNoTask},
		{"two charge tasks", []frames.Task{chargeTask(7, false), chargeTask(8, false)}, 0, 0, errAmbiguous},
		{"fixed picks among several", []frames.Task{chargeTask(7, false), chargeTask(8, false)}, 8, 8, nil},
		{"fixed but missing", []frames.Task{chargeTask(7, false)}, 9, 0, errNoTask},
		{"fixed but another type", []frames.Task{other}, 4, 0, errNoTask},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickTask(tc.tasks, tc.fixed)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("got error %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil || got.Number != tc.want {
				t.Fatalf("got task %d, error %v; want task %d", got.Number, err, tc.want)
			}
		})
	}
}

// device stands in for the cloud and the device: it records what is sent and
// answers a task switch the way the real one does - an acknowledgement with
// the command's sequence number, then the updated list.
type device struct {
	t      *testing.T
	b      *blocker
	silent bool // never answers

	mu    sync.Mutex
	tasks []frames.Task
	sent  []frames.Frame
}

func (d *device) send(frame []byte) error {
	// What this program sends is plain, like the replies, so ParseReply
	// reads it back.
	f, err := frames.ParseReply(frame)
	if err != nil {
		d.t.Errorf("sent something that is not a frame: %x", frame)
		return err
	}

	d.mu.Lock()
	d.sent = append(d.sent, f)
	if f.Command != frames.TaskConfig || d.silent {
		d.mu.Unlock()
		return nil
	}
	sentTask := commandTask(d.t, f.Payload)
	for i := range d.tasks {
		if d.tasks[i].Number == sentTask.Number {
			d.tasks[i].Enabled = sentTask.Enabled
		}
	}
	list := append([]frames.Task(nil), d.tasks...)
	d.mu.Unlock()

	go func() {
		d.b.acknowledged(f.Seq)
		d.b.listed(list)
	}()
	return nil
}

// commandTask reads the task in a 96/125 payload. A command carries one task
// laid out as a list entry, so wrapping it as a one-entry list (field 1, wire
// type 2) lets the list reader do the work.
func commandTask(t *testing.T, payload []byte) frames.Task {
	t.Helper()
	wrapped := append([]byte{0x0a, byte(len(payload))}, payload...)
	tasks, _ := frames.Frame{Command: frames.TaskList, Payload: wrapped}.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("command payload %x does not hold one task", payload)
	}
	return tasks[0]
}

func (d *device) commands() []frames.Frame {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []frames.Frame
	for _, f := range d.sent {
		if f.Command == frames.TaskConfig {
			out = append(out, f)
		}
	}
	return out
}

func (d *device) list() []frames.Task {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]frames.Task(nil), d.tasks...)
}

func newTestBlocker(t *testing.T, tasks ...frames.Task) (*blocker, *device) {
	t.Helper()
	cfg := &config{serial: "HC31XXXXXXXXXXXX", blockTTL: time.Minute}
	b := newBlocker(cfg, io.Discard)
	b.ackTimeout, b.settle = 300*time.Millisecond, 300*time.Millisecond
	d := &device{t: t, b: b, tasks: tasks}
	return b, d
}

// connect brings the blocker online and delivers the device's list, as the
// answer to the 96/127 would.
func connect(b *blocker, d *device) {
	b.connected(d.send)
	b.listed(d.list())
}

// eventually waits for a condition the asynchronous answers bring about.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConnectAsksForTheList(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	b.connected(d.send)

	if len(d.sent) != 1 || d.sent[0].Command != frames.TaskQuery {
		t.Fatalf("got %v, want exactly one task list request", d.sent)
	}
}

func TestStartUpSwitchesAnEnabledTaskOff(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, true))
	connect(b, d)

	eventually(t, "the task to be switched off", func() bool { return !d.list()[0].Enabled })
	if n := len(d.commands()); n != 1 {
		t.Errorf("got %d commands, want 1", n)
	}
}

func TestSwitchingInTheAppStands(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)

	// Switched on in the app, without a request: reported, not corrected.
	b.listed([]frames.Task{chargeTask(7, true)})
	if n := len(d.commands()); n != 0 {
		t.Errorf("got %d commands, want none", n)
	}
	if s := b.state(); !s.Enabled || s.Requested {
		t.Errorf("got %+v, want enabled and not requested", s)
	}
}

func TestRequestSwitchesOnOnce(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)

	s, err := b.request(true)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if !s.Requested || !s.Enabled || s.Task != 7 || s.Until == "" {
		t.Errorf("got %+v, want task 7 requested and enabled", s)
	}

	// Renewing an enabled task sends nothing.
	if _, err := b.request(true); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if n := len(d.commands()); n != 1 {
		t.Errorf("got %d commands, want 1", n)
	}
}

func TestRequestOff(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)
	if _, err := b.request(true); err != nil {
		t.Fatalf("on: %v", err)
	}

	s, err := b.request(false)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if s.Requested || s.Enabled || s.Until != "" {
		t.Errorf("got %+v, want neither requested nor enabled", s)
	}
}

func TestExpiry(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)
	if _, err := b.request(true); err != nil {
		t.Fatalf("on: %v", err)
	}

	// Too early: nothing happens.
	b.expire()
	if !b.state().Requested {
		t.Fatal("the request ended before its time")
	}

	later := time.Now().Add(2 * time.Minute)
	b.now = func() time.Time { return later }
	b.expire()

	eventually(t, "the task to be switched off", func() bool { return !d.list()[0].Enabled })
	if b.state().Requested {
		t.Error("still requested after expiry")
	}
}

func TestRequestSurvivesAReconnect(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)
	if _, err := b.request(true); err != nil {
		t.Fatalf("on: %v", err)
	}

	// Gone while the connection was down - switched off in the app, say.
	b.disconnected()
	d.mu.Lock()
	d.tasks[0].Enabled = false
	d.mu.Unlock()
	connect(b, d)

	eventually(t, "the task to be switched back on", func() bool { return d.list()[0].Enabled })
}

func TestOffWhileDisconnectedStillEndsTheRequest(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)
	if _, err := b.request(true); err != nil {
		t.Fatalf("on: %v", err)
	}
	b.disconnected()

	if _, err := b.request(false); !errors.Is(err, errUnavailable) {
		t.Fatalf("got %v, want errUnavailable", err)
	}
	if b.state().Requested {
		t.Fatal("still requested")
	}

	// After the reconnect the task, still on, must not be switched on again -
	// nor off: the start-up rule is for the start only.
	before := len(d.commands())
	connect(b, d)
	if n := len(d.commands()); n != before {
		t.Errorf("got %d new commands, want none", n-before)
	}
}

func TestRequestNeedsAConnection(t *testing.T) {
	b, _ := newTestBlocker(t, chargeTask(7, false))

	_, err := b.request(true)
	if !errors.Is(err, errUnavailable) || statusFor(err) != http.StatusServiceUnavailable {
		t.Fatalf("got %v, want 503", err)
	}
	if b.state().Requested {
		t.Error("a request that could not be carried out was recorded")
	}
}

func TestRequestNeedsExactlyOneTask(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false), chargeTask(8, false))
	connect(b, d)

	_, err := b.request(true)
	if !errors.Is(err, errAmbiguous) || statusFor(err) != http.StatusConflict {
		t.Fatalf("got %v, want 409", err)
	}
	if n := len(d.commands()); n != 0 {
		t.Errorf("got %d commands, want none", n)
	}
	if s := b.state(); s.Problem == "" || s.Task != 0 {
		t.Errorf("got %+v, want the problem stated and no task", s)
	}
}

func TestNoAcknowledgement(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	d.silent = true
	connect(b, d)

	_, err := b.request(true)
	if !errors.Is(err, errNoAnswer) || statusFor(err) != http.StatusGatewayTimeout {
		t.Fatalf("got %v, want 504", err)
	}
}

// TestSwitchSendsTheListedTask checks the frame that goes out: the task as
// listed, only the switch changed.
func TestSwitchSendsTheListedTask(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)
	if _, err := b.request(true); err != nil {
		t.Fatalf("on: %v", err)
	}

	cmds := d.commands()
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	want, err := frames.BuildTaskSwitch("HC31XXXXXXXXXXXX", int(cmds[0].Seq), chargeTask(7, false), true)
	if err != nil {
		t.Fatal(err)
	}
	wantFrame, _ := frames.ParseReply(want)
	if string(cmds[0].Payload) != string(wantFrame.Payload) {
		t.Errorf("got payload %x, want %x", cmds[0].Payload, wantFrame.Payload)
	}
}
