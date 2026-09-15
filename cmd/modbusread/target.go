package main

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// defaultPort is the registered Modbus TCP port.
const defaultPort = "502"

// windowsCOM matches a Windows serial port name such as COM3.
var windowsCOM = regexp.MustCompile(`^(?i)COM[0-9]+$`)

// target is a parsed connection target.
type target struct {
	url    string // as the modbus library expects it, scheme included
	serial bool   // true for a real serial line, where the link settings apply
}

// parseTarget turns the first positional argument into a client URL.
//
// A device path ("/dev/ttyUSB0", "COM3") means Modbus RTU, a host means
// Modbus TCP, and an explicit scheme wins over both — so the common cases
// need no extra flag and the unusual ones stay reachable.
func parseTarget(s string) (target, error) {
	if s == "" {
		return target{}, fmt.Errorf("empty target")
	}

	scheme, rest, hasScheme := strings.Cut(s, "://")
	if !hasScheme {
		switch {
		case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "./"):
			return target{url: "rtu://" + s, serial: true}, nil
		case windowsCOM.MatchString(s):
			return target{url: "rtu://" + s, serial: true}, nil
		default:
			return target{url: "tcp://" + withPort(s)}, nil
		}
	}

	if rest == "" {
		return target{}, fmt.Errorf("target %q has a scheme but no address", s)
	}

	switch strings.ToLower(scheme) {
	case "rtu":
		return target{url: "rtu://" + rest, serial: true}, nil
	case "tcp", "udp", "rtuovertcp", "rtuoverudp":
		// These carry RTU or MBAP frames over a network socket; the serial
		// link settings do not apply to them.
		return target{url: strings.ToLower(scheme) + "://" + withPort(rest)}, nil
	case "tcp+tls":
		return target{}, fmt.Errorf("tcp+tls is not supported (it needs certificates this tool does not manage)")
	default:
		return target{}, fmt.Errorf("unknown scheme %q (known: tcp, rtu, rtuovertcp, rtuoverudp, udp)", scheme)
	}
}

// withPort appends the default Modbus port when the address carries none.
func withPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, defaultPort)
}
