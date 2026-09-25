package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/womat/ecoflow/internal/frames"
)

// The two telegrams, under the prefix given with --topic. Lower case on
// purpose: MQTT topics are case-sensitive, and a subscription that differs
// in one letter gets silence rather than an error.
//
// Readings and totals come from different frames with different timestamps,
// so they go out separately. One object holding both would put fields of
// different age under a single time - the thing a telegram is meant to avoid.
const (
	leafState  = "state"
	leafEnergy = "energy"
)

// broker is the little of an MQTT client this needs. Naming it separately is
// what makes the publisher testable at all: the real client can only be built
// by connecting to something.
type broker interface {
	Publish(topic string, qos byte, retained bool, payload any) mqtt.Token
	Disconnect(quiesce uint)
}

// publisher writes readings to the local broker, one JSON telegram per
// measurement.
//
// Each telegram carries the serial and the device's own measurement time.
// The output used to lack the time, and three mechanisms stood in for it: a
// heartbeat so that "unchanged" could be told from "gone", an availability
// topic with a last will, and a staleness watch feeding it. A consumer that
// reads the time in the payload needs none of them - it sees the age of every
// value, by the device's clock rather than this program's. And only the
// consumer can notice a publisher that hangs: a hung process reports nothing,
// least of all that it hangs.
//
// Nothing is retained. A retained reading outlives the thing it describes;
// a consumer connecting later waits for the next one, at most a minute.
type publisher struct {
	client broker
	prefix string
	serial string
	stderr io.Writer
}

// state is one measurement. The field order is the order on the wire.
//
// Keys are camelCase: JSON prescribes no style, and this is the one the
// widely cited guides (Google, Microsoft, JSON:API) settle on.
//
// A field the frame did not carry goes out as 0 rather than being left out.
// That is not a shortcut: the device omits a field whose value is zero - the
// protobuf default - and in the captures the energy balance closes exactly
// whenever grid is absent. Leaving it out would drop grid from every telegram
// in the most common state, neither importing nor exporting.
//
// DCDC is decoded but deliberately not published. Its role is not settled -
// it is not part of the energy balance and follows the battery at a varying
// share - and a telegram should carry only what is understood. It comes back
// once it is.
type state struct {
	SN        string `json:"sn"`
	Timestamp string `json:"timestamp"`
	PV        int64  `json:"pv"`
	House     int64  `json:"house"`
	Battery   int64  `json:"battery"`
	Grid      int64  `json:"grid"`
	SoC       uint64 `json:"soc"`
}

// energy is the day's totals so far. Timestamp is that of the history frame,
// and the day it covers is the UTC day, because the device keeps its hours in
// UTC.
type energy struct {
	SN         string `json:"sn"`
	Timestamp  string `json:"timestamp"`
	PV         uint64 `json:"pv"`
	House      uint64 `json:"house"`
	BatteryIn  uint64 `json:"batteryIn"`
	BatteryOut uint64 `json:"batteryOut"`
	GridIn     uint64 `json:"gridIn"`
	GridOut    uint64 `json:"gridOut"`
}

// newPublisher connects to the local broker.
func newPublisher(cfg *config, stderr io.Writer) (*publisher, error) {
	broker, err := brokerURL(cfg.broker)
	if err != nil {
		return nil, err
	}

	p := &publisher{
		prefix: strings.Trim(cfg.topic, "/"),
		serial: cfg.serial,
		stderr: stderr,
	}

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID("ecoflowd-" + cfg.serial).
		SetCleanSession(true).
		SetConnectTimeout(15 * time.Second).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(time.Minute).
		SetConnectRetry(true).
		SetConnectRetryInterval(10 * time.Second)

	if cfg.mqttUser != "" {
		opts.SetUsername(cfg.mqttUser).SetPassword(cfg.mqttPassword)
	}
	if strings.HasPrefix(broker, "ssl://") || strings.HasPrefix(broker, "tls://") {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	opts.SetOnConnectHandler(func(mqtt.Client) {
		fmt.Fprintf(stderr, "publishing to %s on %s/%s and %s/%s\n",
			broker, p.prefix, leafState, p.prefix, leafEnergy)
	})

	c := mqtt.NewClient(opts)
	p.client = c
	// ConnectRetry is on, so this returns once the first attempt is under way
	// rather than failing when the broker is not up yet. A service that starts
	// before mosquitto should wait for it, not exit.
	token := c.Connect()
	if !token.WaitTimeout(15*time.Second) && !c.IsConnected() {
		fmt.Fprintf(stderr, "warning: %s did not answer yet; retrying in the background\n", broker)
	} else if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", broker, err)
	}

	return p, nil
}

// brokerURL turns what was typed into what paho wants.
func brokerURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("no broker given")
	}
	if !strings.Contains(s, "://") {
		s = "tcp://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("broker %q is not an address: %w", s, err)
	}
	switch u.Scheme {
	case "tcp", "ssl", "tls", "ws", "wss":
	default:
		return "", fmt.Errorf("broker scheme %q is not one of tcp, ssl, ws, wss", u.Scheme)
	}
	if u.Port() == "" {
		port := "1883"
		if u.Scheme == "ssl" || u.Scheme == "tls" {
			port = "8883"
		}
		u.Host = u.Host + ":" + port
	}
	return u.String(), nil
}

// stateOf builds the telegram for one reading.
func stateOf(serial string, e frames.Energy) state {
	return state{
		SN:        serial,
		Timestamp: e.Measured.Format(time.RFC3339),
		PV:        watts(e.PV),
		House:     watts(e.House),
		Battery:   watts(e.Battery),
		Grid:      watts(e.Grid),
		SoC:       e.SoC,
	}
}

// energyOf builds the telegram for a set of hourly parts. The caller only
// hands over complete sets, and all parts of a set share one timestamp.
func energyOf(serial string, parts map[frames.Flow]frames.HourlyPart) energy {
	total := func(f frames.Flow) uint64 { return parts[f].Total() }

	return energy{
		SN:         serial,
		Timestamp:  parts[frames.FlowPV].When.Format(time.RFC3339),
		PV:         total(frames.FlowPV),
		House:      total(frames.FlowHouse),
		BatteryIn:  total(frames.FlowBatteryIn),
		BatteryOut: total(frames.FlowBatteryOut),
		GridIn:     total(frames.FlowGridIn),
		GridOut:    total(frames.FlowGridOut),
	}
}

// energy publishes one reading.
func (p *publisher) energy(e frames.Energy) {
	p.send(leafState, stateOf(p.serial, e))
}

// totals publishes the day's energy so far.
func (p *publisher) totals(parts map[frames.Flow]frames.HourlyPart) {
	p.send(leafEnergy, energyOf(p.serial, parts))
}

func (p *publisher) send(leaf string, v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		// Both types are flat structs of strings and integers, so this does
		// not happen - but saying so beats publishing an empty message.
		fmt.Fprintf(p.stderr, "error: encoding the %s telegram: %v\n", leaf, err)
		return
	}
	p.client.Publish(p.prefix+"/"+leaf, 0, false, payload)
}

// close hangs up.
func (p *publisher) close() {
	p.client.Disconnect(250)
}

// watts rounds a power value to whole watts.
//
// The device's own sign is kept: positive grid means export and positive
// battery means charging, which is the opposite of what evcc expects. Turning
// it here would bury a measured finding inside a conversion - evcc has
// scale: -1 for exactly this, and the README says so.
func watts(v float32) int64 {
	return int64(math.Round(float64(v)))
}
