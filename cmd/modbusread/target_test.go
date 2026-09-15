package main

import (
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in         string
		wantURL    string
		wantSerial bool
	}{
		{"192.168.1.50", "tcp://192.168.1.50:502", false},
		{"192.168.1.50:1502", "tcp://192.168.1.50:1502", false},
		{"plc.local", "tcp://plc.local:502", false},
		{"[fe80::1]:502", "tcp://[fe80::1]:502", false},

		{"/dev/ttyUSB0", "rtu:///dev/ttyUSB0", true},
		{"/dev/tty.usbserial-A50285BI", "rtu:///dev/tty.usbserial-A50285BI", true},
		{"COM3", "rtu://COM3", true},
		{"com12", "rtu://com12", true},

		// An explicit scheme wins over the guess.
		{"rtu:///dev/ttyS1", "rtu:///dev/ttyS1", true},
		{"tcp://192.168.1.50", "tcp://192.168.1.50:502", false},
		{"rtuovertcp://gw:4001", "rtuovertcp://gw:4001", false},
		{"udp://192.168.1.50", "udp://192.168.1.50:502", false},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseTarget(tc.in)
			if err != nil {
				t.Fatalf("parseTarget(%q): %v", tc.in, err)
			}
			if got.url != tc.wantURL {
				t.Errorf("url = %q, want %q", got.url, tc.wantURL)
			}
			if got.serial != tc.wantSerial {
				t.Errorf("serial = %v, want %v", got.serial, tc.wantSerial)
			}
		})
	}

	for _, in := range []string{"", "http://plc", "tcp://", "tcp+tls://plc:802"} {
		if got, err := parseTarget(in); err == nil {
			t.Errorf("parseTarget(%q) = %+v, want an error", in, got)
		}
	}
}

// Serial settings that quietly do nothing would send someone hunting for a
// wiring fault, so they have to be refused on a TCP target — including when
// they are typed after the positional arguments.
func TestSerialFlagsRejectedOnTCPTarget(t *testing.T) {
	for _, args := range [][]string{
		{"192.168.1.50", "42082", "uint16", "--baud", "9600"},
		{"--parity", "even", "192.168.1.50", "42082", "uint16"},
		{"192.168.1.50", "42082", "uint16", "--databits", "7"},
		{"192.168.1.50", "42082", "uint16", "--stopbits", "2"},
	} {
		var out, errOut strings.Builder
		if code := run(args, &out, &errOut); code == 0 {
			t.Errorf("%v: expected a non-zero exit code", args)
		} else if !strings.Contains(errOut.String(), "only applies to a serial") {
			t.Errorf("%v: unexpected error: %s", args, errOut.String())
		}
	}
}

func TestSerialFlagsAcceptedOnRTUTarget(t *testing.T) {
	o := &options{baud: 9600, dataBits: 8, parity: "even", stopBits: 1}
	set := map[string]bool{"baud": true, "parity": true}

	got, err := buildSerial(o, set, target{url: "rtu:///dev/ttyUSB0", serial: true})
	if err != nil {
		t.Fatalf("buildSerial: %v", err)
	}
	if got.speed != 9600 || got.dataBits != 8 || got.stopBits != 1 {
		t.Fatalf("unexpected serial config: %+v", got)
	}
	if got.parity == 0 {
		t.Fatal("parity even should not map to PARITY_NONE")
	}
}

func TestSerialSettingsValidated(t *testing.T) {
	tgt := target{url: "rtu:///dev/ttyUSB0", serial: true}
	for _, o := range []*options{
		{baud: 9600, dataBits: 8, parity: "mark"},
		{baud: 9600, dataBits: 9, parity: "none"},
		{baud: 9600, dataBits: 8, parity: "none", stopBits: 3},
		{baud: 0, dataBits: 8, parity: "none"},
	} {
		if _, err := buildSerial(o, nil, tgt); err == nil {
			t.Errorf("%+v: expected an error", o)
		}
	}
}

// A serial device cannot be opened in CI, but the failure has to be a clean
// message rather than a crash or a silent hang.
func TestMissingSerialDeviceFails(t *testing.T) {
	var out, errOut strings.Builder
	code := run([]string{"/dev/definitely-not-a-serial-port", "42082", "uint16", "--timeout", "300ms"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if !strings.Contains(errOut.String(), "error:") {
		t.Fatalf("expected an error message, got: %q", errOut.String())
	}
}
