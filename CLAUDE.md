# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

Research notes and the tools they were produced with:

1. **Research notes** (Markdown) on the ways to integrate the **EcoFlow PowerOcean DC
   Fit** – cloud paths vs. local Modbus TCP, register map.
2. **`scripts/ecoflow-api.sh`** and **`scripts/ecoflow-frames.py`** – the measuring tool
   on the cloud channel and the frame decoder. Practically all findings in these notes
   were made with them.
3. **`cmd/ecoflowd`** – the service for continuous operation on the same channel: reads
   the readings and publishes them to a local MQTT broker.
4. **`modbusread`** – a Go CLI used to check the claims in these notes on the device.
   Deliberately **universal**: it contains no EcoFlow knowledge, no built-in register
   map and no device-specific messages. Speaks Modbus TCP and Modbus RTU (serial); the
   transport follows from the target argument (`cmd/modbusread/target.go`), not from a
   flag.

## Commands

```
go build ./...                 # builds cmd/modbusread and cmd/ecoflowd
go test ./...                  # everything, runs without hardware
go test -run TestParseAddr ./internal/decode/   # a single test
go vet ./...
gofmt -l ./cmd ./internal      # no output = fine; CI fails on it
scripts/ecoflow-api.sh selftest # signature and stream switch frame, no network
```

The integration tests start the Modbus server from `github.com/simonvetter/modbus` on a
free port and read against it – no device needed. `internal/frames` and `cmd/ecoflowd`
test against anonymised captures from the real device in `internal/frames/testdata/`;
the `.golden` files there are the output of `scripts/ecoflow-frames.py` over the same
captures and are read by `cmd/ecoflowd/ecoflowd_test.go` — they keep the Go and the
Python version line-for-line identical. **Both directions are checked:** `go test`
measures the Go version against the `.golden` files, a CI step regenerates them with the
Python decoder and compares.

`damaged.txt` is not a capture but built by hand: four deliberately broken frames on
which the Python decoder used to end mid-stream with a traceback. Its `.golden` file is
**empty** and so says two things — neither version reports a reading from it, and
neither gets stuck on it. New failure cases go in there.

Whoever changes either version regenerates the `.golden` files:

```
python3 scripts/ecoflow-frames.py < internal/frames/testdata/fast.txt \
  > internal/frames/testdata/fast.golden
```

## Branches & releases

One permanent branch: **`main`**. Work happens in short-lived feature branches that go to
`main` via PR — CI (`gofmt`, `vet`, `build`, `test -race`) has to be green. No `develop`:
`go install …@latest` resolves to the newest semver tag, not to a branch, so a second
permanent branch would only create a merge ritual with nothing in return.

A release is a tag `vX.Y.Z` on `main`; the release workflow builds the binaries from it
and stamps the tag number in via `-X main.version`. Do not tag on other branches —
otherwise a release points at a state that was never in `main`.

## Language

**Everything in the repo is English: documentation, code comments, `--help` text, error
messages and program output.** New or changed Markdown sections too.

Why: `ecoflowd` and `modbusread` should be usable without reading German, and the repo is
public. The research notes were originally written in German and translated in Sep 2026;
keeping a German copy alongside would have been a
second version that falls behind. Conversations with the maintainer may still be in
German — that concerns the chat, not the files.

## Structure & how the files fit together

- `cmd/modbusread/` – CLI: flags, reading with chunking/error isolation, output, polling
- `cmd/ecoflowd/` – service for continuous operation on the cloud channel: flags,
  connection loop with backoff, and the output side – it publishes the readings to a
  local MQTT broker as two JSON telegrams (`<topic>/state`, `<topic>/energy`) with serial
  number and time of measurement in the payload; no retain, no availability topic, no
  heartbeat. Only what is understood goes into the telegram — `dcdc` is therefore
  deliberately missing. None of it is on disk; the session lives in the process. Unlike
  `modbusread`, deliberately device-specific; the knowledge for that lives in
  `internal/frames` and `internal/ecoflow`
- `internal/decode/` – pure functions over `[]uint16` (types, word/byte order, address
  parsing). This is where the logic lives that produces *wrong numbers* rather than
  crashes when it errs – hence kept network-free and fully testable
- `internal/frames/` – pure functions over `[]byte`: the protobuf frames of the app MQTT
  channel (wrapper, XOR obfuscation, energy reports, hourly history, component list,
  building the stream switch). Network-free for the same reason as `internal/decode`.
  **This is where the EcoFlow knowledge lives**, so that `modbusread` stays universal.
  The tests run against anonymised captures from the real device in `testdata/` and
  check the two arithmetic identities that established the field assignment. The
  `.golden` files live here, but they are read by `cmd/ecoflowd` – that is where the
  formatting they pin down lives
- `internal/ecoflow/` – the way *in*: login, certification, client id, topics. The
  counterpart to `internal/frames`, which only interprets what is already there; the two
  do not know each other. Tested against an `httptest` server, not against the real cloud
- `scripts/ecoflow-api.sh` – the measuring tool on the cloud channel and the version with
  which all findings were made: Developer API, portal REST, app MQTT, listening on the
  `set` topic. Stays alongside `ecoflowd` – for the next unknown identifier you reach for
  it again
- `scripts/ecoflow-frames.py` – unpacks the frames that `ecoflow-api.sh live|fast`
  delivers; `--hours` and `--modules` can do more than the Go service. Generates the
  `.golden` files
- `contrib/` – operational extras that are not built: the systemd template for `ecoflowd`
- `.github/workflows/` – `ci.yml` (gofmt, vet, build, test -race) and `release.yml`
  (both binaries for six platforms, tag `vX.Y.Z` on `main`)
- `ecoflow-open-demo/` – EcoFlow's official Java demo client, downloaded for reference
  only. Deliberately **not** versioned via `.gitignore`; do not "tidy it up"
- `README.md` – entry point, disclaimer, summary, list of sources, open points
- `api-status.md` – the *decision level*: cloud REST (EcoFlow Developer/Open API,
  HMAC-signed, often returns error 1006 for PowerOcean) vs. local **Modbus TCP** (port
  502, unlocked only by the installer via the EcoFlow **Pro app**)
- `mqtt-output.md` – the *output side*: how `ecoflowd` publishes to the local broker and
  why — two JSON telegrams instead of, as up to v0.4.x, one topic per value. Contains the
  reasoning (time of measurement in the payload instead of heartbeat and last will,
  camelCase, missing field = 0 because of proto3, `dcdc` only once understood) and the
  measurements it rests on
- `modbus-registers.md` – the *detail level*: register map, encoding conventions, Python
  decoding snippets (pymodbus), known gaps

The four Markdown files overlap on purpose: the README links all three, `api-status.md`
refers to `modbus-registers.md` for the mapping and to `mqtt-output.md` for the output
side. On changes of substance (e.g. error 1006 solved, unlocking path found) **carry all
affected places along**, including the "Open questions" checklist in `api-status.md` and
"Open points" in the README.

The same has applied to the tools since the cloud channel: whoever changes
`scripts/ecoflow-api.sh`, `scripts/ecoflow-frames.py` or `cmd/ecoflowd` carries the
matching README sections and the `--help` text along. **This has been skipped several
times** — one review found about 60 places where the docs described what used to be
true: among them the promise that the script "cannot change anything on the device", and
an explanation for the REST timestamp freezing that its own later measurement had
refuted. When catching up, the code counts, not the older prose.

## Code conventions

- **Read-only is a hard property, not a default:** `modbusread` calls no `Write*` method
  of the library. The mapping is unconfirmed (see below); a tool without a write path
  cannot write by accident. Do not soften this.
- **The one write path in the repo, and how it is fenced in:** on the app MQTT channel
  there is exactly one – the `EnergyStreamSwitch` on `.../set`, which switches on the fast
  data stream. It is bound to four conditions, and together they are the rule: it hangs
  on a **command of its own** (`ecoflow-api.sh fast`) or a **flag of its own**
  (`ecoflowd --fast`), so it never happens as a side effect of reading; it carries **no
  parameters**; its bytes were **captured** on the wire and are replayed unchanged, only
  the sequence number varies; and it is the only one there. A second write path would be
  a decision of its own, not an extension of this one. Why this is so strict: an attempt
  assembled from third-party sources was wrong in four places – on a topic through which
  the device can be reconfigured.
- **Addresses are never converted** – what is typed goes on the wire as it is (0-based).
  Many sources document 1-based; converting is deliberately left to the human, so the
  tool does not hide an assumption.
- **The tool computes word/byte order itself**, the client runs fixed on
  `BIG_ENDIAN, HIGH_WORD_FIRST` and only reads `ReadRegisters`. That way the raw words
  are always available for the output and the decoding stays purely testable.
- **Flags that cannot take effect are rejected rather than ignored** – the serial
  parameters on a TCP target are an error. A baud rate that silently has no effect sends
  people debugging the hardware.
- **Raw words are always in the output**, even when a value was decoded – in reverse
  engineering the raw value matters more than the interpretation.
- **Address parsing deliberately does not use `strconv.ParseUint(s, 0, …)`** (base 0
  would read `042` as octal 34).

## Content conventions (docs)

- **Mark provenance:** practically everything here is community reverse engineering, not
  official EcoFlow documentation. Back new claims with a source URL (maintain the source
  lists at the end of each file) and separate confirmed knowledge from assumptions in the
  wording ("presumably", "not confirmed").
- **Plus vs. DC Fit:** the model question is open, **but the direction has turned**: the
  current source treats the DC Fit as the normal case and knows exactly *one*
  model-dependent special case, and that one concerns the Plus. The earlier worry that the
  mapping was "determined on the Plus and questionable for the DC Fit" is thereby
  outdated — nothing is confirmed because of it, only the suspicion is a different one.
  Do not cut the caveat from register statements, but do not preserve it in the old form
  either; the current one is in `modbus-registers.md`.

  **For the protobuf field numbers of the cloud channel, on the other hand, it applies
  sharply and in the original direction:** `cmd_func 96 / cmd_id 33` uses different
  fields on the DC Fit than on the Plus, measured. Whoever takes the wrong table there
  gets plausible numbers under wrong names.
- **Register tables:** the addresses are given as they go on the wire – the reference
  integration passes the 4xxxx numbers unchanged to pymodbus, and so does `modbusread`.
  **Whether they are meant 1-based or 0-based is unresolved**; the earlier claim
  "1-based" in these notes is now seriously in doubt and can only be decided on the
  device. That is exactly why nothing is converted and nothing is added that supports
  only one of the two readings. Floats occupy 2 registers, 32-bit IEEE754
  **word-swapped** (high word in the second register). Add new entries in the existing
  table format (Register | Unit | Scale | Description) with an explicit scale factor.
- **Write registers:** keep the split into "explicitly writable" / "known, not exposed" /
  "unknown", and do not remove the warning about write access (power limits
  40554/40556).
