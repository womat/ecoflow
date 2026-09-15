package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/womat/ecoflow/internal/decode"
)

func newFlagSet(o *options, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("modbusread", flag.ContinueOnError)
	fs.SetOutput(stderr)

	fs.IntVar(&o.count, "count", 1, "number of values to read (with type string: number of registers)")
	fs.UintVar(&o.unit, "unit", 1, "Modbus unit/slave id")
	fs.StringVar(&o.fc, "fc", "holding", "register space: holding (FC3) or input (FC4)")
	fs.StringVar(&o.wordOrder, "word-order", "high", "word order of multi-register values: high or low")
	fs.StringVar(&o.byteOrder, "byte-order", "big", "byte order within a register: big or little")
	fs.DurationVar(&o.timeout, "timeout", 3*time.Second, "connect and request timeout")
	fs.StringVar(&o.out, "out", "dec", "display addresses and integers as dec or hex")
	fs.BoolVar(&o.json, "json", false, "machine readable output (JSON lines when polling)")
	fs.DurationVar(&o.interval, "interval", 0, "repeat the read at this interval until interrupted")
	fs.IntVar(&o.samples, "samples", 0, "stop after this many samples (0 = unlimited)")
	fs.BoolVar(&o.onChange, "on-change", false, "with --interval: print only when a value changed")
	fs.BoolVar(&o.version, "version", false, "print the version and exit")

	fs.UintVar(&o.baud, "baud", 19200, "serial line speed (RTU only)")
	fs.UintVar(&o.dataBits, "databits", 8, "bits per character, 7 or 8 (RTU only)")
	fs.StringVar(&o.parity, "parity", "none", "parity: none, even or odd (RTU only)")
	fs.UintVar(&o.stopBits, "stopbits", 0, "stop bits, 1 or 2; 0 follows the Modbus rule of 2 without parity, 1 with (RTU only)")

	fs.Usage = func() {
		names := make([]string, len(decode.Types))
		for i, t := range decode.Types {
			names[i] = string(t)
		}
		fmt.Fprintf(stderr, `modbusread — read registers from a Modbus device, TCP or serial (read-only).

Usage:
  modbusread [flags] <host[:port]> <address> <type>

  host      a network address (the port defaults to 502) or a serial device
            such as /dev/ttyUSB0 or COM3; an explicit tcp:// or rtu:// URL
            also works
  address   register address, decimal (42082) or hexadecimal (0xA462)
  type      %s

Addresses are used exactly as given, on the wire and zero-based. Many register
maps found online are documented one-based — subtract 1 from those.

Flags:
`, strings.Join(names, ", "))
		fs.PrintDefaults()
		fmt.Fprint(stderr, `
Examples:
  modbusread 192.168.1.50 42082 uint16
  modbusread 192.168.1.50 40574 float32 --word-order low
  modbusread 192.168.1.50 40520 raw --count 120 --out hex
  modbusread 192.168.1.50 40520 raw --count 120 --interval 1s --on-change
  modbusread /dev/ttyUSB0 40069 uint16 --baud 9600 --parity even --unit 3
`)
	}
	return fs
}
