# The measuring tools: `ecoflow-api.sh` and `ecoflow-frames.py`

Practically every finding in these notes was made with these two scripts, on the cloud
channel that [`ecoflowd`](../../README.md) now reads continuously. They stay next to the
service: for the next unknown identifier, they are what you reach for. The commands below
are run from the repository root.

Back to the [research overview](README.md).

## `scripts/ecoflow-api.sh`

The counterpart to `modbusread` for the cloud path: a small shell script that builds the
HMAC signature of the EcoFlow Developer/Open API and prints the answer raw.

**With one exception it only reads.** The exception deserves naming rather than hiding:
`fast` publishes to a `.../set` topic and is thus the only command that can change the
device (see below). `values` and `login` use POST — they are still reads, the endpoints
just want it that way; the script does not know the writing PUT counterpart.

Needs `bash`, `curl` and `openssl`. **`jq` is required for most commands** — only
`devices`, `quota`, `get`, `cert`, `portal`, `portal-get` and `selftest` get by without
it, and there it just prettifies the output. The other nine abort without `jq`.

Credentials come from the environment, never from the repo. **There are two kinds, and
most commands need the second:** the API key pair only works for the Developer API
(`devices`, `quota`, `get`, `values`, `cert`, `mqtt`, `request`) — and that is exactly
the one that refuses the DC Fit its readings. Everything that actually delivers data goes
through the portal token (`login`, `portal`, `status`, `portal-get`, `app-cert`,
`app-mqtt`, `live`, `fast`); that needs **no** key pair.

```bash
export ECOFLOW_ACCESS_KEY='…'   # developer-eu.ecoflow.com → Security
export ECOFLOW_SECRET_KEY='…'
# ECOFLOW_HOST sets the host, default https://api-e.ecoflow.com (EU)

scripts/ecoflow-api.sh devices                    # devices on the account
scripts/ecoflow-api.sh quota <SN>                  # all values of a device
scripts/ecoflow-api.sh -v get /iot-open/sign/device/list   # any GET, with debug output
scripts/ecoflow-api.sh values <SN> bpSoc bpPwr     # selected values (POST endpoint)
scripts/ecoflow-api.sh cert                       # MQTT credentials of the account
scripts/ecoflow-api.sh mqtt <SN>                  # subscribe to a topic (Ctrl-C ends)
scripts/ecoflow-api.sh request <SN> bpSoc         # request values over MQTT
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"   # get a token by logging in
scripts/ecoflow-api.sh portal <SN>                # consumer portal instead of Developer API
scripts/ecoflow-api.sh status <SN>                # the same data as a short overview
scripts/ecoflow-api.sh portal-get <path>          # any GET against the portal API
scripts/ecoflow-api.sh app-cert                   # MQTT credentials of the app channel
scripts/ecoflow-api.sh live <SN>                  # subscribe to the app channel, keep it alive
scripts/ecoflow-api.sh fast <SN>                  # the same at the fast rate (writes!)
scripts/ecoflow-api.sh app-mqtt <SN>              # listen to what the app sends the device
scripts/ecoflow-api.sh selftest                   # signature and stream switch frame
```

`values` uses `POST /iot-open/sign/device/quota`, the way EcoFlow's PowerOcean docs
describe for selected quantities (`bpSoc`, `bpPwr`, `mpptPwr`, `sysLoadPwr`,
`sysGridPwr`, `pcsAPhase` …). POST is the *read* endpoint here; the script deliberately
does not know the PUT counterpart that sets values.

`mqtt` fetches the credentials via `/iot-open/sign/certification` and subscribes to
`/open/<certificateAccount>/<SN>/quota` (a second argument changes the suffix, e.g.
`status`, `get_reply` or `#`). Every message gets a timestamp – a silent recording is only
evidence if you know when it was silent. Additionally needs `jq` and `mosquitto_sub`
(`brew install mosquitto` or `apt install mosquitto-clients`).

`portal` takes a different path: it queries `provider-service/user/device/detail` – the
endpoint the consumer portal itself uses. It answers even for devices the Developer API
blocks with 1006, and returns SOC, live power, energy counters and the firmware's raw
blocks (69 EMS fields, DCDC status, energy stream). Authentication is not with the API
keys but with the **portal's session token** in `ECOFLOW_PORTAL_TOKEN`. The token
expires; fetch a new one on HTTP 401. Keep it out of the repo and, where possible, out of
the shell history.

### Fetching live values

`status` and `portal` need **no** API key pair – only the portal token. A one-off query
from a fresh shell, login and query in one command:

```console
$ ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login first.last@example.com)" \
    scripts/ecoflow-api.sh status HC31XXXXXXXXXXXX
Password (not echoed):
logged in as user 1000000000000…
device   : Home (online)
SoC      : 18 %
PV       : 1045 W
grid     : 0 W (idle)
house    : 446 W
battery  : 599 W (charging)

yield    : today 1.90 | month 282.67 | year 4548.79 | total 5236.71 kWh
measured : 2026-09-17T08:39:26Z
```

`measured` is the timestamp from the firmware's energy stream block – the **time of
measurement, not the time of the query**. If it stands still across several calls, the
display is a still image: the endpoint hands out whatever was last pushed to the cloud.
Why it freezes is open (see `api-status.md`); current values come from `live`, not from
`status`.

The assignment in front **without `export`** only applies to this one command: afterwards
the shell does not know the variable, the token is in no other process environment, and
there is nothing to clean up – not even after an error or Ctrl-C. The password prompt
comes from the terminal (`/dev/tty`) and therefore also works inside the command
substitution: `login` writes **only** the token to stdout, everything else to stderr.
The e-mail may be left out; it is then asked for or taken from `ECOFLOW_EMAIL`. Needs
`jq` (`brew install jq`).

For several queries without typing the password each time, export the token once – but
**`unset`** it at the end, and with `;` rather than `&&`, otherwise it stays around
precisely when something fails. `export ECOFLOW_PORTAL_TOKEN=` does not delete it; it
sets it empty and leaves it exported:

```bash
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
scripts/ecoflow-api.sh status HC31XXXXXXXXXXXX
scripts/ecoflow-api.sh portal HC31XXXXXXXXXXXX
unset ECOFLOW_PORTAL_TOKEN
```

A subshell takes the token with it when it exits, which saves the cleanup:

```bash
( export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
  scripts/ecoflow-api.sh status HC31XXXXXXXXXXXX )
```

`status` renders the answer of `portal` as an overview. The portal reports house and
battery power as **negative**, while its own UI shows them as positive. `status` therefore
prints the magnitude and writes the direction next to it, instead of passing on a sign you
would first have to interpret. If you get *"the response carried no data"* instead of the
overview, `ECOFLOW_PRODUCT_TYPE` does not match the device (default `85` = PowerOcean).

Two ways to the token:

- **From the browser:** in the logged-in portal under *Local Storage → `S1_JWT`*.
- **With `login`:** asks for e-mail and password (password without echo, optionally from
  `ECOFLOW_PASSWORD` – better not, an exported variable outlives the shell that set it and
  ends up in process environments).

To weigh it up: `login` uses the consumer app's login endpoint, which sends the password
**base64-encoded, not hashed** – base64 is encoding, not encryption; only the TLS channel
protects it. The browser token is the smaller secret and expires by itself; the password
is the more convenient way. Both are unofficial interfaces.

### Keeping the channel alive: `live`

`live` subscribes to the app's MQTT channel the way the app does; the device then reports
into it once a minute:

```bash
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
export ECOFLOW_USER_ID=1000000000000000000   # "login" prints this ready to export
scripts/ecoflow-api.sh live HC31XXXXXXXXXXXX
```

It fetches the app MQTT channel's credentials via `app-cert`, subscribes to the device's
three topics and sends a request every `ECOFLOW_LIVE_INTERVAL` seconds (default 30). Runs
until Ctrl-C. Needs `jq`, `mosquitto_sub` and `mosquitto_pub`.

**That request is unnecessary, though** — measured at the device: with
`ECOFLOW_LIVE_INTERVAL=0`, i.e. without any publish at all, minute values kept coming
without a gap for 23 minutes. The subscription alone keeps the device talking. The
default of 30 stays for now, because other models may need it; if you want to work
strictly read-only, set `ECOFLOW_LIVE_INTERVAL=0` — then `live` publishes nothing at all:

```bash
ECOFLOW_LIVE_INTERVAL=0 scripts/ecoflow-api.sh live HC31XXXXXXXXXXXX
```

The output of `live` is **raw** – timestamp, topic, length and payload as hex, because the
push is protobuf, not JSON:

```console
2026-09-22T10:18:05+0200 /app/device/property/HC31... 74 0a480a26f6ddf1e70b98...
```

### Current readings: `ecoflow-frames.py`

For reading there is `scripts/ecoflow-frames.py`, which unpacks the frames. It only needs
`python3`, no further packages:

```console
$ scripts/ecoflow-api.sh live HC31XXXXXXXXXXXX | python3 scripts/ecoflow-frames.py
08:18:00Z  PV     969 W | house    352 W | battery    530 W (charging) | grid     87 W (export) | SoC 60 %
08:19:00Z  PV     976 W | house    349 W | battery    540 W (charging) | grid     87 W (export) | SoC 61 %
```

This is the way to current values – **not** `status`. Measured at the device
(22 September 2026): while `live` was running and frames stamped `08:19Z` were arriving,
`status` kept reporting `measured : 07:13:28Z`. So the detour through the cloud is not
refreshed even by a running listener.

The timestamp on the left is the device's (UTC). The device sends some frames twice;
identical consecutive lines are suppressed, but two different values within the same
second are not — those happen. How the frames are built, why the payload is
XOR-obfuscated and what the field assignment rests on is in `api-status.md`.

**Caveat:** the field numbers apply to the **DC Fit**. On the PowerOcean Plus the same
quantities sit on different numbers – there the script produced plausible numbers under
the wrong names. The assignment here is backed by the energy balance: in every frame,
`PV = battery + house + grid` adds up to two decimal places.

Python because it needs nothing beyond the standard library on the Mac and the Pi; the
continuous-operation counterpart in Go is `ecoflowd`, whose output matches this script
character for character.

### Fast rate: `fast`

Every few seconds instead of every minute – that is what `fast` is for, in place of
`live`. It needs the same two variables as `live`:

```bash
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
export ECOFLOW_USER_ID=1000000000000000000     # "login" prints this ready to export

scripts/ecoflow-api.sh fast HC31XXXXXXXXXXXX | python3 scripts/ecoflow-frames.py
```

It does everything `live` does and additionally switches on the fast data stream.

**This is the only command in the script that writes to a `.../set` topic** – that is, to
the path through which the device could also be reconfigured. That is why it is a command
of its own: the write never happens on the side, only when you type `fast`.

What gets sent is **not guessed**. The command was captured by subscribing to the `set`
topic while operating the phone app (`app-mqtt`, see below); the script replays those
bytes unchanged and only changes the sequence number. It carries no parameters. An earlier
attempt to assemble it from third-party sources was wrong in four places — see
`api-status.md`.

```console
09:13:19Z  PV     970 W | house    415 W | battery    482 W (charging) | grid     72 W (export) | SoC 63 %
09:13:20Z  PV     963 W | house    403 W | battery    476 W (charging) | grid     83 W (export) | SoC 63 %
09:13:22Z  PV     967 W | house    403 W | battery    472 W (charging) | grid     92 W (export) | SoC 63 %
```

Measured: 131 values over a little more than four minutes, on average every 1.9 s —
instead of four. The timestamp here is **accurate to the second**; in the minute report it
is rounded to the minute.

While the fast stream is running, the minute report is **not** shown as well: it carries
the same timestamp as a per-second report that already existed, and with its rounded time
it would look like a standstill. When the fast stream dries up, it appears again.

`ECOFLOW_FAST_INTERVAL` sets the repeat rate, default 3 seconds — the app's rhythm.
**Longer is not more economical, it simply does not work:** at 10 seconds the device fell
back to the minute rate. The switch only lasts a few seconds.

It has a cost: each switch starts its own `mosquitto_pub`, i.e. a new connection every
three seconds. Fine for a measurement, not nice for continuous operation.

### Today's hourly values: `--hours`

On the side, the device sends the **energy balance of the current day, hour by hour**. It
is in the most frequent frame of all, but only arrives in fast mode:

```console
$ scripts/ecoflow-api.sh fast HC31XXXXXXXXXXXX | python3 scripts/ecoflow-frames.py --hours
hourly energy in Wh, device day up to 2026-09-22 09:13:18Z

flow             0     1     2     3     4     5     6     7     8     9   total
PV               0     0     0     0    43   183   738  1350   793   194    3301
battery in       0     0     0     0     0     0   288   798   357   102    1545
battery out    261   245   248   297   353   158     0     0     0     0    1562
grid in          1     0     0    11    32    13     1     1     3     0      62
grid out         0     0     0     0     0     2    68    81    24     0     175
house          262   246   249   308   427   352   383   473   415    91    3206
balance          0    -1    -1     0     1     0     0    -1     0     1
```

It waits for a complete report, prints the table and **then exits** — the data stream
stops along with it. The hours are UTC; the current one is still filling up.

The `balance` row is the check and belongs to the output: in every hour,
`PV + battery out + grid in` must equal `house + battery in + grid out`. If it shows
anything other than a rounding difference, the field assignment no longer holds — for
instance because a firmware shifted the numbers. That is exactly how it was established in
the first place; details in `api-status.md`.

### Which components the system reports: `--modules`

```console
$ scripts/ecoflow-api.sh fast HC31XXXXXXXXXXXX | python3 scripts/ecoflow-frames.py --modules
modules reported by the system

  system     HC31XXXXXXXXXXXX
  converter  HC31YYYYYYYYYYYY
  battery    HJ3AXXXXXXXXXXXX
  battery    HJ3AYYYYYYYYYYYY
```

Serial numbers and configuration without app or portal — here a 5 kW converter with two
battery modules. Like `--hours`, it waits for the first report and then exits.

The labels are **not guessed from the prefixes** but checked against the portal's display:
`user-portal.ecoflow.com` lists the same serial numbers with type and model under
*System information → Component information*. Firmware versions and activation date are
only there, though, not in the frame.

### Listening to what the app sends: `app-mqtt`

```bash
scripts/ecoflow-api.sh app-mqtt HC31XXXXXXXXXXXX        # default topic: set
```

Subscribes to one of the app topics under `/app/<userId>/<SN>/thing/property/` and shows
what arrives there. The default is `set` — the topic the script otherwise writes nothing
to. Operate the phone app while this is running and you see its commands in the original.
Pure subscription, no write access. With `set_reply` as the suffix you see the device's
answers as well. This is how the stream switch and the scheduled tasks (`96/125`, see
`api-status.md`, section 3) were captured.

Besides `fast`, only `request` and `live` publish, and only read requests to a `get`
topic; all other commands just subscribe.

`request` subscribes to **two** topics — `.../get_reply` and `.../quota` —, sends the
request to `.../get` and waits `ECOFLOW_WAIT` seconds (default 15). Listening on both is
not overeagerness: on some accounts the ACL refuses `.../get_reply` while granting
`.../quota`. The suffix is hard-wired; there is no free topic argument. Additionally needs
`mosquitto_pub`.

Four exit codes, not two — if you only check for "non-zero", you mistake a network error
for an API answer:

| Code | Meaning                                                    |
|------|------------------------------------------------------------|
| `0`  | the API answered with `code 0`                             |
| `1`  | usage or configuration error (missing variable …)          |
| `2`  | the API answered with a different code                     |
| `3`  | the request itself failed — network, TLS, name resolution  |

**`2` with code 1006** is the interesting answer: it means the model is excluded from the
Developer API (see `api-status.md`) and only the app MQTT channel or local Modbus remain.
The device has to be bound to your own EcoFlow account, otherwise the list stays empty.

