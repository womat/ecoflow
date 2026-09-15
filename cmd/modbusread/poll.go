package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/simonvetter/modbus"
)

// poll performs the read once, or repeatedly when --interval is set.
//
// In polling mode a single failure never ends the run: the connection is
// reopened and the next interval tried again. The exit code is non-zero only
// if not a single sample succeeded.
func poll(ctx context.Context, c *config, stdout, stderr io.Writer) int {
	mc, err := modbus.NewClient(&modbus.ClientConfiguration{URL: c.url, Timeout: c.timeout})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := mc.SetUnitId(c.unit); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	// Word and byte order are applied by this program, not by the library,
	// so that the raw words stay available for the output.
	if err := mc.SetEncoding(modbus.BIG_ENDIAN, modbus.HIGH_WORD_FIRST); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	connected := false
	defer func() {
		if connected {
			_ = mc.Close()
		}
	}()

	p := newPrinter(stdout, c)
	var prev map[uint16]string
	var ticker *time.Ticker
	if c.interval > 0 {
		ticker = time.NewTicker(c.interval)
		defer ticker.Stop()
	}

	anyOK := false
	for n := 0; ; n++ {
		if !connected {
			if err := mc.Open(); err != nil {
				fmt.Fprintln(stderr, "error: connect:", err)
				if c.interval == 0 {
					return 1
				}
			} else {
				connected = true
			}
		}

		if connected {
			words, errs := readRegisters(mc, c.addr, c.total, c.regType)
			samples := buildSamples(c, words, errs)

			if allFailed(errs) {
				// Most likely the connection died rather than every
				// single register having vanished.
				_ = mc.Close()
				connected = false
			} else {
				anyOK = true
			}

			shown := samples
			if c.onChange && c.interval > 0 {
				shown = changedOnly(samples, &prev, n == 0)
			}
			if len(shown) > 0 {
				if err := p.print(time.Now(), shown); err != nil {
					fmt.Fprintln(stderr, "error:", err)
					return 1
				}
			}
			if c.interval == 0 {
				if !anyOK {
					return 1
				}
				return 0
			}
		}

		if c.samples > 0 && n+1 >= c.samples {
			break
		}
		select {
		case <-ctx.Done():
			return exitCode(anyOK)
		case <-ticker.C:
		}
	}
	return exitCode(anyOK)
}

func exitCode(anyOK bool) int {
	if anyOK {
		return 0
	}
	return 1
}

func allFailed(errs []error) bool {
	for _, err := range errs {
		if err == nil {
			return false
		}
	}
	return len(errs) > 0
}

// changedOnly filters the samples down to those whose raw words (or error)
// differ from the previous pass. Comparing raw words rather than the decoded
// value means a change that rounds away in a float is still reported.
func changedOnly(samples []sample, prev *map[uint16]string, first bool) []sample {
	if *prev == nil {
		*prev = make(map[uint16]string, len(samples))
	}
	out := make([]sample, 0, len(samples))
	for _, s := range samples {
		sig := signature(s)
		if old, seen := (*prev)[s.addr]; !seen || old != sig {
			out = append(out, s)
		}
		(*prev)[s.addr] = sig
	}
	if first {
		// The first pass is the baseline: show everything.
		return samples
	}
	return out
}

func signature(s sample) string {
	var sb strings.Builder
	if s.err != nil {
		sb.WriteString("!" + s.err.Error())
	}
	for _, w := range s.words {
		fmt.Fprintf(&sb, "%04X", w)
	}
	return sb.String()
}
