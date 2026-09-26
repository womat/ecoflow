package frames

import "time"

// Field numbers inside an energy report, measured at a DC Fit.
//
// Confirmed by arithmetic rather than by trust: in every captured frame
// PV equals battery plus house plus grid to within a thousandth of a watt. A
// wrong assignment would not land on that. Other PowerOcean models number
// these differently — see api-status.md.
const (
	energyGrid      = 1
	energyDCDC      = 2
	energyBattery   = 3
	energyPV        = 4
	energyTimestamp = 5
	energySoC       = 7
	energyHouse     = 8
)

// Energy is one reading of the power flows.
//
// The signs are the device's own and deliberately not normalised: positive
// grid means export and positive battery means charging, which is the
// opposite of what EcoFlow's field documentation suggests and was settled by
// an energy balance. House is reported negative while the house consumes.
type Energy struct {
	PV       float32
	House    float32
	Battery  float32
	Grid     float32
	DCDC     float32
	SoC      uint64
	Measured time.Time // device clock, UTC
}

// Energy decodes a Fast or Minutely report.
//
// The two pack their fields differently — Minutely wraps them in a further
// message, Fast lists them directly — so the wire type of the first field
// decides: a nested message is length-delimited, a power value is a fixed32.
func (f Frame) Energy() (Energy, bool) {
	if f.Command != Fast && f.Command != Minutely {
		return Energy{}, false
	}

	body := parse(f.Payload)
	if first, ok := find(body, 1); ok && first.wire == wireBytes {
		body = parse(first.bytes)
	}

	// seen tracks a PV field that really is a fixed32, not merely a field with
	// that number. float32At hands back a zero for anything else, and a frame
	// rendered as all zeros reads like a genuine measurement at night.
	//
	// The other power fields stay 0 when they are absent, and that is the
	// right reading rather than a gap: the device leaves out a field whose
	// value is zero - protobuf's default - and never sends an explicit 0.0. In
	// the captures grid is missing from about half the reports, and on every
	// one of them the balance closes with grid taken as 0 (TestAbsentMeansZero).
	// That EcoFlow's schema is proto3 without "optional" is inferred from this
	// behaviour, not known.
	//
	// By the same rule a PV of exactly 0 would not be sent either, and this
	// check would then drop the frame. At night it does not: reports keep
	// arriving every minute with "pv":0 after rounding - probably because PV
	// is small rather than zero. See mqtt-output.md, section 3.
	var e Energy
	var seen bool
	for _, fl := range body {
		switch fl.number {
		case energyPV:
			e.PV, seen = fl.float32At(), fl.wire == wireFixed32 && len(fl.fixed) == 4
		case energyHouse:
			e.House = fl.float32At()
		case energyBattery:
			e.Battery = fl.float32At()
		case energyGrid:
			e.Grid = fl.float32At()
		case energyDCDC:
			e.DCDC = fl.float32At()
		case energySoC:
			e.SoC = fl.varint
		case energyTimestamp:
			if fl.varint != 0 {
				e.Measured = time.Unix(int64(fl.varint), 0).UTC()
			}
		}
	}
	return e, seen
}

// Balance is what the four flows leave over: PV minus battery, house and grid.
//
// It is zero on a correctly read frame, which makes it the cheapest check that
// the field numbers still mean what they meant when they were measured. A
// firmware that renumbers them would show up here instead of in a plausible
// wrong reading.
func (e Energy) Balance() float32 {
	house := e.House
	if house < 0 {
		house = -house
	}
	return e.PV - e.Battery - house - e.Grid
}
