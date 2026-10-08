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

The Modbus CLI used to check the register claims on the device, `modbusread`, lived here
as `cmd/modbusread` up to v0.6.0 and now has its own repo,
[womat/modbusread](https://github.com/womat/modbusread): it is deliberately universal and
knows nothing about EcoFlow, so it does not belong next to a device-specific service. Its
code conventions (read-only, addresses never converted, raw words always shown) are kept
in that repo's CLAUDE.md. The notes here still refer to it by name.

## Commands

```
go build ./...                 # builds cmd/ecoflowd
go test ./...                  # everything, runs without hardware
go test -run TestEnergyBalances ./internal/frames/   # a single test
go vet ./...
gofmt -l ./cmd ./internal      # no output = fine; CI fails on it
scripts/ecoflow-api.sh selftest # signature and stream switch frame, no network
```

`internal/frames` and `cmd/ecoflowd`
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

Why: `ecoflowd` should be usable without reading German, and the repo is
public. The research notes were originally written in German and translated in Sep 2026;
keeping a German copy alongside would have been a
second version that falls behind. Conversations with the maintainer may still be in
German — that concerns the chat, not the files.

## Structure & how the files fit together

- `cmd/ecoflowd/` – service for continuous operation on the cloud channel: flags,
  connection loop with backoff, and the output side – it publishes the readings to a
  local MQTT broker as two JSON telegrams (`<topic>/state`, `<topic>/energy`) with serial
  number and time of measurement in the payload; no retain, no availability topic, no
  heartbeat. Only what is understood goes into the telegram — `dcdc` is therefore
  deliberately missing. None of it is on disk; the session lives in the process. With
  `--block` a third telegram (`<topic>/block`) and an HTTPS endpoint join it: the
  discharge block (`block.go` the state, `https.go` the endpoint), see the write paths
  below. Deliberately device-specific; the knowledge for that lives in
  `internal/frames` and `internal/ecoflow`
- `internal/frames/` – pure functions over `[]byte`: the protobuf frames of the app MQTT
  channel (wrapper, XOR obfuscation, energy reports, hourly history, component list,
  scheduled task lists, building the stream switch and the task commands). Replies on
  `set_reply` are plain, everything on the push topic is XORed – hence `ParseReply`
  next to `Parse`. Network-free: a misread field produces a plausible *wrong number*
  rather than a crash, so the interpretation has to be directly testable.
  **This is where the EcoFlow knowledge lives.**
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
- `.github/workflows/` – `ci.yml` (gofmt, vet, build, test -race, govulncheck) and
  `release.yml` (vet, test and govulncheck again, then both binaries for six platforms,
  tag `vX.Y.Z` on `main`). Actions are pinned to a commit SHA with the release in a
  comment, never to a movable tag like `@v7`; `.github/dependabot.yml` proposes weekly
  updates for them and the Go modules, but not for the `go install` pin of govulncheck
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

- **The two write paths in the repo, and how they are fenced in:** on the app MQTT channel
  there are exactly two, both on `.../set`.
  - The first is the `EnergyStreamSwitch` (`96/97`), which switches on the fast data
    stream. It is bound to four conditions, and together they are the rule: it hangs on a
    **command of its own** (`ecoflow-api.sh fast`) or a **flag of its own**
    (`ecoflowd --fast`), so it never happens as a side effect of reading; it carries **no
    parameters**; its bytes were **captured** on the wire and are replayed unchanged, only
    the sequence number varies.
  - The second is the **discharge block** (`ecoflowd --block`, decided by the maintainer
    on 3 Oct 2026 after the fifth write test). It switches one scheduled task of type
    "Laden des Akkus" on and off. Its fence:
    - a flag of its own, triggered only over a local HTTPS endpoint with a token, never
      over MQTT;
    - exactly two message kinds, both captured from the app: the empty task list request
      `96/127`, and `96/125` carrying the task **exactly as the device listed it**, with
      only field 4 (on/off) changed;
    - exactly one task of type 1, or `--block-task N`; nothing is guessed;
    - nothing is created, deleted, moved or re-timed. The app task's window decides when a
      block can apply.

    The byte-for-byte test against the capture (`internal/frames/tasks_test.go`) is what
    this rests on.

  A third write path, or widening either of these (creating tasks, writing times, the
  community's `96/112`/`96/98`), would be a decision of its own, not an extension. Why
  this is so strict: an attempt assembled from third-party sources was wrong in four
  places – on a topic through which the device can be reconfigured. And the fourth write
  test showed that a task with self-chosen times is stored but not executed.

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
  table format (Register | Type | Description); state the unit, and a scale factor
  whenever it is not 1, explicitly in the description rather than implying it.
- **Write registers:** keep the split into "explicitly writable" / "known, not exposed" /
  "unknown", and do not remove the warning about write access (power limits
  40554/40556).
