# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

**`ecoflowd`** is the product: a service that reads the **EcoFlow PowerOcean DC Fit**
over the app's cloud channel and publishes the readings to a local MQTT broker; with
`--listen` it also shows them on a read-only web page. It ships
as release binaries (systemd, `contrib/`) and as a container image (`Dockerfile`,
`docker-compose.yaml`). The README is written for its users and leads with installing it.

Behind it, as background rather than as the subject of the repo:

1. **Research notes** in `docs/research/` on the ways to integrate the DC Fit – cloud
   paths vs. local Modbus TCP, register map, open points.
2. **`scripts/ecoflow-api.sh`** and **`scripts/ecoflow-frames.py`** – the measuring tool
   on the cloud channel and the frame decoder, described in `docs/research/tools.md`.
   Practically all findings in these notes were made with them.

The Modbus CLI used to check the register claims on the device, `modbusread`, lived here
as `cmd/modbusread` up to v0.6.0 and now has its own repo,
[womat/modbusread](https://github.com/womat/modbusread): it is deliberately universal and
knows nothing about EcoFlow, so it does not belong next to a device-specific service. Its
code conventions (read-only, addresses never converted, raw words always shown) are kept
in that repo's CLAUDE.md. The notes here still refer to it by name.

## Commands

`make help` lists the targets; `make test` and `make lint` together run what CI runs.

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
`main` via PR — CI (`gofmt`, `vet`, `build`, `test -race`, `govulncheck`, `docker build`) has to be green. No `develop`:
`go install …@latest` resolves to the newest semver tag, not to a branch, so a second
permanent branch would only create a merge ritual with nothing in return.

A release is a tag `vX.Y.Z` on `main`, made with `make release TAG=vX.Y.Z`; the release
workflow refuses a tag that is not on `main`, tests again, lets GoReleaser
(`.goreleaser.yaml`) build the archives – binary, systemd unit, README, LICENSE – and
stamps the tag in via `-X main.version`, then pushes the image. Do not tag on other
branches — otherwise a release points at a state that was never in `main`. CI and release
build with Go `stable`, not with `go-version-file`, on purpose: standard library security
fixes reach a release through the toolchain.

## Language

**Everything in the repo is English: documentation, code comments, `--help` text, error
messages and program output.** New or changed Markdown sections too. The one exception is
`README.de.md`, a German summary of about 50 lines like the maintainer's other public
repos have; it links to the English README for every detail instead of repeating it, and
carries along only what it states itself (install paths, topic names, the licence note).

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
  `--listen` an HTTPS server joins it (`https.go`): the web page (`ui.go`, embedded
  `ui/index.html`, public, no data in it) and `GET /status` (token), fed by `status.go`,
  which keeps the last reading, the day's totals and the connection state for the
  lifetime of the process. The page follows the sibling projects' pages (s0meter,
  smartmeter, modbusgateway: same tokens and parts, flow strip with the client left and
  the device right) and is **read-only** – a switch on it would be a third write path, see
  below. Its screenshots come from `docs/screenshots/capture.py`. With `--block` a third
  telegram (`<topic>/block`) and the `/block` endpoint on the same server join it: the
  discharge block (`block.go` the state, `https.go` the endpoint and the list of its
  callers), see the write paths below. Deliberately device-specific; the knowledge for
  that lives in `internal/frames` and `internal/ecoflow`
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
- `contrib/` – operational extras that are not built: the systemd template for `ecoflowd`,
  shipped in every release archive
- `Makefile` – `make help`; `test` also runs the golden check and the script selftest,
  `golden` regenerates the `.golden` files
- `docs/social-preview.png` – rendered from `docs/social-preview.html` with headless Chrome
  (command in the file) and uploaded by hand under Settings → Social preview
- `Dockerfile`, `docker-compose.yaml`, `.env.example` – the Docker way to run it. The
  image is `scratch` plus the binary and the CA bundle, built by cross-compiling (no
  emulation), user `65532`. The compose file uses `restart: on-failure:3`, **not**
  `unless-stopped`: it is the counterpart to `RestartPreventExitStatus=78` in the unit,
  and an endless restart would repeat a rejected login for ever. `--listen` takes a
  comma-separated list of concrete addresses (on a host: `172.17.0.1,<LAN address>`,
  the LAN address fixed in the router), never `0.0.0.0`; in Docker it takes the
  container's name (`--listen=ecoflowd:8089`); without `ports:` only
  containers on the network reach it, which is enough for a caller of `--block`. For the
  web page the port is published on one host address only
  (`ports: ["192.168.1.10:8089:8089"]`, decided 10 Oct 2026) – `/block` is then on that
  address too, behind token and TLS
- `.github/workflows/` – `ci.yml` (gofmt, vet, build, test -race, govulncheck, and a
  `docker build`) and `release.yml` (tag on `main`?, vet, test and govulncheck again, then
  GoReleaser for seven platform archives; afterwards the image for four Linux platforms to
  `ghcr.io/womat/ecoflowd`). Actions are pinned to a commit SHA with the release in a
  comment, never to a movable tag like `@v7`; `.github/dependabot.yml` proposes weekly
  updates for them and the Go modules, but not for the `go install` pin of govulncheck
- `ecoflow-open-demo/` – EcoFlow's official Java demo client, downloaded for reference
  only. Deliberately **not** versioned via `.gitignore`; do not "tidy it up"
- `README.md` – entry point for users of `ecoflowd`: what it does, quick start with
  Docker and systemd, configuration, MQTT output, the web page (`--listen`), `--fast`, `--block`, how it works; the
  research only as a short section with links
- `docs/research/README.md` – entry point to the research: disclaimer, summary, open
  points, sources, the pointer to `modbusread`
- `docs/research/tools.md` – usage of `ecoflow-api.sh` and `ecoflow-frames.py`
- `docs/research/api-status.md` – the *decision level*: cloud REST (EcoFlow Developer/Open API,
  HMAC-signed, often returns error 1006 for PowerOcean) vs. local **Modbus TCP** (port
  502, unlocked only by the installer via the EcoFlow **Pro app**)
- `docs/mqtt-output.md` – the *output side*: how `ecoflowd` publishes to the local broker and
  why — two JSON telegrams instead of, as up to v0.4.x, one topic per value. Contains the
  reasoning (time of measurement in the payload instead of heartbeat and last will,
  camelCase, missing field = 0 because of proto3, `dcdc` only once understood) and the
  measurements it rests on
- `docs/research/modbus-registers.md` – the *detail level*: register map, encoding conventions, Python
  decoding snippets (pymodbus), known gaps

The Markdown files overlap on purpose: the README links the others, `api-status.md`
refers to `modbus-registers.md` for the mapping and to `mqtt-output.md` for the output
side. On changes of substance (e.g. error 1006 solved, unlocking path found) **carry all
affected places along**, including the "Open questions" checklist in `api-status.md` and
"Open points" in `docs/research/README.md`.

The same has applied to the tools since the cloud channel: whoever changes
`scripts/ecoflow-api.sh`, `scripts/ecoflow-frames.py` or `cmd/ecoflowd` carries the
matching README sections (for the scripts: `docs/research/tools.md`) and the `--help`
text along; a new or changed flag or environment variable of `ecoflowd` also goes into
`docker-compose.yaml`, `.env.example` and the systemd unit. **This has been skipped several
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
      over MQTT, and never from the web page – the page shows the block read-only;
    - exactly two message kinds, both captured from the app: the empty task list request
      `96/127`, and `96/125` carrying the task **exactly as the device listed it**, with
      only field 4 (on/off) changed;
    - exactly one task of type 1, or `--block-task N`; nothing is guessed;
    - nothing is created, deleted, moved or re-timed. The app task's window decides when a
      block can apply.

    The byte-for-byte test against the capture (`internal/frames/tasks_test.go`) is what
    this rests on.

  A third write path, or widening either of these (creating tasks, writing times, the
  community's `96/112`/`96/98`, a block switch on the web page), would be a decision of
  its own, not an extension. Why
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
  either; the current one is in `docs/research/modbus-registers.md`.

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
