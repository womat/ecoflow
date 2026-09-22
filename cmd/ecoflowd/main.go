// Command ecoflowd reads an EcoFlow PowerOcean over the consumer app's cloud
// channel and keeps reading it.
//
// It is the long-running counterpart to modbusread, which is a probe. Where
// that one knows nothing about any device on purpose, this one is specific
// down to the protobuf field numbers — that knowledge lives in
// internal/frames and internal/ecoflow so modbusread stays universal.
//
// Nothing here is documented or promised by EcoFlow. The login endpoint, the
// credentials, the client id format, the frame layout: all of it was measured
// and can stop working without notice. A service invites the assumption that
// it will keep running, so failures are reported loudly rather than swallowed.
//
// In its normal mode it sends nothing to the device. That is not caution, it
// is what the device turned out to need: subscribing alone keeps it reporting
// once a minute, measured over 23 minutes without a single message to the
// cloud. Readings still go to the local broker - that is what the program is
// for; only --fast writes anything back to the device.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Exit codes. 78 is the conventional EX_CONFIG and matters operationally: the
// systemd unit lists it in RestartPreventExitStatus, so a rejected password
// stops the service instead of retrying a typo against the cloud forever.
const (
	exitOK          = 0
	exitUsage       = 1
	exitRuntime     = 2
	exitCredentials = 78
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr io.Writer) int {
	opts, fs, err := parseArgs(argv, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	if opts == nil {
		return exitOK // -h
	}
	if opts.version {
		fmt.Fprintln(stdout, versionString())
		return exitOK
	}

	cfg, err := buildConfig(opts, fs)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serve(ctx, cfg, stdout, stderr)
}
