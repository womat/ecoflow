package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
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
	block       bool
	listen      string
	tlsCert     string
	tlsKey      string
	blockTTL    time.Duration
	blockTask   uint64
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

	block          bool
	listen         []string
	tlsCertificate tls.Certificate
	httpToken      string
	blockTTL       time.Duration
	blockTask      uint64
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
	fs.StringVar(&o.topic, "topic", defaultTopic,
		"topic prefix on the local broker; readings go to <topic>/state and <topic>/energy")
	fs.StringVar(&o.mqttUser, "mqtt-user", "", "user for the local broker")
	fs.BoolVar(&o.stdout, "stdout", false, "print each reading")
	fs.BoolVar(&o.fast, "fast", false,
		"switch on the device's fast stream - this publishes, see the note below")
	fs.DurationVar(&o.switchEvery, "switch-every", 3*time.Second,
		"how often to renew the fast stream")
	fs.BoolVar(&o.block, "block", false,
		"switch the discharge block task on request - this publishes, see the note below")
	fs.StringVar(&o.listen, "listen", "",
		"addresses for the web page and the --block endpoint, comma-separated, e.g. 172.17.0.1,192.168.1.10 (port 8089 if none is given)")
	fs.StringVar(&o.tlsCert, "tls-cert", "", "certificate file for --listen")
	fs.StringVar(&o.tlsKey, "tls-key", "", "private key file for --listen")
	fs.DurationVar(&o.blockTTL, "block-ttl", defaultBlockTTL,
		"how long a block request holds unless renewed (1m to 15m)")
	fs.Uint64Var(&o.blockTask, "block-task", 0,
		"number of the task to switch; by default the only one of type \"Laden des Akkus\"")
	fs.BoolVar(&o.verbose, "v", false, "report every frame that arrives")
	fs.BoolVar(&o.version, "version", false, "print the version and exit")

	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: ecoflowd --sn <serial> [options]

Reads an EcoFlow PowerOcean over the consumer app's cloud channel and keeps
reading it. Nothing is ever sent to the device unless --fast or --block is
given.

With --broker the readings go to a local MQTT broker as two JSON telegrams,
neither retained:

  <topic>/state   one per reading: sn, timestamp, pv, house, battery, grid, soc
  <topic>/energy  the day's totals: sn, timestamp, pv, house, batteryIn,
                  batteryOut, gridIn, gridOut

timestamp is the device's measurement time in UTC, not the time of sending, so
a consumer checks a value's age there; there is no availability topic. The
totals cover the UTC day. Values carry the device's own signs - positive grid
is export, positive battery is charging - and watts and watt-hours as
measured; evcc turns those with scale: -1 and scale: 0.001. A field the device
did not send is 0, because the device leaves out fields that are zero.

credentials, from the environment and never from flags:
  ECOFLOW_EMAIL        account e-mail
  ECOFLOW_PASSWORD     account password. The login endpoint takes it
                       base64-encoded rather than hashed, and it is the account
                       password, not an application token - whatever holds it
                       should be readable by root alone.
  ECOFLOW_HOST         API host, overridden by --host
  MQTT_PASSWORD        password for the local broker, if it wants one. A flag
                       would put it in the process list for anyone to read.
  ECOFLOWD_HTTP_TOKEN  with --listen: the token the web page asks for and every
                       request to /status and /block has to carry as
                       "Authorization: Bearer <token>"; at least 16
                       characters, e.g. from "openssl rand -hex 32".

options:
`)
		fs.PrintDefaults()
		fmt.Fprint(stderr, `
note on --fast:
  Without it the device reports once a minute and nothing is sent to it at all -
  measured at the device, the subscription alone is enough to keep it talking.
  Readings still reach the local broker, just once a minute rather than every
  few seconds.

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

note on --listen:
  Starts an HTTPS server with a web page: the battery's state of charge and
  which way it goes, PV, house and grid, the day's energy, and how the
  connections stand. The page is read-only - it shows the discharge block but
  cannot switch it. It holds no data itself and asks for the token once; the
  data comes from GET /status, which needs the token.

  It listens on the addresses given, comma-separated, never on every
  interface: 0.0.0.0 is refused. For the page that is the machine's LAN
  address - one the router always hands out the same - and next to it, for a
  caller of --block in a container, the Docker bridge, e.g.
  --listen 172.17.0.1,192.168.1.10. The certificate has to name every one. TLS 1.3 only; under systemd, pass certificate
  and key with LoadCredential=, see contrib/ecoflowd@.service.

note on --block:
  The discharge block. In the app, set up exactly one scheduled task of type
  "Laden des Akkus" (charge battery); its window and repetition decide when a
  block may apply at all, e.g. daily 00:00-24:00. With --block this program
  switches that task on and off on request and never changes its times.
  Measured: about 25 s after the task is enabled the battery stops
  discharging, and within a good minute of disabling it, it supplies again.

  The request comes over HTTPS on --listen, never over MQTT - the same
  server as the web page:
    PUT    /block   switch on, or renew; holds for --block-ttl
    DELETE /block   switch off now
    GET    /block   the state, as JSON
  each with "Authorization: Bearer $ECOFLOWD_HTTP_TOKEN". A request not
  renewed within --block-ttl ends by itself and the task is switched off. At
  start-up an enabled task is switched off as well; switching in the app is
  left alone otherwise. Answers: 200, 401 wrong token, 409 not exactly one
  task to switch, 503 no connection or no task list yet, 504 the device did
  not acknowledge.

  Like --fast this publishes to the .../set topic, and for the same reason it
  is a flag: two kinds of message, both captured from the app - the request
  for the task list, and the task itself sent back exactly as the device
  listed it with only its on/off field changed. Nothing is created, deleted or
  moved. The state also goes to <topic>/block on the local broker.

  For the caller alone, bind --listen to the Docker bridge or localhost. On a
  LAN address as well, for the page, /block is reachable there too -
  protected by the token and TLS.

exit status:
  0  stopped on a signal
  1  usage or configuration error
  78 the account credentials were rejected - waiting will not fix this, so the
     systemd unit should not restart on it

examples:
  ecoflowd --sn HC31XXXXXXXXXXXX --stdout
  ecoflowd --sn HC31XXXXXXXXXXXX --stdout --fast
  ecoflowd --sn HC31XXXXXXXXXXXX --broker tcp://127.0.0.1:1883
  ecoflowd --sn HC31XXXXXXXXXXXX --broker tcp://127.0.0.1:1883 \
           --listen 192.168.1.10 --tls-cert tls.crt --tls-key tls.key
  ecoflowd --sn HC31XXXXXXXXXXXX --broker tcp://127.0.0.1:1883 --block \
           --listen 172.17.0.1,192.168.1.10 --tls-cert tls.crt --tls-key tls.key
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

// The --block limits. Five minutes matches the revert time the home
// automation already uses for the inverter's power limit; the floor keeps a
// block from flapping against the device's 25 s reaction, the ceiling keeps
// a forgotten one from holding for hours.
const (
	defaultBlockTTL  = 5 * time.Minute
	minBlockTTL      = time.Minute
	maxBlockTTL      = 15 * time.Minute
	defaultHTTPSPort = "8089"
	minTokenLength   = 16
)

// buildConfig validates the command line and the environment together.
func buildConfig(o *options, fs *flag.FlagSet) (*config, error) {
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
	if err := httpConfig(c, o, fs); err != nil {
		return nil, err
	}

	return c, nil
}

// httpConfig checks the HTTPS flags. --listen starts the server with the web
// page; --block adds its endpoint to it and needs it. Flags that belong to
// either are an error without it: an endpoint that silently does not exist
// sends people debugging the network.
func httpConfig(c *config, o *options, fs *flag.FlagSet) error {
	visited := func(names ...string) []string {
		var got []string
		if fs != nil {
			fs.Visit(func(f *flag.Flag) {
				for _, n := range names {
					if f.Name == n {
						got = append(got, "--"+f.Name)
					}
				}
			})
		}
		return got
	}

	if !o.block {
		if stray := visited("block-ttl", "block-task"); len(stray) > 0 {
			return fmt.Errorf("%s only work together with --block", strings.Join(stray, ", "))
		}
	}
	if strings.TrimSpace(o.listen) == "" {
		if o.block {
			return errors.New("--block needs --listen, e.g. 172.17.0.1 for the Docker bridge or 127.0.0.1")
		}
		if stray := visited("tls-cert", "tls-key"); len(stray) > 0 {
			return fmt.Errorf("%s only work together with --listen", strings.Join(stray, ", "))
		}
		return nil
	}

	for _, s := range strings.Split(o.listen, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		listen, err := listenAddress(s)
		if err != nil {
			return err
		}
		c.listen = append(c.listen, listen)
	}
	if len(c.listen) == 0 {
		return fmt.Errorf("--listen %q names no address", o.listen)
	}

	if o.tlsCert == "" || o.tlsKey == "" {
		return errors.New("--listen needs --tls-cert and --tls-key; the server speaks HTTPS only")
	}
	cert, err := tls.LoadX509KeyPair(o.tlsCert, o.tlsKey)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	c.tlsCertificate = cert

	// From the environment, like every other secret here: a flag would put it
	// in the process list.
	c.httpToken = os.Getenv("ECOFLOWD_HTTP_TOKEN")
	if len(c.httpToken) < minTokenLength {
		return fmt.Errorf("--listen needs ECOFLOWD_HTTP_TOKEN of at least %d characters; see --help",
			minTokenLength)
	}

	if o.block {
		c.block, c.blockTTL, c.blockTask = true, o.blockTTL, o.blockTask
		if c.blockTTL < minBlockTTL || c.blockTTL > maxBlockTTL {
			return fmt.Errorf("--block-ttl %s is outside %s to %s", c.blockTTL, minBlockTTL, maxBlockTTL)
		}
	}
	return nil
}

// listenAddress checks one address of --listen. The unspecified addresses are
// refused: listening on every interface would put the endpoint on every
// network the machine is in, including ones nobody thought of. A concrete
// address - the Docker bridge for a caller in a container, the LAN address for
// the page - is a choice, and several are given as a list.
func listenAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = strings.Trim(s, "[]"), defaultHTTPSPort
	}
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		return "", fmt.Errorf("--listen %q would listen on every interface; give the address to bind", s)
	}
	return net.JoinHostPort(host, port), nil
}
