# CableMend

CableMend is an offline backend command line tool for **submarine telecom cable
fault localization and repair campaign planning**. It takes strictly decoded JSON
and JSONL documents describing cable systems, fault evidence, repair assets,
sea-state observations and work permits, and it produces fault positions with
explicit uncertainty windows, single-fault repair plans, multi-fault campaign
schedules and post-repair verification reports.

Everything runs locally. There is no network client, no telemetry, no service
dependency and no hidden state: the only state is a local directory of
append-only logs and plan snapshots.

## Independence notice

CableMend is an **independent original project**. It has **no affiliation with,
and no endorsement, sponsorship or association from or with any other product,
company, vessel operator, cable owner, standards body or organization**. Nothing
in this repository describes, models or reproduces any real vessel, fleet,
system, route, permit regime or commercial offering.

**All bundled sample data is fictional.** The system names, landing stations,
vessels, depots, jurisdictions, permits and weather series under `examples/` were
invented for this repository. Any resemblance to a real asset or authority is
coincidental.

## Motivation

Two currents in recent public technology reporting motivated this project, and
the description below is written from scratch rather than quoted from any
article. The first is the observation that the ships able to recover and rejoin
deep-water telecom cable are few, old and heavily booked, so the scarce resource
in a fault response is often the repair window rather than the repair itself. The
second is the emergence of autonomous and remotely operated subsea vehicles that
live near the seabed and can survey a damaged stretch before a ship arrives,
which shifts value towards knowing precisely _where_ to send a vessel and _when_
the sea, the permits and the spare cable line up.

CableMend is a small, self-contained answer to those two ideas: it turns raw test
measurements into a defensible position window, then treats vessels, depots,
weather windows and permits as hard constraints on a schedule that can be
reproduced and audited later.

## What it does

- **Fault localization.** Optical (OTDR), loop-resistance and power-feed voltage
  measurements from either end are converted from _cable_ kilometres into _route_
  kilometres (KP) through per-segment slack factors and inline housing cable
  allowances. Measurements are combined by inverse-variance weighting; the result
  is a best estimate plus a bounded uncertainty window, and disagreement beyond
  the configured tolerance is reported rather than averaged away.
- **Route mapping.** The window is mapped onto the containing segment, the
  overlapping protection zones and jurisdictions, the burial and depth profile
  and the nearest repeater or branching unit.
- **Asset modelling.** Cable ships carry transit speed, mobilization delay,
  spare cable and joint stock, ROV/AUV capability, maximum working depth and a
  sea-state limit. Depots hold spare cable and joints and declare sailing
  distances to route reference points.
- **Workability windows.** Hourly sea-state observations become per-day
  workability summaries and maximal runs of consecutive workable hours; the
  earliest run that can host the required number of consecutive hours is
  selected deterministically. Missing observation hours break a run and are
  reported as gaps.
- **Permits.** Every protection zone in the window that requires a permit must be
  covered by a single permit whose validity interval contains the whole work
  window and whose operation list contains every planned operation.
- **Repair plans.** Vessel and depot are chosen by a deterministic cost model
  (vessel time, transit, mobilization, standby, depot transfer, spare shortfall,
  missing survey capability). The plan expands into an ordered stage list with
  cumulative timing: mobilize, transit, standby, survey, cut-and-hold, splice,
  test, final burial and demobilize. Spare cable consumption accounts for route
  slack, the bight needed to lift the cable to the deck, a working slack
  allowance and an excess allowance at each side of the replaced stretch.
- **Campaigns.** Several faults across several systems are ordered by a priority
  score derived from traffic impact and declared urgency, vessels are held until
  released by their previous repair, depot and vessel stock is consumed as the
  campaign progresses, and unassignable faults are reported with reasons.
- **Post-repair verification.** Measured segment loss is compared with the loss
  budget implied by cable geometry and joint count, joint counts are checked
  against the per-segment limit, achieved burial is compared with the design
  profile, and a residual risk score with a band is produced.
- **Local store.** An append-only evidence log, an append-only work ledger, plan
  snapshots, a metadata document and a SHA-256 hash-chained audit log that can be
  re-verified at any time.

## Build and test (offline)

The module has no third-party dependencies, so everything works with the module
proxy switched off.

```sh
GOTOOLCHAIN=local GOPROXY=off go build ./...
GOTOOLCHAIN=local GOPROXY=off go vet ./...
GOTOOLCHAIN=local GOPROXY=off go test ./... -count=1
gofmt -l .
```

Build the CLI:

```sh
GOTOOLCHAIN=local GOPROXY=off go build -o cablemend ./cmd/cablemend
```

Go cache, temporary files and smoke-run artifacts belong under `.cache/`, which
is ignored by git:

```sh
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/gomod"
export GOTMPDIR="$PWD/.cache/gotmp"
```

## Commands

```
cablemend <command> [flags]

validate   strictly decode and cross-check every input document
ingest     append fault evidence to the local append-only store
locate     localize a fault and produce an uncertainty window
assets     list vessels and depots with transit and capability checks
window     compute workability windows from sea-state observations
plan       build a repair plan for one fault
campaign   plan repairs for several faults with vessel contention
verify     check post-repair measurements against the loss budget
report     summarize the local store and verify the audit chain
```

Flags shared by every subcommand:

| Flag                  | Meaning                                                                       |
| --------------------- | ----------------------------------------------------------------------------- |
| `--config PATH`       | strict JSON configuration document (optional; defaults are used when omitted) |
| `--store PATH`        | local store directory (default: `store_dir` from the configuration)           |
| `--format text\|json` | output format, `text` by default                                              |
| `--out PATH`          | write the rendered output to a file instead of stdout                         |

Notable per-command flags: `--systems`, `--evidence`, `--assets`, `--weather`,
`--permits`, `--faults`, `--verification` select input documents; `--system`,
`--fault`, `--kp` narrow the subject; `--hours`, `--max-wave`, `--max-wind`,
`--min-visibility`, `--not-before`, `--vessel`, `--hourly` drive window search;
`--as-of` fixes the planning instant; `--dry-run` suppresses store writes;
`--candidates` and `--plans` expand the reporting.

Exit codes: `0` success, `1` a command level failure (validation errors, a
rejected verification record, a broken audit chain), `2` usage error.

### Example session

```sh
cablemend validate --config examples/config.json \
  --systems examples/systems.json --evidence examples/evidence.jsonl \
  --assets examples/assets.json --weather examples/weather.jsonl \
  --permits examples/permits.json --faults examples/faults.json \
  --verification examples/verification.jsonl

cablemend ingest --config examples/config.json \
  --systems examples/systems.json --evidence examples/evidence.jsonl

cablemend locate --config examples/config.json \
  --systems examples/systems.json --system SYS-COR --fault F-1001

cablemend window --config examples/config.json --systems examples/systems.json \
  --weather examples/weather.jsonl --assets examples/assets.json \
  --system SYS-COR --vessel CS-AURORA --hours 36

cablemend plan --config examples/config.json --systems examples/systems.json \
  --assets examples/assets.json --weather examples/weather.jsonl \
  --permits examples/permits.json --system SYS-COR --fault F-1001 --candidates

cablemend campaign --config examples/config.json --systems examples/systems.json \
  --assets examples/assets.json --weather examples/weather.jsonl \
  --permits examples/permits.json --faults examples/faults.json

cablemend verify --config examples/config.json \
  --systems examples/systems.json --verification examples/verification.jsonl

cablemend report --config examples/config.json --format json
```

## Input formats

All documents are decoded strictly: unknown fields are rejected, any content
after the top-level JSON value is rejected, and JSONL problems are reported with
the file name and line number. Every timestamp must be exactly
`YYYY-MM-DDTHH:MM:SSZ` — no offsets, no fractional seconds.

### Configuration (`--config`, JSON)

A partial document is allowed: omitted fields keep their documented default, and
every field is range checked. Sections: `localization` (slack fallback,
per-method uncertainty, disagreement tolerance, coverage factor, window bounds),
`repair` (stage durations, joints per repair, slack/bight/excess allowances,
depth margin), `weather` (wave, wind and visibility limits, observation length),
`costs` (selection weights and penalties), `verification` (loss tolerance, joint
loss, joint limit, depth and burial thresholds), `output` (`float_decimals`) and
`priorities` (campaign scoring weights). See `examples/config.json`.

### Cable systems (`--systems`, JSON)

`version` plus a list of systems. Each system declares landing stations, KP
contiguous segments (cable type, slack factor, existing joints, loss per km, a
burial profile of depth and burial spans), repeaters and equalizers with a cable
allowance, branching units, and protection zones with jurisdiction, permit
requirement, anchoring restriction and a risk weight. Segments must be
contiguous, zones must not overlap and every position must lie on the route.

### Fault evidence (`--evidence`, JSONL, one record per line)

```json
{
  "id": "EV-1001-A1",
  "system_id": "SYS-COR",
  "fault_id": "F-1001",
  "observed_at": "2026-03-10T01:05:00Z",
  "end": "A",
  "method": "otdr",
  "instrument": "OTDR-A1",
  "cable_distance_km": 133.05,
  "loss_step_db": 4.2,
  "insulation_resistance_mohm": 0.4,
  "fault_class": "shunt",
  "notes": "north trace"
}
```

`end` is `A` (low KP) or `B` (high KP). `method` is `otdr` (requires
`cable_distance_km`), `resistance` (requires `resistance_ohm` and
`conductor_ohm_per_km`) or `voltage` (requires `voltage_v` and
`voltage_gradient_v_per_km`). `fault_class` may be `shunt`, `open` or `unknown`;
when it is absent the insulation reading decides, with the loss step as a weak
fallback. `uncertainty_km` overrides the per-method uncertainty.

### Repair assets (`--assets`, JSON)

Vessels (transit speed, mobilization hours, spare cable and joints, ROV/AUV,
maximum working depth, sea-state limit, day rate, availability, station depot)
and depots (spare stock plus `access` entries giving a route reference KP and the
sailing distance to it). Distance to a work site is the access distance plus the
along-route offset converted to nautical miles; depot-to-depot distance is
derived through the cheapest shared route reference.

### Sea-state observations (`--weather`, JSONL)

```json
{
  "system_id": "SYS-COR",
  "hour_start": "2026-03-11T06:00:00Z",
  "wave_height_m": 1.3,
  "wind_speed_kn": 16.0,
  "visibility_km": 14.0,
  "current_kn": 0.5,
  "source": "AURIC-MET"
}
```

One record per observation hour per system. A missing hour breaks a run.

### Permits (`--permits`, JSON)

Each permit names a system, a protection zone, a jurisdiction, a validity
interval and the operations it authorizes (`survey`, `cut_and_hold`, `splice`,
`test`, `burial`).

### Faults (`--faults`, JSON)

Each fault names a system, a report time, a declared priority, traffic impact in
Tbps, an optional single-route marker and an optional restoration deadline.
Evidence is linked to a fault by `fault_id`.

### Post-repair verification (`--verification`, JSONL)

Measured segment loss, joint count after the repair, repair KP, spare cable used,
achieved burial and whether a vehicle inspection was recorded.

## Determinism

CableMend is designed so that the same inputs always yield byte-identical output.

- Maps are never iterated for output: every list is sorted by an explicit key,
  and ties fall back to identifiers so ordering is total.
- Floating point values are rounded once, to the configured decimal count, and
  formatted with a fixed number of decimals.
- Sums that could depend on accumulation order are computed over sorted values.
- All timestamps in stored artifacts come from the input data. The wall clock is
  never read: `--as-of` defaults to the latest input timestamp, and stage times
  are derived from it by rounding fractional hours to whole seconds.
- No random values, no process identifiers, no host paths inside stored
  documents apart from the store directory that the operator chose.
- Vessel and depot selection compares rounded costs first and then completion
  time, vessel id and depot id, so an exact cost tie still has one answer.

## Local store

The store directory (default `.cablemend`) contains:

| Path               | Content                                                  |
| ------------------ | -------------------------------------------------------- |
| `evidence.jsonl`   | append-only evidence log, de-duplicated by record id     |
| `ledger.jsonl`     | append-only work ledger, one entry per committed event   |
| `audit.jsonl`      | SHA-256 hash chain over the ledger entries               |
| `meta.json`        | counts, known systems and faults, last event, audit head |
| `snapshots/*.json` | plan, campaign and verification snapshots                |

`meta.json` and snapshots are written atomically (temporary file plus rename).
Each audit record hashes a fixed, field-separated pre-image over its sequence
number, event time, kind, subject, payload hash and the previous record hash.
`cablemend report` recomputes the whole chain, cross-checks it against the ledger
and fails when a link, hash, sequence number or ordering is wrong.

## Docker

The image is built in two stages: a `golang:1.22` builder with
`GOTOOLCHAIN=local`, `CGO_ENABLED=0` and `GOPROXY=off`, and a `scratch` final
stage that contains only the static binary as its entrypoint.

```sh
docker build -t cablemend:local .

# The tool needs no network at all.
docker run --rm --network none cablemend:local version

docker run --rm --network none \
  -v "$PWD/examples:/data:ro" cablemend:local \
  validate --config /data/config.json --systems /data/systems.json \
    --evidence /data/evidence.jsonl --assets /data/assets.json \
    --weather /data/weather.jsonl --permits /data/permits.json \
    --faults /data/faults.json --verification /data/verification.jsonl

docker run --rm --network none \
  -v "$PWD/examples:/data:ro" cablemend:local \
  plan --config /data/config.json --systems /data/systems.json \
    --assets /data/assets.json --weather /data/weather.jsonl \
    --permits /data/permits.json --evidence /data/evidence.jsonl \
    --system SYS-COR --fault F-1001 --dry-run
```

Commands that write to the store need a writable mount, for example
`-v "$PWD/.cache/store:/store" ... --store /store`.

## Repository layout

```
cmd/cablemend        CLI entry point
internal/cli         subcommands, flag handling, text and JSON rendering
internal/config      configuration document, defaults and range checks
internal/jsonio      strict JSON and JSONL decoding, canonical encoding
internal/model       domain model: systems, evidence, assets, permits, time
internal/locate      cable distance to route KP conversion and combination
internal/weather     workability classification and window selection
internal/permits     permit coverage assessment and candidate start times
internal/planner     vessel and depot selection, stage plan, spares, risk
internal/campaign    multi-fault priority ordering and vessel contention
internal/quality     post-repair verification and residual risk
internal/store       append-only logs, snapshots, hash-chained audit log
internal/validate    input loading and cross-document checks
internal/numeric     deterministic arithmetic, rounding and formatting
examples/            fictional sample data
```

## Licence

No licence is granted by this repository; it is published as a self-contained
technical exercise.
