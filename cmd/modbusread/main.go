// Command modbusread reads registers from a Modbus device, over TCP or over a
// serial line (RTU).
//
// It is a probe, not a monitor: address, register and type in, value out. It
// knows nothing about any particular device and never writes — there is no
// code path in this program that issues a Modbus write.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/simonvetter/modbus"

	"github.com/womat/ecoflow/internal/decode"
)

// maxRegsPerRequest is the Modbus limit for a single read of holding or
// input registers.
const maxRegsPerRequest = 125

type options struct {
	count     int
	unit      uint
	fc        string
	wordOrder string
	byteOrder string
	timeout   time.Duration
	out       string
	json      bool
	interval  time.Duration
	samples   int
	onChange  bool
	version   bool

	// serial link settings, only meaningful for an RTU target
	baud     uint
	dataBits uint
	parity   string
	stopBits uint
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr io.Writer) int {
	opts, pos, fs, err := parseArgs(argv, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if opts == nil {
		return 0 // -h
	}
	if opts.version {
		fmt.Fprintln(stdout, versionString())
		return 0
	}
	if len(pos) != 3 {
		fmt.Fprintln(stderr, "error: expected <target> <address> <type>; see --help")
		return 1
	}

	cfg, err := buildConfig(opts, pos, setFlags(fs))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return poll(ctx, cfg, stdout, stderr)
}

// serialConfig holds the RTU link settings. It is zero for a TCP target, in
// which case the library ignores it.
type serialConfig struct {
	speed    uint
	dataBits uint
	parity   uint
	stopBits uint
}

// config is the fully validated form of the command line.
type config struct {
	url       string
	serial    serialConfig
	unit      uint8
	addr      uint16
	total     int // registers to read in one pass
	typ       decode.Type
	regType   modbus.RegType
	wordOrder decode.WordOrder
	byteOrder decode.ByteOrder
	timeout   time.Duration
	hex       bool
	json      bool
	interval  time.Duration
	samples   int
	onChange  bool
}

func parseArgs(argv []string, stderr io.Writer) (*options, []string, *flag.FlagSet, error) {
	opts := &options{}
	fs := newFlagSet(opts, stderr)

	// Allow flags to appear before, between or after the positional
	// arguments — "modbusread host 42082 uint16 --count 2" is how this gets
	// typed in practice.
	var pos []string
	rest := argv
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, nil, nil, nil
			}
			return nil, nil, nil, errFlag
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	return opts, pos, fs, nil
}

var errFlag = errors.New("invalid arguments; see --help")

// setFlags reports which flags the user actually passed.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func buildConfig(o *options, pos []string, set map[string]bool) (*config, error) {
	addr, err := decode.ParseAddr(pos[1])
	if err != nil {
		return nil, err
	}
	typ, err := decode.ParseType(pos[2])
	if err != nil {
		return nil, err
	}
	wo, err := decode.ParseWordOrder(o.wordOrder)
	if err != nil {
		return nil, err
	}
	bo, err := decode.ParseByteOrder(o.byteOrder)
	if err != nil {
		return nil, err
	}

	var regType modbus.RegType
	switch strings.ToLower(o.fc) {
	case "holding":
		regType = modbus.HOLDING_REGISTER
	case "input":
		regType = modbus.INPUT_REGISTER
	default:
		return nil, fmt.Errorf("unknown --fc %q (known: holding, input)", o.fc)
	}

	switch strings.ToLower(o.out) {
	case "dec", "hex":
	default:
		return nil, fmt.Errorf("unknown --out %q (known: dec, hex)", o.out)
	}

	if o.count < 1 {
		return nil, fmt.Errorf("--count must be at least 1")
	}
	if o.unit > 255 {
		return nil, fmt.Errorf("--unit must be 0-255")
	}

	// For a string the count is the number of registers making up the text;
	// for every other type it is the number of values.
	total := o.count
	if typ != decode.Str {
		total = o.count * decode.RegsPerValue(typ)
	}
	if int(addr)+total > 0x10000 {
		return nil, fmt.Errorf("address %d plus %d register(s) exceeds 65535", addr, total)
	}

	tgt, err := parseTarget(pos[0])
	if err != nil {
		return nil, err
	}

	serial, err := buildSerial(o, set, tgt)
	if err != nil {
		return nil, err
	}

	return &config{
		url:       tgt.url,
		serial:    serial,
		unit:      uint8(o.unit),
		addr:      addr,
		total:     total,
		typ:       typ,
		regType:   regType,
		wordOrder: wo,
		byteOrder: bo,
		timeout:   o.timeout,
		hex:       strings.EqualFold(o.out, "hex"),
		json:      o.json,
		interval:  o.interval,
		samples:   o.samples,
		onChange:  o.onChange,
	}, nil
}

// buildSerial validates the serial link settings.
//
// Passing them for a TCP target is refused rather than ignored: a baud rate
// that silently has no effect is exactly the kind of thing that sends someone
// hunting for a wiring fault that does not exist.
func buildSerial(o *options, set map[string]bool, tgt target) (serialConfig, error) {
	serialFlags := []string{"baud", "databits", "parity", "stopbits"}

	if !tgt.serial {
		for _, name := range serialFlags {
			if set[name] {
				return serialConfig{}, fmt.Errorf("--%s only applies to a serial (RTU) target, not to %s", name, tgt.url)
			}
		}
		return serialConfig{}, nil
	}

	var parity uint
	switch strings.ToLower(o.parity) {
	case "none":
		parity = modbus.PARITY_NONE
	case "even":
		parity = modbus.PARITY_EVEN
	case "odd":
		parity = modbus.PARITY_ODD
	default:
		return serialConfig{}, fmt.Errorf("unknown --parity %q (known: none, even, odd)", o.parity)
	}

	switch o.dataBits {
	case 7, 8:
	default:
		return serialConfig{}, fmt.Errorf("--databits must be 7 or 8")
	}

	// Left at zero the library applies the Modbus spec rule: two stop bits
	// without parity, one with.
	switch o.stopBits {
	case 0, 1, 2:
	default:
		return serialConfig{}, fmt.Errorf("--stopbits must be 1 or 2")
	}

	if o.baud == 0 {
		return serialConfig{}, fmt.Errorf("--baud must be greater than zero")
	}

	return serialConfig{speed: o.baud, dataBits: o.dataBits, parity: parity, stopBits: o.stopBits}, nil
}
