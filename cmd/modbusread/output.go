package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// jsonSample is the --json representation of a sample. Addresses and values
// stay decimal and numeric here regardless of --out, so that the output can be
// fed straight into tools that want to compute with it; the raw words carry
// the bit level.
type jsonSample struct {
	Time    string   `json:"time,omitempty"`
	Address uint16   `json:"address"`
	Raw     []string `json:"raw"`
	Value   any      `json:"value"`
	Error   string   `json:"error,omitempty"`
}

type printer struct {
	w         io.Writer
	hex       bool
	asJSON    bool
	timestamp bool // polling: prefix every line with the time
	enc       *json.Encoder
	headed    bool
}

func newPrinter(w io.Writer, c *config) *printer {
	p := &printer{w: w, hex: c.hex, asJSON: c.json, timestamp: c.interval > 0}
	if c.json {
		p.enc = json.NewEncoder(w)
	}
	return p
}

func (p *printer) print(now time.Time, samples []sample) error {
	if p.asJSON {
		for _, s := range samples {
			js := jsonSample{Address: s.addr, Raw: rawWords(s.words), Value: s.value}
			if p.timestamp {
				js.Time = now.Format(time.RFC3339Nano)
			}
			if s.err != nil {
				js.Value, js.Error = nil, s.err.Error()
			}
			if err := p.enc.Encode(js); err != nil {
				return err
			}
		}
		return nil
	}

	if !p.headed {
		p.headed = true
		if p.timestamp {
			fmt.Fprintf(p.w, "%-12s ", "time")
		}
		fmt.Fprintf(p.w, "%-8s %-20s %s\n", "addr", "raw", "value")
	}

	for _, s := range samples {
		if p.timestamp {
			fmt.Fprintf(p.w, "%-12s ", now.Format("15:04:05.000"))
		}
		value := formatValue(s.value, p.hex)
		if s.err != nil {
			value = "! " + s.err.Error()
		}
		fmt.Fprintf(p.w, "%-8s %-20s %s\n", formatAddr(s.addr, p.hex), strings.Join(rawWords(s.words), " "), value)
	}
	return nil
}

func rawWords(words []uint16) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = fmt.Sprintf("0x%04X", w)
	}
	return out
}

func formatAddr(a uint16, hex bool) string {
	if hex {
		return fmt.Sprintf("0x%04X", a)
	}
	return strconv.FormatUint(uint64(a), 10)
}

// formatValue renders a decoded value. With --out hex only integers switch to
// hexadecimal: a hex float is unreadable, and its bit pattern is already in
// the raw column. Signed values show their two's complement, which is the more
// useful form when probing.
func formatValue(v any, hex bool) string {
	switch x := v.(type) {
	case nil:
		return ""
	case uint16:
		if hex {
			return fmt.Sprintf("0x%04X", x)
		}
		return strconv.FormatUint(uint64(x), 10)
	case int16:
		if hex {
			return fmt.Sprintf("0x%04X", uint16(x))
		}
		return strconv.FormatInt(int64(x), 10)
	case uint32:
		if hex {
			return fmt.Sprintf("0x%08X", x)
		}
		return strconv.FormatUint(uint64(x), 10)
	case int32:
		if hex {
			return fmt.Sprintf("0x%08X", uint32(x))
		}
		return strconv.FormatInt(int64(x), 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}
