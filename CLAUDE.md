# CLAUDE.md

## Project overview

CLI tool (Go) that queries a GCS bucket containing OpenShift CI (Prow) periodic job results and displays a status grid showing step results (SUCCESS/FAILURE) for each job run.

## Build and run

```bash
make build && make test
./prow-status -j "aws-4.22" -n 5        # display from local DB
./prow-status fetch -j "120nodes" -n 10  # discover run IDs from GCS
./prow-status pull -n 3 -j "aws"         # pull step data for latest unpulled
./prow-status pull 2038435361289408512   # force re-pull specific run
```

## Architecture

Single-binary Go CLI using cobra subcommands, all source in the root package:

- `main.go` - Entry point, cobra commands: root (local display), `fetch` (discovery), `pull` (artifact traversal)
- `gcs.go` - GCS operations: recursive listing + concurrent `finished.json` reads, slog-based call logging to `~/.local/share/prow-status/gcs.log`
- `db.go` - SQLite storage (modernc.org/sqlite). Steps table stores result (SUCCESS/FAILURE/UNKNOWN). Schema auto-migrates.
- `display.go` - Grid orchestration, platform/job-type classification, ANSI raw renderer
- `display_table.go` - Lipgloss v2 table renderer (`-t` flag)
- `config.go` - JSON config loading
- `Makefile` - Build with `-s -w` ldflags, `make test`

## Key design decisions

- Two-phase workflow: `fetch` discovers run IDs (lightweight), `pull` traverses artifacts and reads `finished.json`
- `pull -n N` skips runs already pulled; explicit run IDs always force re-pull
- GCS fallback: `pull <id>` searches all jobs if ID not in local DB
- Go GCS SDK with semaphore-controlled concurrency (`cfg.Concurrency`)
- SQLite for offline querying; logrus logging to stderr (`-v` for debug); slog file logging for GCS calls
- Two-level platform grouping (`-g`): job type (loaded-upgrade, metal) then platform (AWS, ROSA, vSphere, etc.)
- Job classification by job name keywords; step filtering by earliest-keyword-wins
- Metal sub-groups: daily-virt, weekly-telco-core-cpt, weekly-eip, weekly, udn-bgp
- Standalone platforms: Metal RHOSO, Baremetal Multi, NetObserv (checked before generic metal)
- Cell states: ✅=success, ❌=failure, 👻=unknown, ❔=not pulled, `..`=optional, `──`=n/a
- No-recurse/gather steps pushed to bottom of page
- Empty step rows skipped; pagination via `columns_per_page` config

## GCS bucket structure

```
gs://{bucket}/{prefix}/{job-name}/{run-id}/artifacts/{variant}/{step-name}/finished.json
```

Variant auto-detected as first subdir under `artifacts/` not in `ignore_artifact_dirs`.
