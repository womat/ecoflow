#!/usr/bin/env python3
"""Decode the frames "ecoflow-api.sh live" prints, and show the power values.

Reads the output of "ecoflow-api.sh live" on stdin and writes one line per
energy stream report. Everything it needs is in the standard library: the
protobuf wire format is parsed directly, no .proto file and no code generation.

What it knows, and how that was established (September 2026, PowerOcean DC Fit):

  - Each frame wraps a header whose field 8 is cmd_func and field 9 is cmd_id.
    The payload sits in field 1.
  - The payload is obfuscated: every byte is XORed with the low byte of the
    sequence number (header field 14). Two frames one sequence apart differ by
    exactly one bit pattern, which is what gave this away.
  - Two reports carry the power values, with the same fields in both:
    cmd_id 34 arrives once a minute on its own and carries a timestamp rounded
    to the minute; cmd_id 33 arrives every two to three seconds, timestamped to
    the second, but only while something keeps the fast stream switched on (see
    "fast" in ecoflow-api.sh). They are packed differently - 34 wraps the values
    in another message, 33 lists them directly - so both shapes are handled.
  - The field order below is confirmed by the energy balance closing to two
    decimals on every frame: PV = battery + house + grid.

The mapping is for the DC Fit. Other PowerOcean models number these fields
differently - see api-status.md. Read-only: this only ever reads stdin.
"""

import struct
import sys
import time

# The hourly energy history, cmd_func 254 / cmd_id 32. It arrives about twice a
# second in six parts that share a timestamp, one part per flow. Each part
# carries 24 packed varints: one per hour of the current day, in Wh, with the
# current hour still filling up.
#
# Established by measurement, not from any documentation. The part numbers were
# matched to flows by regression - over four minutes each counter grew at the
# rate of its power, PV at 1013 W against a measured 1029 W and so on - and the
# reading is confirmed by the hourly energy balance, which closes to within one
# watt-hour on every hour of the day:
#
#   PV + battery out + grid in  =  house + battery in + grid out
#
# Zeros land where they should: no PV before dawn, the battery discharging
# overnight and charging once the sun carries the house.
# The module list, cmd_func 96 / cmd_id 3: the serial numbers of the parts the
# system is made of, as ASCII, one protobuf field each. Byte for byte identical
# across a whole capture, so it is an inventory rather than a measurement.
#
# Like the bare acknowledgement on cmd_id 137, it shows up only while the fast
# stream is being switched on - neither appeared at all in a capture taken
# without it. The switch asks for an acknowledgement (needAck = 1), and the
# device answers with both.
#
# The field numbers were matched against the portal's own "Component
# information" table, which lists the same serials with their types, so these
# names are read off EcoFlow's interface rather than guessed from prefixes.
MODULES = (96, 3)
MODULE_KINDS = {
    1: 'system',
    2: 'converter',
    3: 'battery',
}

HOURLY = (254, 32)
HOURLY_FLOWS = {
    1: 'PV',
    16: 'battery in',
    32: 'battery out',
    48: 'grid in',
    64: 'grid out',
    80: 'house',
}

# The two reports that carry power values, by cmd_id within cmd_func 96.
MINUTELY, FAST = 34, 33
ENERGY_STREAM = {(96, MINUTELY), (96, FAST)}

# Field numbers inside the energy stream report, as measured on a DC Fit.
GRID, DCDC, BATTERY, PV, TIMESTAMP, TIMEZONE, SOC, HOUSE = 1, 2, 3, 4, 5, 6, 7, 8
POWER_FIELDS = (GRID, DCDC, BATTERY, PV, HOUSE)


def varint(b, i):
    result = shift = 0
    while True:
        byte = b[i]
        i += 1
        result |= (byte & 0x7F) << shift
        if not byte & 0x80:
            return result, i
        shift += 7


def fields(b):
    """Yield (number, wire type, value) for one protobuf message.

    A frame that ends mid-field simply stops here. Slicing past the end of a
    bytes object does not raise, so the length has to be checked: a short slice
    handed on as a value turns up later as a struct error or a bytes object
    where a number was expected, and this runs over a live stream where one
    damaged frame must not end the reading.
    """
    i = 0
    while i < len(b):
        try:
            key, i = varint(b, i)
            number, wire = key >> 3, key & 7
            if wire == 0:
                value, i = varint(b, i)
            elif wire in (1, 2, 5):
                if wire == 2:
                    length, i = varint(b, i)
                else:
                    length = 4 if wire == 5 else 8
                if i + length > len(b):
                    return
                value, i = b[i:i + length], i + length
            else:
                return
            yield number, wire, value
        except IndexError:
            return


def energy_stream(frame):
    """Return (values, cmd_id) of a power report, or None for anything else."""
    for number, wire, value in fields(frame):
        if number != 1 or wire != 2:
            continue
        header = {n: v for n, w, v in fields(value) if w == 0}
        if (header.get(8), header.get(9)) not in ENERGY_STREAM:
            return None
        cmd_id = header.get(9)
        payload = next((v for n, w, v in fields(value) if n == 1 and w == 2), None)
        if payload is None:
            return None

        key = header.get(14, 0) & 0xFF
        plain = bytes(c ^ key for c in payload)

        # The minutely report wraps the values in a further message, the fast
        # one lists them directly. The wire type of the first field tells them
        # apart: a nested message is length-delimited, a power value is a
        # 32-bit float.
        body = plain
        for number, wire, value in fields(plain):
            if number == 1 and wire == 2:
                body = value
            break

        # A power value is a fixed32 and nothing else. Taking whatever arrives
        # under that field number would put bytes where a float belongs, which
        # only shows up when the line is rendered - and a frame that renders as
        # zeros reads like a real measurement at night.
        out = {}
        for number, wire, value in fields(body):
            if number in POWER_FIELDS:
                if wire != 5:
                    return None
                out[number] = struct.unpack('<f', value)[0]
            else:
                out[number] = value
        return (out, cmd_id) if PV in out else None
    return None


def render(values):
    grid = values.get(GRID, 0.0)
    battery = values.get(BATTERY, 0.0)
    house = abs(values.get(HOUSE, 0.0))
    pv = values.get(PV, 0.0)

    when = values.get(TIMESTAMP)
    measured = time.strftime('%H:%M:%S', time.gmtime(when)) + 'Z' if when else '--:--:--'

    # The directions are spelled out rather than passed through as a sign, the
    # same choice "status" makes: the portal's own display shows magnitudes.
    grid_dir = 'export' if grid > 0 else 'import' if grid < 0 else 'idle'
    battery_dir = 'charging' if battery > 0 else 'discharging' if battery < 0 else 'idle'

    return (f'{measured}  PV {pv:7.0f} W | house {house:6.0f} W | '
            f'battery {abs(battery):6.0f} W ({battery_dir}) | '
            f'grid {abs(grid):6.0f} W ({grid_dir}) | SoC {values.get(SOC, "?")} %')


def packed_varints(b):
    """Read a run of varints laid end to end, as the hourly parts store them.

    Returns None if the run ends in the middle of one. That means the blob is
    damaged, and a partial list of hours would be read as real numbers.
    """
    out, i = [], 0
    while i < len(b):
        value = shift = 0
        while True:
            if i >= len(b):
                return None
            byte = b[i]
            i += 1
            value |= (byte & 0x7F) << shift
            if not byte & 0x80:
                break
            shift += 7
        out.append(value)
    return out


def hourly_part(frame):
    """Return (timestamp, flow, hourly Wh) of one part, or None."""
    for number, wire, value in fields(frame):
        if number != 1 or wire != 2:
            continue
        header = {n: v for n, w, v in fields(value) if w == 0}
        if (header.get(8), header.get(9)) != HOURLY:
            return None
        payload = next((v for n, w, v in fields(value) if n == 1 and w == 2), None)
        if payload is None:
            return None

        key = header.get(14, 0) & 0xFF
        plain = bytes(c ^ key for c in payload)

        body = next((v for n, w, v in fields(plain) if n == 2 and w == 2), None)
        if body is None:
            return None
        part = {n: v for n, w, v in fields(body)}
        when, flow, blob = part.get(1), part.get(2), part.get(3)
        if when is None or flow not in HOURLY_FLOWS or not isinstance(blob, bytes):
            return None
        hours = packed_varints(blob)
        return None if hours is None else (when, flow, hours)
    return None


def modules(frame):
    """Return [(field number, serial)] of a module list frame, or None."""
    for number, wire, value in fields(frame):
        if number != 1 or wire != 2:
            continue
        header = {n: v for n, w, v in fields(value) if w == 0}
        if (header.get(8), header.get(9)) != MODULES:
            return None
        payload = next((v for n, w, v in fields(value) if n == 1 and w == 2), None)
        if payload is None:
            return None

        key = header.get(14, 0) & 0xFF
        plain = bytes(c ^ key for c in payload)

        found = []
        for slot, wire, entry in fields(plain):
            if wire != 2:
                continue
            serial = next((v for n, w, v in fields(entry) if n == 1 and w == 2), None)
            if serial is None:
                continue
            try:
                found.append((slot, serial.decode('ascii')))
            except UnicodeDecodeError:
                return None
        return found or None
    return None


def render_modules(found):
    """The module list, with the types the portal gives for the same serials."""
    lines = ['modules reported by the system', '']
    for slot, serial in found:
        lines.append(f'  {MODULE_KINDS.get(slot, f"field {slot}"):<10} {serial}')
    return '\n'.join(lines)


def render_hours(when, parts):
    """The hourly table, one row per flow, plus the balance as a check."""
    hour = time.gmtime(when).tm_hour
    head = time.strftime('%Y-%m-%d %H:%M:%SZ', time.gmtime(when))

    lines = [f'hourly energy in Wh, device day up to {head}', '']
    lines.append('flow        ' + ''.join(f'{h:>6}' for h in range(hour + 1)) + '   total')
    for flow, name in HOURLY_FLOWS.items():
        row = parts.get(flow, [])
        cells = ''.join(f'{row[h]:>6}' if h < len(row) else '     .' for h in range(hour + 1))
        lines.append(f'{name:<12}{cells}{sum(row):>8}')

    # In every hour the energy coming in equals the energy going out. Printing
    # the difference makes a misread field obvious instead of plausible.
    def at(flow, h):
        row = parts.get(flow, [])
        return row[h] if h < len(row) else 0

    diff = ''.join(
        f'{at(1, h) + at(32, h) + at(48, h) - at(80, h) - at(16, h) - at(64, h):>6}'
        for h in range(hour + 1))
    lines.append(f'{"balance":<12}{diff}')
    lines.append('')
    lines.append('the current hour is still filling up; balance should be near zero')
    return '\n'.join(lines)


# How long a fast report keeps the minutely one redundant. The fast stream
# arrives every two to three seconds, so anything beyond a minute means it has
# lapsed and the minutely report is the only source left.
FAST_STILL_RUNNING = 90


def main_modules():
    """Print the module list on the first one seen, then stop."""
    for line in sys.stdin:
        parts = line.split()
        if len(parts) < 4:
            continue
        try:
            frame = bytes.fromhex(parts[3])
        except ValueError:
            continue

        found = modules(frame)
        if found:
            print(render_modules(found))
            return 0

    print('no module list seen - is the fast stream running?', file=sys.stderr)
    return 1


def main_hours():
    """Print the hourly table as soon as one complete set has arrived, then stop.

    The six parts repeat about twice a second, so waiting for more would only
    reprint the same table. Exiting closes the pipe, which stops the producer.
    """
    parts = {}
    current = None

    for line in sys.stdin:
        fields_ = line.split()
        if len(fields_) < 4:
            continue
        try:
            frame = bytes.fromhex(fields_[3])
        except ValueError:
            continue

        part = hourly_part(frame)
        if not part:
            continue
        when, flow, hours = part

        if when != current:
            current, parts = when, {}
        parts[flow] = hours

        if len(parts) == len(HOURLY_FLOWS):
            print(render_hours(when, parts))
            return 0

    print('no hourly report seen - is the fast stream running?', file=sys.stderr)
    return 1


def main():
    previous = None
    last_fast = None

    for line in sys.stdin:
        parts = line.split()
        # "<time> <topic> <length> <hex>" - anything else is a status line.
        if len(parts) < 4:
            continue
        try:
            frame = bytes.fromhex(parts[3])
        except ValueError:
            continue

        report = energy_stream(frame)
        if not report:
            continue
        values, cmd_id = report
        when = values.get(TIMESTAMP)

        if cmd_id == FAST:
            last_fast = when
        elif last_fast is not None and when is not None \
                and when - last_fast <= FAST_STILL_RUNNING:
            # Every minutely report shares its timestamp with a fast one, so
            # while the fast stream runs it is a second copy of a reading
            # already printed - only rounded to the minute, which makes it look
            # like the clock stopped.
            continue

        line = render(values)
        if line == previous:
            # The device sends some frames twice. Two identical readings are
            # one reading, and printing both suggests a change that did not
            # happen.
            continue
        previous = line
        print(line, flush=True)


if __name__ == '__main__':
    try:
        if '--hours' in sys.argv[1:]:
            sys.exit(main_hours())
        if '--modules' in sys.argv[1:]:
            sys.exit(main_modules())
        main()
    except (BrokenPipeError, KeyboardInterrupt):
        pass
