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

# (cmd_func, cmd_id) of the two reports that carry power values.
ENERGY_STREAM = {(96, 34), (96, 33)}

# Field numbers inside the energy stream report, as measured on a DC Fit.
GRID, DCDC, BATTERY, PV, TIMESTAMP, TIMEZONE, SOC, HOUSE = 1, 2, 3, 4, 5, 6, 7, 8


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
    """Yield (number, wire type, value) for one protobuf message."""
    i = 0
    while i < len(b):
        try:
            key, i = varint(b, i)
            number, wire = key >> 3, key & 7
            if wire == 0:
                value, i = varint(b, i)
            elif wire == 2:
                length, i = varint(b, i)
                value, i = b[i:i + length], i + length
            elif wire == 5:
                value, i = b[i:i + 4], i + 4
            elif wire == 1:
                value, i = b[i:i + 8], i + 8
            else:
                return
            yield number, wire, value
        except IndexError:
            return


def energy_stream(frame):
    """Return the decoded power values of a frame, or None for anything else."""
    for number, wire, value in fields(frame):
        if number != 1 or wire != 2:
            continue
        header = {n: v for n, w, v in fields(value) if w == 0}
        if (header.get(8), header.get(9)) not in ENERGY_STREAM:
            return None
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

        out = {}
        for number, wire, value in fields(body):
            out[number] = struct.unpack('<f', value)[0] if wire == 5 else value
        return out if PV in out else None
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


def main():
    for line in sys.stdin:
        parts = line.split()
        # "<time> <topic> <length> <hex>" - anything else is a status line.
        if len(parts) < 4:
            continue
        try:
            frame = bytes.fromhex(parts[3])
        except ValueError:
            continue

        values = energy_stream(frame)
        if values:
            print(render(values), flush=True)


if __name__ == '__main__':
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt):
        pass
