package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/womat/ecoflow/internal/ecoflow"
	"github.com/womat/ecoflow/internal/frames"
)

// serve connects, reads, and keeps doing so until the context ends.
//
// Every failure that waiting might fix leads back into the loop with a longer
// pause. The one that waiting never fixes - credentials the cloud rejected -
// ends the program with its own status, so the unit can decline to restart it.
func serve(ctx context.Context, cfg *config, stdout, stderr io.Writer) int {
	// The local broker is independent of the cloud: it stays connected across
	// every reconnection attempt, so a consumer keeps seeing the availability
	// topic even while the cloud side is down. That is the whole point of it.
	var out *publisher
	if cfg.broker != "" {
		p, err := newPublisher(cfg, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return exitUsage
		}
		defer p.close()
		out = p
	}

	// The session is held here rather than fetched per attempt. Logging in is
	// what costs something - an undocumented endpoint, the account password on
	// the wire - and a token lasts about a month, so a network that comes and
	// goes must not turn into a login each time it does.
	var s ecoflow.Session
	wait := backoffStart

	for {
		connected, err := session(ctx, cfg, &s, out, stdout, stderr)

		// A connection that actually stood is not something to back off from.
		// Without this the wait only ever grows: a service that reconnects
		// once an hour reaches the quarter-hour ceiling after half a day, and
		// from then on every brief interruption costs fifteen minutes of
		// silence - however healthy the network is by then.
		if connected {
			wait = backoffStart
		}

		switch {
		case ctx.Err() != nil:
			fmt.Fprintln(stderr, "stopping")
			return exitOK
		case errors.Is(err, ecoflow.ErrCredentials):
			fmt.Fprintln(stderr, "error:", err)
			fmt.Fprintln(stderr, "stopping: check ECOFLOW_EMAIL and ECOFLOW_PASSWORD")
			fmt.Fprintln(stderr, "if those are right, the cloud refused for a reason of its own -",
				"the codes are documented nowhere, and a restart is then the way back")
			return exitCredentials
		case err != nil:
			fmt.Fprintf(stderr, "error: %v; retrying in %s\n", err, wait)
		}

		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "stopping")
			return exitOK
		case <-time.After(wait):
		}

		// Back off towards a quarter hour. The cloud being briefly away is
		// ordinary; knocking every five seconds for hours is not.
		if wait *= 2; wait > backoffMax {
			wait = backoffMax
		}
	}
}

// session runs one connection from start to finish. The bool reports whether
// the subscription ever stood, which is what tells a failed attempt apart from
// a connection that worked and later dropped.
func session(ctx context.Context, cfg *config, s *ecoflow.Session,
	out *publisher, stdout, stderr io.Writer) (bool, error) {

	client := &ecoflow.Client{Host: cfg.host}

	if s.Token == "" {
		got, err := client.Login(ctx, cfg.email, cfg.password)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(stderr, "logged in as user %s\n", got.UserID)
		*s = got
	}

	broker, err := client.Certification(ctx, *s)
	if err != nil {
		// Only throw the token away when the cloud actually turned it down -
		// they last about a month and do expire, so a dead one has to be
		// noticed. A line that was briefly gone is not that: discarding the
		// token there would turn every hiccup into another login, and the
		// login is the one request carrying the account password.
		if errors.Is(err, ecoflow.ErrTokenRejected) {
			*s = ecoflow.Session{}
		}
		return false, fmt.Errorf("fetch broker credentials: %w", err)
	}

	topics := ecoflow.TopicsFor(s.UserID, cfg.serial)
	return listen(ctx, cfg, out, broker, *s, topics, stdout, stderr)
}

// listen subscribes and reports what arrives, until the connection or the
// context ends.
func listen(ctx context.Context, cfg *config, out *publisher, broker ecoflow.Broker,
	s ecoflow.Session, topics ecoflow.Topics, stdout, stderr io.Writer) (bool, error) {

	id, err := ecoflow.ClientID(s.UserID)
	if err != nil {
		return false, err
	}

	// Paho's own reconnect is deliberately off: it would reconnect with the
	// same client id, and this broker refuses an id it has already seen. Every
	// attempt needs a fresh one, so reconnecting is the outer loop's job.
	opts := mqtt.NewClientOptions().
		AddBroker("ssl://" + broker.Address()).
		SetClientID(id).
		SetUsername(broker.Account).
		SetPassword(broker.Password).
		SetCleanSession(true).
		SetAutoReconnect(false).
		SetConnectTimeout(30 * time.Second).
		SetKeepAlive(60 * time.Second).
		SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})

	lost := make(chan error, 1)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		select {
		case lost <- err:
		default:
		}
	})

	incoming := make(chan []byte, 256)
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		select {
		case incoming <- m.Payload():
		default: // never block the MQTT thread on a slow reader
		}
	})

	c := mqtt.NewClient(opts)
	// Registered before the check on purpose: if Connect times out here while
	// paho completes the handshake a moment later, the client would otherwise
	// be left connected with nobody holding a reference - and its client id
	// burnt, since the broker refuses one it has already seen.
	defer c.Disconnect(250)

	if token := c.Connect(); !token.WaitTimeout(30*time.Second) || token.Error() != nil {
		return false, fmt.Errorf("connect to %s: %w", broker.Address(), tokenErr(token))
	}

	for _, topic := range topics.Subscribe() {
		if token := c.Subscribe(topic, 0, nil); !token.WaitTimeout(30*time.Second) || token.Error() != nil {
			return false, fmt.Errorf("subscribe to %s: %w", topic, tokenErr(token))
		}
	}
	fmt.Fprintf(stderr, "connected to %s, subscribed to %d topics\n",
		broker.Address(), len(topics.Subscribe()))

	// The connection lives only as long as this function, and so must the
	// switch loop: a goroutine publishing onto a client that has been
	// disconnected would fail quietly forever.
	connCtx, done := context.WithCancel(ctx)
	defer done()

	var fast fastStream
	if cfg.fast {
		fmt.Fprintf(stderr, "switching on the fast stream every %s (publishes to %s)\n",
			cfg.switchEvery, topics.Set)
		go keepFastStream(connCtx, c, cfg, topics.Set, stderr)
		fast.start(cfg.switchEvery)
	}

	var seen readings
	day := newDailyTotals()

	// Readings stopping is not an event, only an absence, so it has to be
	// looked for rather than waited for.
	stale := time.NewTicker(30 * time.Second)
	defer stale.Stop()

	for {
		select {
		case <-ctx.Done():
			return true, nil
		case err := <-lost:
			return true, fmt.Errorf("connection lost: %w", err)
		case <-fast.deadline():
			fast.complain(stderr)
		case now := <-stale.C:
			if out != nil {
				out.check(now)
			}
		case payload := <-incoming:
			f, err := frames.Parse(payload)
			if err != nil {
				continue // not every frame on these topics is one we know
			}
			if cfg.verbose {
				fmt.Fprintf(stderr, "frame %v, %d bytes\n", f.Command, len(f.Payload))
			}
			if f.Command == frames.Fast {
				fast.arrived()
			}

			if part, ok := f.Hourly(); ok {
				if parts, complete := day.add(part); complete && out != nil {
					out.totals(parts, time.Now())
				}
				continue
			}

			line, e, ok := seen.add(f)
			if !ok {
				continue
			}
			if cfg.stdout {
				fmt.Fprintln(stdout, line)
			}
			if out != nil {
				out.energy(e, time.Now())
			}
		}
	}
}

// dailyTotals collects the six parts of the hourly history.
//
// They arrive one per flow, all carrying the same timestamp, so the set is
// only worth reporting once it is complete - publishing a partial set would
// mean totals that disagree with each other.
type dailyTotals struct {
	when  int64
	parts map[frames.Flow]frames.HourlyPart
}

func newDailyTotals() *dailyTotals {
	return &dailyTotals{parts: map[frames.Flow]frames.HourlyPart{}}
}

func (d *dailyTotals) add(p frames.HourlyPart) (map[frames.Flow]frames.HourlyPart, bool) {
	if when := p.When.Unix(); when != d.when {
		d.when, d.parts = when, map[frames.Flow]frames.HourlyPart{}
	}
	d.parts[p.Flow] = p
	return d.parts, len(d.parts) == len(frames.Flows)
}

// keepFastStream renews the device's fast stream until the connection ends.
//
// The switch only holds for a short while, so it has to be repeated; ten
// seconds was already too slow when measured, and the app itself sends it
// every three. The sequence number wraps at 127 to stay a single-byte varint,
// which is what the captured frames do.
func keepFastStream(ctx context.Context, c mqtt.Client, cfg *config, topic string, stderr io.Writer) {
	seq := 1
	for {
		frame, err := frames.BuildStreamSwitch(cfg.serial, seq)
		if err != nil {
			fmt.Fprintln(stderr, "error: building the stream switch:", err)
			return
		}

		token := c.Publish(topic, 1, false, frame)
		if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
			fmt.Fprintln(stderr, "warning: the stream switch was not accepted:", tokenErr(token))
		}

		seq = seq%frames.MaxSwitchSeq + 1

		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.switchEvery):
		}
	}
}

// fastStream watches whether asking for the fast stream actually achieved
// anything.
//
// The broker accepts the switch either way - it answered PUBACK RC:0 when this
// was measured - so acceptance proves nothing. What proves it is whether fast
// reports turn up. If they do not, saying so once is the difference between a
// coarse graph and a mystery; saying it every three seconds would be noise.
type fastStream struct {
	timer     *time.Timer
	satisfied bool
	warned    bool
}

func (f *fastStream) start(every time.Duration) {
	// Generous on purpose: the first switch and the device's answer both take
	// a moment, and a false alarm here would be worse than a late one.
	f.timer = time.NewTimer(max(20*every, 60*time.Second))
}

func (f *fastStream) deadline() <-chan time.Time {
	if f.timer == nil {
		return nil
	}
	return f.timer.C
}

func (f *fastStream) arrived() { f.satisfied = true }

func (f *fastStream) complain(stderr io.Writer) {
	// Both guards earn their keep. Clearing the timer stops the select from
	// firing again; warned keeps the promise local, so the single complaint
	// does not depend on how the one caller happens to be shaped.
	f.timer = nil
	if f.satisfied || f.warned {
		return
	}
	f.warned = true
	fmt.Fprintln(stderr,
		"warning: --fast is on but no fast reports arrived; continuing at the minute cadence")
}

func tokenErr(t mqtt.Token) error {
	if err := t.Error(); err != nil {
		return err
	}
	return errors.New("timed out")
}
