package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/womat/ecoflow/internal/ecoflow"
)

// options is the raw command line, before validation.
type options struct {
	serial      string
	host        string
	broker      string
	topic       string
	mqttUser    string
	stdout      bool
	fast        bool
	switchEvery time.Duration
	verbose     bool
	version     bool
}

// config is the validated form.
type config struct {
	serial       string
	email        string
	password     string
	host         string
	broker       string
	topic        string
	mqttUser     string
	mqttPassword string
	stdout       bool
	fast         bool
	switchEvery  time.Duration
	verbose      bool
}

// backoff bounds the wait between reconnection attempts. The cloud being
// briefly unreachable is ordinary; hammering it is not.
const (
	backoffStart = 5 * time.Second
	backoffMax   = 15 * time.Minute
)

// defaultTopic is the prefix on the local broker.
//
// It lives here rather than only in the flag definition so that the flag and
// buildConfig cannot drift apart: an empty prefix means this, whichever way
// the configuration was assembled.
const defaultTopic = "ecoflow"

var errFlag = errors.New("flag error")

// newFlagSet defines the command line. Keep it in step with buildConfig.
func newFlagSet(o *options, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ecoflowd", flag.ContinueOnError)
	fs.SetOutput(stderr)

	fs.StringVar(&o.serial, "sn", "", "serial number of the device")
	fs.StringVar(&o.host, "host", "", "API host (default "+ecoflow.DefaultHost+")")
	fs.StringVar(&o.broker, "broker", "", "local MQTT broker, e.g. tcp://127.0.0.1:1883")
	fs.StringVar(&o.topic, "topic", defaultTopic, "topic prefix on the local broker")
	fs.StringVar(&o.mqttUser, "mqtt-user", "", "user for the local broker")
	fs.BoolVar(&o.stdout, "stdout", false, "print each reading")
	fs.BoolVar(&o.fast, "fast", false,
		"switch on the device's fast stream - this publishes, see the note below")
	fs.DurationVar(&o.switchEvery, "switch-every", 3*time.Second,
		"how often to renew the fast stream")
	fs.BoolVar(&o.verbose, "v", false, "report every frame that arrives")
	fs.BoolVar(&o.version, "version", false, "print the version and exit")

	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: ecoflowd --sn <serial> [options]

Reads an EcoFlow PowerOcean over the consumer app's cloud channel and keeps
reading it. Subscribing is all it does there, unless --fast is given.

With --broker the readings go to a local MQTT broker, one topic per value:
<topic>/<SN>/pv, /house, /battery, /grid, /dcdc, /soc, /measured, the day's
totals under /energy/, and /status as availability. Values carry the device's
own signs - positive grid is export, positive battery is charging - and watts
and watt-hours as measured. evcc turns those with scale: -1 and scale: 0.001.

credentials, from the environment and never from flags:
  ECOFLOW_EMAIL        account e-mail
  ECOFLOW_PASSWORD     account password. The login endpoint takes it
                       base64-encoded rather than hashed, and it is the account
                       password, not an application token - whatever holds it
                       should be readable by root alone.
  ECOFLOW_HOST         API host, overridden by --host
  MQTT_PASSWORD        password for the local broker, if it wants one. A flag
                       would put it in the process list for anyone to read.

options:
`)
		fs.PrintDefaults()
		fmt.Fprint(stderr, `
note on --fast:
  Without it the device reports once a minute and this program never publishes
  anything - measured at the device, the subscription alone is enough.

  With it, the message that switches on the device's fast stream goes to the
  .../set topic every --switch-every seconds, and readings arrive every two to
  three seconds instead. That topic is the one through which a device can
  actually be changed, which is why this is a flag and not a default: the write
  never happens as a side effect of asking for values.

  The message itself is not guesswork. It was captured off the wire while the
  phone app was running, and this program reproduces those bytes exactly,
  varying only the sequence number. It carries no parameters.

  Ten seconds was measured to be too slow - the device falls back to its minute
  cadence - so three is the default, which is what the app itself uses.

exit status:
  0  stopped on a signal
  1  usage or configuration error
  2  gave up after a failure that kept repeating
  78 the account credentials were rejected - waiting will not fix this, so the
     systemd unit should not restart on it

examples:
  ecoflowd --sn HC31XXXXXXXXXXXX --stdout
  ecoflowd --sn HC31XXXXXXXXXXXX --stdout --fast
  ecoflowd --sn HC31XXXXXXXXXXXX --broker tcp://127.0.0.1:1883
`)
	}

	return fs
}

func parseArgs(argv []string, stderr io.Writer) (*options, *flag.FlagSet, error) {
	opts := &options{}
	fs := newFlagSet(opts, stderr)

	if err := fs.Parse(argv[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, nil
		}
		return nil, nil, errFlag
	}
	if fs.NArg() > 0 {
		return nil, nil, fmt.Errorf("unexpected argument %q; see --help", fs.Arg(0))
	}
	return opts, fs, nil
}

// buildConfig validates the command line and the environment together.
func buildConfig(o *options, _ *flag.FlagSet) (*config, error) {
	c := &config{
		serial:      strings.TrimSpace(o.serial),
		host:        o.host,
		broker:      o.broker,
		topic:       o.topic,
		mqttUser:    o.mqttUser,
		stdout:      o.stdout,
		fast:        o.fast,
		switchEvery: o.switchEvery,
		verbose:     o.verbose,
		email:       os.Getenv("ECOFLOW_EMAIL"),
		password:    os.Getenv("ECOFLOW_PASSWORD"),
		// A flag would put the broker password in the process list, where
		// anyone on the machine can read it.
		mqttPassword: os.Getenv("MQTT_PASSWORD"),
	}

	if c.serial == "" {
		return nil, errors.New("--sn is required")
	}
	if strings.ContainsAny(c.serial, " \t/#+") {
		return nil, fmt.Errorf("serial number looks wrong: %q", c.serial)
	}
	if c.email == "" || c.password == "" {
		return nil, errors.New("ECOFLOW_EMAIL and ECOFLOW_PASSWORD must be set; see --help")
	}
	if c.host == "" {
		c.host = os.Getenv("ECOFLOW_HOST")
	}
	if c.topic = strings.Trim(c.topic, "/"); c.topic == "" {
		c.topic = defaultTopic
	}
	if strings.ContainsAny(c.topic, " \t#+") {
		return nil, fmt.Errorf("topic prefix must not contain spaces or MQTT wildcards: %q", c.topic)
	}
	if c.mqttUser != "" && c.broker == "" {
		return nil, errors.New("--mqtt-user without --broker has nothing to log in to")
	}
	if c.fast && c.switchEvery < time.Second {
		return nil, fmt.Errorf("--switch-every %s is too short; the app uses 3s", c.switchEvery)
	}

	return c, nil
}
