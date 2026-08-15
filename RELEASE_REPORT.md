# CableMend release report

CableMend is an independent original project with no affiliation, endorsement,
sponsorship or association with any other product, company, vessel operator or
organization. All sample data under `examples/` is fictional.

- Module: `CableMend`
- Go directive: `go 1.22.5`
- Third-party dependencies: none (standard library only, no `go.sum`)
- Packages: 14 (including the `main` command package)

## Effective production LOC

Effective LOC counts lines in non-test `.go` files, excluding blank lines and
comment-only lines. No generated code is present.

| File                          | Effective LOC |
| ----------------------------- | ------------- |
| cmd/cablemend/main.go         | 8             |
| internal/campaign/campaign.go | 382           |
| internal/cli/app.go           | 213           |
| internal/cli/cmd_assets.go    | 241           |
| internal/cli/cmd_campaign.go  | 226           |
| internal/cli/cmd_ingest.go    | 158           |
| internal/cli/cmd_locate.go    | 157           |
| internal/cli/cmd_plan.go      | 275           |
| internal/cli/cmd_report.go    | 240           |
| internal/cli/cmd_validate.go  | 88            |
| internal/cli/cmd_verify.go    | 140           |
| internal/cli/cmd_window.go    | 179           |
| internal/cli/render.go        | 115           |
| internal/config/config.go     | 302           |
| internal/jsonio/jsonio.go     | 145           |
| internal/locate/locate.go     | 430           |
| internal/model/assets.go      | 275           |
| internal/model/evidence.go    | 229           |
| internal/model/fault.go       | 135           |
| internal/model/index.go       | 444           |
| internal/model/permit.go      | 143           |
| internal/model/system.go      | 364           |
| internal/model/time.go        | 154           |
| internal/model/weather.go     | 96            |
| internal/numeric/numeric.go   | 136           |
| internal/permits/permits.go   | 153           |
| internal/planner/planner.go   | 421           |
| internal/planner/spares.go    | 164           |
| internal/planner/types.go     | 179           |
| internal/quality/quality.go   | 261           |
| internal/store/audit.go       | 173           |
| internal/store/store.go       | 344           |
| internal/validate/check.go    | 260           |
| internal/validate/load.go     | 171           |
| internal/weather/weather.go   | 333           |
| **Total (production)**        | **7734**      |

Requirement was at least 2600 effective production LOC.

## Test LOC

| File                               | Effective LOC |
| ---------------------------------- | ------------- |
| internal/campaign/campaign_test.go | 212           |
| internal/cli/cli_test.go           | 387           |
| internal/config/config_test.go     | 91            |
| internal/jsonio/jsonio_test.go     | 112           |
| internal/locate/locate_test.go     | 216           |
| internal/model/index_test.go       | 194           |
| internal/model/time_test.go        | 85            |
| internal/numeric/numeric_test.go   | 119           |
| internal/permits/permits_test.go   | 126           |
| internal/planner/planner_test.go   | 374           |
| internal/quality/quality_test.go   | 217           |
| internal/store/store_test.go       | 264           |
| internal/validate/validate_test.go | 231           |
| internal/weather/weather_test.go   | 174           |
| **Total (tests)**                  | **2802**      |

## Packages

| Import path                   | Responsibility                                                 |
| ----------------------------- | -------------------------------------------------------------- |
| `CableMend/cmd/cablemend`     | process entry point, exit code plumbing                        |
| `CableMend/internal/cli`      | nine subcommands, shared flags, text and JSON rendering        |
| `CableMend/internal/config`   | configuration document, defaults, range validation             |
| `CableMend/internal/jsonio`   | strict JSON/JSONL decoding, canonical encoding                 |
| `CableMend/internal/model`    | domain model: time, systems, evidence, assets, permits, faults |
| `CableMend/internal/locate`   | cable distance to route KP conversion, evidence combination    |
| `CableMend/internal/weather`  | hourly workability classification and window selection         |
| `CableMend/internal/permits`  | permit coverage assessment, candidate start instants           |
| `CableMend/internal/planner`  | vessel/depot selection, stage plan, spare consumption, risk    |
| `CableMend/internal/campaign` | multi-fault priority ordering, vessel contention, stock draw   |
| `CableMend/internal/quality`  | post-repair loss budget, joint limits, residual risk           |
| `CableMend/internal/store`    | append-only logs, atomic snapshots, hash-chained audit log     |
| `CableMend/internal/validate` | input loading and cross-document consistency checks            |
| `CableMend/internal/numeric`  | deterministic rounding, formatting, weighted statistics        |

## Validation results

All commands were run with `GOTOOLCHAIN=local`, `GOPROXY=off` and with
`GOCACHE`, `GOMODCACHE` and `GOTMPDIR` redirected under `.cache/`.

| Command                                                                                              | Result                                                      |
| ---------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- |
| `gofmt -l .`                                                                                         | clean (no output)                                           |
| `go build ./...`                                                                                     | pass                                                        |
| `go vet ./...`                                                                                       | pass (no findings)                                          |
| `go test ./... -count=1`                                                                             | pass, 14 packages, 0 failures                               |
| `docker build -t cablemend:local .`                                                                  | pass, image `cablemend:local` (scratch, static binary only) |
| `docker run --rm --network none cablemend:local version`                                             | pass, prints `cablemend 1.0.0`                              |
| `docker run --rm --network none -v .../examples:/data:ro cablemend:local validate ...`               | pass, `ok: yes`, 0 errors, 0 warnings                       |
| `docker run --rm --network none -v .../examples:/data:ro cablemend:local plan ... --dry-run`         | pass, feasible plan identical to the host run               |
| `docker run --rm --network none -v .../examples:/data:ro cablemend:local campaign ... --format json` | pass, 3 of 3 faults scheduled                               |

### Test package summary

```
ok  CableMend/internal/campaign
ok  CableMend/internal/cli
ok  CableMend/internal/config
ok  CableMend/internal/jsonio
ok  CableMend/internal/locate
ok  CableMend/internal/model
ok  CableMend/internal/numeric
ok  CableMend/internal/permits
ok  CableMend/internal/planner
ok  CableMend/internal/quality
ok  CableMend/internal/store
ok  CableMend/internal/validate
ok  CableMend/internal/weather
?   CableMend/cmd/cablemend  [no test files]
```

### Offline CLI smoke workflow

Executed end to end against `examples/` with a fresh store under
`.cache/smoke/store`; every step exited 0.

| Step | Command                                             | Outcome                                                     |
| ---- | --------------------------------------------------- | ----------------------------------------------------------- |
| 1    | `validate` (all seven documents)                    | ok, 0 errors, 0 warnings                                    |
| 2    | `ingest`                                            | 8 of 8 evidence records appended, chain verified            |
| 3    | `locate --system SYS-COR --fault F-1001`            | best KP 127.996, window 127.072..128.920, consistent        |
| 4    | `assets --system SYS-COR --kp 128`                  | 2 vessels scored; the 2500 m vessel rejected on depth       |
| 5    | `window --vessel CS-AURORA --hours 36`              | 3 continuous windows, 1 observation gap, window found       |
| 6    | `plan --system SYS-COR --fault F-1001 --candidates` | feasible, CS-AURORA from DEP-AURIC, 8 stages, cost 555.940  |
| 7    | `campaign`                                          | 3 of 3 faults scheduled, 1 vessel contention, 2 depot draws |
| 8    | `verify`                                            | 3 records, 3 accepted, 0 rejected                           |
| 9    | `report`                                            | 4 ledger entries, 4 audit records, chain valid              |
| 10   | `locate --format json` twice                        | byte-identical output (determinism check)                   |

### Notable behaviour confirmed by the smoke run

- A three hour hole in the sea-state series splits an otherwise 118 hour calm
  stretch, so the planner defers the on-site window from 2026-03-12T03:06Z to
  2026-03-13T03:00Z and books 23.899 h of standby instead.
- The deep-water fault (4200 m) is only feasible for the vessel rated to 5000 m;
  all three pairings with the 2500 m vessel are reported infeasible with the
  reason `water_depth_exceeds_vessel_capability`.
- The permit for the mid-route zone authorizes four operations, and because the
  localization window contains no buried cable the plan requests exactly those
  four, omitting the final burial stage.
- In the campaign, the second vessel is released by its first repair before the
  third fault starts, which is recorded as an explicit contention entry, and the
  third assignment is flagged `restoration_deadline_missed`.
- The audit chain reports an informational note when a later ledger entry carries
  an earlier input-derived event time; chain validity depends only on the hash
  links, sequence numbers and ledger cross-check.

## Repository hygiene

- `.gitignore` excludes `.cache/` (Go build cache, module cache, temp dir, smoke
  artifacts and the smoke binary) and the default `.cablemend/` store.
- `.dockerignore` limits the build context to `go.mod`, `cmd/` and `internal/`.
- Git history: branch `main`, exactly one root commit containing everything,
  author and committer `GoMark Author <gomark@example.invalid>`, no remotes,
  clean working tree.
