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

	var e Energy
	var seen bool
	for _, fl := range body {
		switch fl.number {
		case energyPV:
			e.PV, seen = fl.float32At(), true
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
