package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/womat/ecoflow/internal/frames"
)

// Payloads of the availability topic. These are Home Assistant's defaults for
// an availability_topic, so it needs no extra configuration to understand them.
const (
	payloadOnline  = "online"
	payloadOffline = "offline"
)

// staleAfter is how long without a reading counts as offline.
//
// The device reports once a minute even when nothing else is running, so three
// missed minutes mean something is wrong rather than quiet. The device's own
// status topic would be the better source, but nothing was ever seen on it -
// see api-status.md - so this infers from the data instead of decoding a
// payload nobody has observed.
const staleAfter = 3 * time.Minute

// heartbeat is how often unchanged values are published again.
//
// Only changes are published otherwise, which keeps the broker quiet. Without
// a heartbeat a consumer cannot tell "unchanged" from "gone", and both evcc
// and Home Assistant want a value often enough to decide that themselves.
const heartbeat = 60 * time.Second

// publisher writes readings to the local broker.
//
// Measurements are published without the retain flag on purpose. A retained
// reading outlives the thing it describes: after a cloud outage a consumer
// reads the last one forever and acts on it, and Home Assistant's own
// documentation warns that retained values collide with expire_after and leave
// entities behind. Availability is retained, because that one is a fact about
// the publisher and stays true until it changes.
type publisher struct {
	client mqtt.Client
	prefix string
	stderr io.Writer

	last     map[string]string
	lastSent time.Time
	seen     time.Time
	online   bool
}

// topicFor builds one topic. The device is named by its serial: it is the one
// name a device actually has, and a second one would be a second thing to keep
// in step for nothing.
func topicFor(prefix, serial, leaf string) string {
	return strings.Trim(prefix, "/") + "/" + serial + "/" + leaf
}

// newPublisher connects to the local broker.
//
// The last will is set before connecting, which is the only time it can be:
// it is what the broker sends on this program's behalf when it stops saying
// anything, including when it is killed outright.
func newPublisher(cfg *config, stderr io.Writer) (*publisher, error) {
	broker, err := brokerURL(cfg.broker)
	if err != nil {
		return nil, err
	}

	status := topicFor(cfg.topic, cfg.serial, "status")

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID("ecoflowd-"+cfg.serial).
		SetCleanSession(true).
		SetWill(status, payloadOffline, 0, true).
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

	// Announce as soon as a connection stands, including after a reconnect -
	// the broker discarded the retained "online" when the will fired.
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		c.Publish(status, 0, true, payloadOnline)
		fmt.Fprintf(stderr, "publishing to %s under %s\n", broker,
			topicFor(cfg.topic, cfg.serial, "")+"…")
	})

	c := mqtt.NewClient(opts)
	// ConnectRetry is on, so this returns once the first attempt is under way
	// rather than failing when the broker is not up yet. A service that starts
	// before mosquitto should wait for it, not exit.
	token := c.Connect()
	if !token.WaitTimeout(15*time.Second) && !c.IsConnected() {
		fmt.Fprintf(stderr, "warning: %s did not answer yet; retrying in the background\n", broker)
	} else if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", broker, err)
	}

	return &publisher{
		client: c,
		prefix: strings.Trim(cfg.topic, "/") + "/" + cfg.serial,
		stderr: stderr,
		last:   map[string]string{},
		online: true,
	}, nil
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

// energy publishes one reading.
func (p *publisher) energy(e frames.Energy, now time.Time) {
	p.seen = now
	p.setOnline(true)

	force := now.Sub(p.lastSent) >= heartbeat
	if force {
		p.lastSent = now
	}

	p.put("pv", watts(e.PV), force)
	p.put("house", watts(e.House), force)
	p.put("battery", watts(e.Battery), force)
	p.put("grid", watts(e.Grid), force)
	p.put("dcdc", watts(e.DCDC), force)
	p.put("soc", strconv.FormatUint(e.SoC, 10), force)
	p.put("measured", e.Measured.Format(time.RFC3339), force)
}

// totals publishes the day's energy so far, one topic per flow.
func (p *publisher) totals(parts map[frames.Flow]frames.HourlyPart, now time.Time) {
	force := now.Sub(p.lastSent) >= heartbeat
	for _, flow := range frames.Flows {
		part, ok := parts[flow]
		if !ok {
			continue
		}
		p.put("energy/"+flow.String(), strconv.FormatUint(part.Total(), 10), force)
	}
}

// check marks the device offline when readings stop arriving.
func (p *publisher) check(now time.Time) {
	if p.seen.IsZero() {
		return
	}
	p.setOnline(now.Sub(p.seen) < staleAfter)
}

func (p *publisher) setOnline(online bool) {
	if online == p.online {
		return
	}
	p.online = online

	payload := payloadOffline
	if online {
		payload = payloadOnline
		fmt.Fprintln(p.stderr, "readings are arriving again")
	} else {
		fmt.Fprintf(p.stderr, "no reading for %s; reporting offline\n", staleAfter)
	}
	p.client.Publish(p.prefix+"/status", 0, true, payload)
}

// put publishes one value when it changed, or when the heartbeat is due.
func (p *publisher) put(leaf, value string, force bool) {
	if !force && p.last[leaf] == value {
		return
	}
	p.last[leaf] = value
	p.client.Publish(p.prefix+"/"+leaf, 0, false, value)
}

// close says offline and hangs up, so a clean stop is not reported as a crash.
func (p *publisher) close() {
	p.client.Publish(p.prefix+"/status", 0, true, payloadOffline).WaitTimeout(2 * time.Second)
	p.client.Disconnect(250)
}

// watts renders a power value.
//
// Rounded to whole watts and with the device's own sign: positive grid means
// export and positive battery means charging, which is the opposite of what
// evcc expects. Turning it here would bury a measured finding inside a
// conversion - evcc has scale: -1 for exactly this, and the README says so.
func watts(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', 0, 32)
}
