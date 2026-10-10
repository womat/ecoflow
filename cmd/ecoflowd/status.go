package main

import (
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/womat/ecoflow/internal/frames"
)

// status is what the web page shows: the last reading, the day's totals and
// how the connections stand.
//
// It lives as long as the process, like the blocker, so the page keeps its
// last values across a reconnect to the cloud and can say since when the
// connection is gone. It only ever looks back - nothing here reaches the
// device, and nothing on the page can make it.
//
// A nil *status is valid and does nothing, so the cloud side can feed it
// without asking whether there is a page at all.
type status struct {
	serial      string
	host        string
	started     time.Time
	fast        bool
	switchEvery time.Duration
	topic       string
	now         func() time.Time

	// mqtt says whether the local broker is configured and connected; nil
	// without --broker.
	mqtt   func() bool
	broker string

	mu        sync.Mutex
	cloud     string // the cloud broker while connected, else ""
	since     time.Time
	state     *state
	energy    *energy
	reports   []time.Time // energy frames from the device, the last minute
	telegrams []time.Time // state telegrams sent to the local broker, the last minute
	lastSeen  time.Time
}

// rateWindow is what "per minute" means on the page.
const rateWindow = time.Minute

func newStatus(cfg *config) *status {
	host, _ := os.Hostname()
	return &status{
		serial:      cfg.serial,
		host:        host,
		started:     time.Now(),
		fast:        cfg.fast,
		switchEvery: cfg.switchEvery,
		topic:       cfg.topic,
		now:         time.Now,
	}
}

// brokerHost is the broker as the page shows it: host and port, never a user
// or password someone wrote into the URL.
func brokerHost(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// cloudUp is called once the subscription stands.
func (s *status) cloudUp(address string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cloud, s.since = address, s.now()
}

// cloudDown is called when the connection ends; since then says how long ago.
func (s *status) cloudDown() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cloud != "" {
		s.cloud, s.since = "", s.now()
	}
}

// report counts an energy frame from the device, repeated or not: the page
// shows how often the device talks, not how often a value changes.
func (s *status) report() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.lastSeen = now
	s.reports = recent(append(s.reports, now), now)
}

// reading keeps the last measurement; published says whether it also went to
// the local broker as a telegram.
func (s *status) reading(e frames.Energy, published bool) {
	if s == nil {
		return
	}
	st := stateOf(s.serial, e)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = &st
	if published {
		now := s.now()
		s.telegrams = recent(append(s.telegrams, now), now)
	}
}

// totals keeps the day's energy so far.
func (s *status) totals(parts map[frames.Flow]frames.HourlyPart) {
	if s == nil {
		return
	}
	en := energyOf(s.serial, parts)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.energy = &en
}

// recent drops what is older than the rate window. The slices hold at most a
// minute of reports - twenty or so with the fast stream - so a linear trim is
// all this needs.
func recent(ts []time.Time, now time.Time) []time.Time {
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > rateWindow {
		i++
	}
	return ts[i:]
}

// statusView is the answer of GET /status. Times are RFC 3339 in UTC; the page
// shows them in the browser's zone.
type statusView struct {
	SN      string  `json:"sn"`
	Version string  `json:"version"`
	Host    string  `json:"host"`
	Uptime  int64   `json:"uptimeSeconds"`
	Cloud   cloudV  `json:"cloud"`
	MQTT    mqttV   `json:"mqtt"`
	State   *state  `json:"state"`
	Energy  *energy `json:"energy"`

	// Only with --block.
	Block   *blockView `json:"block,omitempty"`
	Listen  []string   `json:"listen,omitempty"`
	Callers []caller   `json:"callers,omitempty"`
}

type cloudV struct {
	Connected        bool   `json:"connected"`
	Server           string `json:"server,omitempty"`
	Since            string `json:"since,omitempty"`
	Fast             bool   `json:"fast"`
	SwitchEvery      int64  `json:"switchEverySeconds,omitempty"`
	ReportsPerMinute int    `json:"reportsPerMinute"`
	LastReport       string `json:"lastReport,omitempty"`
}

type mqttV struct {
	Configured         bool   `json:"configured"`
	Connected          bool   `json:"connected"`
	Broker             string `json:"broker,omitempty"`
	Topic              string `json:"topic"`
	TelegramsPerMinute int    `json:"telegramsPerMinute"`
}

// blockView is the block's state plus who renewed it last.
type blockView struct {
	blockState
	RenewedAt string `json:"renewedAt,omitempty"`
	RenewedBy string `json:"renewedBy,omitempty"`
}

func utc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// view is a copy of the current status, safe to encode.
func (s *status) view() statusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reports = recent(s.reports, now)
	s.telegrams = recent(s.telegrams, now)

	v := statusView{
		SN:      s.serial,
		Version: shortVersion(),
		Host:    s.host,
		Uptime:  int64(now.Sub(s.started).Seconds()),
		Cloud: cloudV{
			Connected:        s.cloud != "",
			Server:           s.cloud,
			Since:            utc(s.since),
			Fast:             s.fast,
			ReportsPerMinute: len(s.reports),
			LastReport:       utc(s.lastSeen),
		},
		MQTT: mqttV{
			Configured:         s.mqtt != nil,
			Broker:             s.broker,
			Topic:              s.topic,
			TelegramsPerMinute: len(s.telegrams),
		},
		State:  s.state,
		Energy: s.energy,
	}
	if s.fast {
		v.Cloud.SwitchEvery = int64(s.switchEvery.Seconds())
	}
	if s.mqtt != nil {
		v.MQTT.Connected = s.mqtt()
	}
	return v
}
