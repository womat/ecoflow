package main

import (
	"fmt"

	"github.com/womat/ecoflow/internal/frames"
)

// reading renders one measurement.
//
// The format matches scripts/ecoflow-frames.py character for character. That
// is not taste: the shell and python pair ran against the device for days, so
// being able to run both and diff the output is how this program is checked
// against something that is known to work.
func reading(e frames.Energy) string {
	return fmt.Sprintf(
		"%s  PV %7.0f W | house %6.0f W | battery %6.0f W (%s) | grid %6.0f W (%s) | SoC %d %%",
		e.Measured.Format("15:04:05Z"),
		e.PV, abs(e.House),
		abs(e.Battery), direction(e.Battery, "charging", "discharging"),
		abs(e.Grid), direction(e.Grid, "export", "import"),
		e.SoC)
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func direction(v float32, positive, negative string) string {
	switch {
	case v > 0:
		return positive
	case v < 0:
		return negative
	}
	return "idle"
}

// fastMemory is how long a fast report's timestamp is remembered, in seconds.
//
// A minutely report is redundant only if a fast one with exactly its timestamp
// was already seen. The matching fast report arrives a few seconds before the
// minutely one, so five minutes is ample.
const fastMemory = 300

// readings turns frames into measurements, dropping what would only repeat.
//
// Two things get suppressed, both measured at the device. Some frames are sent
// twice outright, and two identical readings are one reading. And every
// minutely report shares its timestamp with a fast one, so while the fast
// stream runs it repeats a reading already shown - with a timestamp rounded to
// the minute, which makes it look like the clock stopped.
//
// The second rule matches the timestamp rather than asking whether a fast
// report came recently. That difference shows when the stream ends: the first
// minutely report after it has no fast twin, and a time window would drop it
// and leave a minute without a reading - seen on the broker on 26.09.2026,
// right after the phone app was closed.
//
// Two different readings within the same second are kept: they happen, and
// they are not repetition.
type readings struct {
	previous string
	fastSeen map[int64]struct{}
}

// add returns the line to report, or false if this frame adds nothing.
func (r *readings) add(f frames.Frame) (string, frames.Energy, bool) {
	e, ok := f.Energy()
	if !ok {
		return "", e, false
	}

	if !e.Measured.IsZero() {
		when := e.Measured.Unix()
		if f.Command == frames.Fast {
			if r.fastSeen == nil {
				r.fastSeen = map[int64]struct{}{}
			}
			r.fastSeen[when] = struct{}{}
			for t := range r.fastSeen {
				if when-t > fastMemory {
					delete(r.fastSeen, t)
				}
			}
		} else if _, twin := r.fastSeen[when]; twin {
			return "", e, false
		}
	}

	line := reading(e)
	if line == r.previous {
		return "", e, false
	}
	r.previous = line
	return line, e, true
}
