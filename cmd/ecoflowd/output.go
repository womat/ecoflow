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

// fastStillRunning is how long a fast report keeps the minutely one redundant.
//
// The fast stream arrives every two to three seconds, so anything beyond a
// minute means it has lapsed and the minutely report is the only source left.
const fastStillRunning = 90

// readings turns frames into measurements, dropping what would only repeat.
//
// Two things get suppressed, both measured at the device. Some frames are sent
// twice outright, and two identical readings are one reading. And every
// minutely report shares its timestamp with a fast one, so while the fast
// stream runs it repeats a reading already shown - with a timestamp rounded to
// the minute, which makes it look like the clock stopped.
//
// Two different readings within the same second are kept: they happen, and
// they are not repetition.
type readings struct {
	previous string
	lastFast int64
	haveFast bool
}

// add returns the line to report, or false if this frame adds nothing.
func (r *readings) add(f frames.Frame) (string, frames.Energy, bool) {
	e, ok := f.Energy()
	if !ok {
		return "", e, false
	}

	when := e.Measured.Unix()
	switch {
	case f.Command == frames.Fast:
		r.lastFast, r.haveFast = when, true
	case r.haveFast && when-r.lastFast <= fastStillRunning:
		return "", e, false
	}

	line := reading(e)
	if line == r.previous {
		return "", e, false
	}
	r.previous = line
	return line, e, true
}
