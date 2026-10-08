package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/womat/ecoflow/internal/frames"
)

// The discharge block: a scheduled task of type "Laden des Akkus", set up in
// the app, switched on and off from here.
//
// The app task decides *when* a block may apply - its window and repetition
// are the frame within which it can take effect. This program decides only
// *whether*, on request over HTTPS, and never touches the times. Measured on
// the DC Fit (docs/research/api-status.md, section 3): enabled from outside the app such a
// task stops the battery discharging about 25 s later, and disabling it frees
// the battery within one to one and a half minutes.
//
// The rules, settled with the maintainer on 3 October 2026:
//
//   - Exactly one task of type 1 is the block. None or several, and nothing
//     is switched; --block-task names one explicitly. Nothing is guessed.
//   - A request holds for --block-ttl. Unless it is renewed, the task is
//     switched off again. A request survives a reconnection to the cloud;
//     a restart of the program does not - at start-up an enabled task is
//     switched off.
//   - What is switched in the app stands. This program acts on a request, on
//     the end of one and at start-up, and does not keep correcting the device
//     towards what it last asked for. That keeps switching by hand possible.

// errNoTask and errAmbiguous say why no task can be switched.
var (
	errNoTask    = errors.New("no task to switch")
	errAmbiguous = errors.New("more than one task to switch")
)

// pickTask finds the block task in a list.
func pickTask(tasks []frames.Task, fixed uint64) (frames.Task, error) {
	describe := func(ts []frames.Task) string {
		var parts []string
		for _, t := range ts {
			parts = append(parts, fmt.Sprintf("%d (type %d, %s)", t.Number, t.Type, t.WindowText()))
		}
		if len(parts) == 0 {
			return "none"
		}
		return strings.Join(parts, ", ")
	}

	if fixed != 0 {
		for _, t := range tasks {
			if t.Number != fixed {
				continue
			}
			if t.Type != frames.TypeCharge {
				return frames.Task{}, fmt.Errorf("%w: task %d is of type %d, not %d (\"Laden des Akkus\")",
					errNoTask, fixed, t.Type, frames.TypeCharge)
			}
			return t, nil
		}
		return frames.Task{}, fmt.Errorf("%w: task %d is not in the device's list; it has %s",
			errNoTask, fixed, describe(tasks))
	}

	var charge []frames.Task
	for _, t := range tasks {
		if t.Type == frames.TypeCharge {
			charge = append(charge, t)
		}
	}
	switch len(charge) {
	case 1:
		return charge[0], nil
	case 0:
		return frames.Task{}, fmt.Errorf("%w: no task of type \"Laden des Akkus\" is set up in the app; the device lists %s",
			errNoTask, describe(tasks))
	default:
		return frames.Task{}, fmt.Errorf("%w: %s; remove all but one in the app or choose one with --block-task",
			errAmbiguous, describe(charge))
	}
}

// blockState is what GET /block answers and what goes out as the block
// telegram. Requested and Until are this program's; the rest is the device's
// list as last received.
type blockState struct {
	SN string `json:"sn"`

	// Timestamp is when the list was received, in UTC. The list frames carry
	// no time of their own, unlike the energy reports, so this is the closest
	// there is; empty until a list has arrived.
	Timestamp string `json:"timestamp"`

	Task      uint64 `json:"task"` // 0 while there is no single task to switch
	Requested bool   `json:"requested"`
	Until     string `json:"until,omitempty"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Window    string `json:"window,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

// blocker holds the block's state between the HTTPS side and the cloud
// connection. The connection comes and goes; the blocker lives as long as the
// process, so that a request outlasts a reconnect.
type blocker struct {
	serial string
	ttl    time.Duration
	fixed  uint64
	stderr io.Writer

	// Overridable in tests.
	now        func() time.Time
	ackTimeout time.Duration
	settle     time.Duration

	// publish sends the block telegram; nil without a broker.
	publish func(blockState)

	mu       sync.Mutex
	send     func([]byte) error // nil while there is no connection
	tasks    []frames.Task
	known    bool // a list has arrived on the current connection
	fresh    bool // the next list is the first on a new connection
	started  bool // the start-up check has run
	listedAt time.Time
	want     bool
	until    time.Time
	expiry   *time.Timer
	seq      int
	waiting  map[int]chan struct{}
	changed  chan struct{} // closed and replaced whenever a list arrives
}

func newBlocker(cfg *config, stderr io.Writer) *blocker {
	return &blocker{
		serial:     cfg.serial,
		ttl:        cfg.blockTTL,
		fixed:      cfg.blockTask,
		stderr:     stderr,
		now:        time.Now,
		ackTimeout: 10 * time.Second,
		settle:     5 * time.Second,
		waiting:    map[int]chan struct{}{},
		changed:    make(chan struct{}),
	}
}

// connected is called once the cloud subscription stands. It asks for the
// task list, since the device pushes one only on a change.
func (b *blocker) connected(send func([]byte) error) {
	b.mu.Lock()
	b.send, b.known, b.fresh = send, false, true
	seq := b.nextSeq()
	b.mu.Unlock()

	frame, err := frames.BuildTaskQuery(b.serial, seq)
	if err == nil {
		err = send(frame)
	}
	if err != nil {
		fmt.Fprintln(b.stderr, "warning: asking for the task list failed:", err)
	}
}

// disconnected is called when the connection ends.
func (b *blocker) disconnected() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.send, b.known = nil, false
}

// nextSeq hands out sequence numbers. It wraps at the one-byte limit, like the
// stream switch: the device answers with the number it was sent, so any
// distinct small number serves to match the answer. Callers hold mu.
func (b *blocker) nextSeq() int {
	b.seq = b.seq%frames.MaxSwitchSeq + 1
	return b.seq
}

// acknowledged is called for every 96/125 answer on set_reply.
func (b *blocker) acknowledged(seq uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.waiting[int(seq)]; ok {
		close(ch)
		delete(b.waiting, int(seq))
	}
}

// listed is called for every task list, pushed (96/10) or requested (96/127).
func (b *blocker) listed(tasks []frames.Task) {
	b.mu.Lock()
	b.tasks, b.known, b.listedAt = tasks, true, b.now()
	close(b.changed)
	b.changed = make(chan struct{})

	fresh := b.fresh
	b.fresh = false

	// Whatever the list says, only these two moments lead to a write here;
	// any other list is just reported. See the rules at the top.
	var switchTo *bool
	if t, err := pickTask(tasks, b.fixed); err == nil {
		switch {
		case !b.started && t.Enabled && !b.want:
			off := false
			switchTo = &off
			fmt.Fprintf(b.stderr, "block: task %d was left enabled; switching it off at start-up\n", t.Number)
		case fresh && b.want && !t.Enabled:
			on := true
			switchTo = &on
			fmt.Fprintf(b.stderr, "block: task %d is off after reconnecting; switching it back on\n", t.Number)
		}
	}
	b.started = true
	state := b.stateLocked()
	b.mu.Unlock()

	if switchTo != nil {
		if _, err := b.switchTask(*switchTo, false); err != nil {
			fmt.Fprintln(b.stderr, "warning: block:", err)
		}
	}
	if b.publish != nil {
		b.publish(state)
	}
}

// stateLocked describes the block as things stand. Callers hold mu.
func (b *blocker) stateLocked() blockState {
	s := blockState{SN: b.serial, Requested: b.want}
	if b.want {
		s.Until = b.until.UTC().Format(time.RFC3339)
	}
	if !b.listedAt.IsZero() {
		s.Timestamp = b.listedAt.UTC().Format(time.RFC3339)
	}
	if !b.known {
		s.Problem = "no task list from the device yet"
		return s
	}
	t, err := pickTask(b.tasks, b.fixed)
	if err != nil {
		s.Problem = err.Error()
		return s
	}
	s.Task, s.Enabled, s.Running, s.Window = t.Number, t.Enabled, t.Running, t.WindowText()
	return s
}

func (b *blocker) state() blockState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stateLocked()
}

// errUnavailable and errNoAnswer map to 503 and 504.
var (
	errUnavailable = errors.New("not connected to the device, or no task list yet")
	errNoAnswer    = errors.New("the device did not acknowledge the command")
)

// request switches the block on (renewing it) or off, as asked over HTTPS.
func (b *blocker) request(on bool) (blockState, error) {
	b.mu.Lock()
	if on {
		// A request that cannot be carried out now is not recorded: the
		// caller is told so and asks again, rather than finding a block
		// switched on later by a reconnect it never heard of.
		if b.send == nil || !b.known {
			b.mu.Unlock()
			return b.state(), errUnavailable
		}
		if _, err := pickTask(b.tasks, b.fixed); err != nil {
			b.mu.Unlock()
			return b.state(), err
		}
		b.want, b.until = true, b.now().Add(b.ttl)
		if b.expiry != nil {
			b.expiry.Stop()
		}
		b.expiry = time.AfterFunc(b.ttl, b.expire)
	} else {
		// Cleared before anything can fail: a block that was called off must
		// not come back on after the next reconnect.
		b.want = false
		if b.expiry != nil {
			b.expiry.Stop()
			b.expiry = nil
		}
	}
	b.mu.Unlock()

	if _, err := b.switchTask(on, true); err != nil {
		return b.state(), err
	}
	return b.state(), nil
}

// expire ends a request that was not renewed.
func (b *blocker) expire() {
	b.mu.Lock()
	if !b.want || b.now().Before(b.until) {
		b.mu.Unlock()
		return
	}
	b.want, b.expiry = false, nil
	b.mu.Unlock()

	fmt.Fprintf(b.stderr, "block: not renewed within %s; switching off\n", b.ttl)
	if _, err := b.switchTask(false, false); err != nil {
		fmt.Fprintln(b.stderr, "warning: block:", err)
	}
}

// switchTask sends the command that brings the task to the wanted state, if
// it is not there already. With wait it waits for the acknowledgement and
// briefly for the list that shows the change.
func (b *blocker) switchTask(on, wait bool) (bool, error) {
	b.mu.Lock()
	if b.send == nil || !b.known {
		b.mu.Unlock()
		return false, errUnavailable
	}
	t, err := pickTask(b.tasks, b.fixed)
	if err != nil {
		b.mu.Unlock()
		return false, err
	}
	if t.Enabled == on {
		b.mu.Unlock()
		return false, nil
	}

	seq := b.nextSeq()
	frame, err := frames.BuildTaskSwitch(b.serial, seq, t, on)
	if err != nil {
		b.mu.Unlock()
		return false, err
	}
	acked := make(chan struct{})
	b.waiting[seq] = acked
	changed, send := b.changed, b.send
	b.mu.Unlock()

	word := map[bool]string{true: "on", false: "off"}[on]
	fmt.Fprintf(b.stderr, "block: switching task %d %s\n", t.Number, word)
	if err := send(frame); err != nil {
		b.forget(seq)
		return true, fmt.Errorf("sending the command: %w", err)
	}
	if !wait {
		return true, nil
	}

	select {
	case <-acked:
	case <-time.After(b.ackTimeout):
		b.forget(seq)
		return true, errNoAnswer
	}

	// The list that shows the new state follows the acknowledgement by about
	// a second. Answering with it beats answering with the old one.
	deadline := time.After(b.settle)
	for {
		select {
		case <-changed:
		case <-deadline:
			return true, nil
		}
		b.mu.Lock()
		cur, err := pickTask(b.tasks, b.fixed)
		changed = b.changed
		b.mu.Unlock()
		if err != nil || cur.Enabled == on {
			return true, nil
		}
	}
}

func (b *blocker) forget(seq int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiting, seq)
}

// statusFor maps the reasons a switch failed to HTTP.
func statusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, errUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, errNoTask), errors.Is(err, errAmbiguous):
		return http.StatusConflict
	case errors.Is(err, errNoAnswer):
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
