package frames

import "time"

// Flow names one of the six energy paths the hourly history counts.
type Flow uint64

// The flow numbers, matched to paths by regression: over four minutes each
// counter grew at the rate of its own power, PV at 1013 W against a measured
// 1029 W and so on. The reading is confirmed by the hourly balance, which
// closes to within one watt-hour on every hour of the day.
const (
	FlowPV         Flow = 1
	FlowBatteryIn  Flow = 16
	FlowBatteryOut Flow = 32
	FlowGridIn     Flow = 48
	FlowGridOut    Flow = 64
	FlowHouse      Flow = 80
)

// Flows lists the six in the order they are worth printing.
var Flows = []Flow{FlowPV, FlowHouse, FlowBatteryIn, FlowBatteryOut, FlowGridIn, FlowGridOut}

func (f Flow) String() string {
	switch f {
	case FlowPV:
		return "pv"
	case FlowBatteryIn:
		return "battery_in"
	case FlowBatteryOut:
		return "battery_out"
	case FlowGridIn:
		return "grid_in"
	case FlowGridOut:
		return "grid_out"
	case FlowHouse:
		return "house"
	}
	return "unknown"
}

// HoursPerDay is how many slots each part carries, one per hour.
const HoursPerDay = 24

// HourlyPart is one flow's share of the hourly history.
//
// The history arrives in six of these, all carrying the same timestamp. Each
// holds one value per hour of the device's day in watt-hours, with the current
// hour still filling up and later hours at zero.
type HourlyPart struct {
	Flow  Flow
	Hours []uint64  // watt-hours, index = hour of day, UTC
	When  time.Time // device clock, UTC
}

// Total is the day's sum for this flow so far.
func (p HourlyPart) Total() uint64 {
	var sum uint64
	for _, v := range p.Hours {
		sum += v
	}
	return sum
}

// Hourly decodes one part of the hourly history.
func (f Frame) Hourly() (HourlyPart, bool) {
	if f.Command != Hourly {
		return HourlyPart{}, false
	}

	outer, ok := find(parse(f.Payload), 2)
	if !ok || outer.wire != wireBytes {
		return HourlyPart{}, false
	}
	body := parse(outer.bytes)

	when, okWhen := find(body, 1)
	flow, okFlow := find(body, 2)
	blob, okBlob := find(body, 3)
	if !okWhen || !okFlow || !okBlob || blob.wire != wireBytes {
		return HourlyPart{}, false
	}

	hours, err := varints(blob.bytes)
	if err != nil {
		return HourlyPart{}, false
	}

	return HourlyPart{
		Flow:  Flow(flow.varint),
		Hours: hours,
		When:  time.Unix(int64(when.varint), 0).UTC(),
	}, true
}

// HourlyBalance is what an hour's flows leave over: everything coming in minus
// everything going out. It is zero to within a watt-hour on a correctly read
// history, and is the check that established the flow numbering.
func HourlyBalance(parts map[Flow]HourlyPart, hour int) int64 {
	at := func(f Flow) int64 {
		p, ok := parts[f]
		if !ok || hour < 0 || hour >= len(p.Hours) {
			return 0
		}
		return int64(p.Hours[hour])
	}
	return at(FlowPV) + at(FlowBatteryOut) + at(FlowGridIn) -
		at(FlowHouse) - at(FlowBatteryIn) - at(FlowGridOut)
}
