# CLAUDE.md

## Project overview

CLI tool (Go) that queries a GCS bucket containing OpenShift CI (Prow) periodic job results and displays a status grid showing which test steps exist for each job run.

## Build and run

```bash
make build
./prow-status -j "aws-4.22" -l 5        # display from local DB
./prow-status fetch -j "120nodes"        # fetch from GCS and display
./prow-status pull 2038435361289408512   # re-fetch specific run
```

## Architecture

Single-binary Go CLI using cobra subcommands, all source in the root package:

- `main.go` - Entry point, cobra commands: root (local display), `fetch` (GCS), `pull` (re-fetch by ID suffix)
- `gcs.go` - GCS operations with atomic API call counter
- `db.go` - SQLite storage (modernc.org/sqlite). Delete-then-insert on re-fetch
- `display.go` - Grid orchestration, platform/job-type classification, ANSI raw renderer
- `display_table.go` - Lipgloss v2 table renderer (`-t` flag)
- `config.go` - JSON config loading
- `Makefile` - Build with `-s -w` ldflags

## Key design decisions

- Go GCS SDK with semaphore-controlled concurrency (`cfg.Concurrency`)
- SQLite for offline querying; logrus logging to stderr (`-v` for debug)
- Two-level platform grouping (`-g`): job type (loaded-upgrade, metal) then platform (AWS, ROSA, vSphere, etc.)
- Job classification by job name keywords; step filtering by keyword anywhere in name
- Metal sub-groups: daily-virt, weekly-telco-core-cpt, weekly-eip, weekly, udn-bgp
- Standalone platforms: Metal RHOSO, Baremetal Multi, NetObserv (checked before generic metal)
- No-recurse/gather steps pushed to bottom of page, shown as `..` when absent
- Step applicability heuristic: <30% occurrence = `──` (not applicable) vs `❌` (missing)
- Empty step rows skipped; pagination via `columns_per_page` config

## GCS bucket structure

```
gs://{bucket}/{prefix}/{job-name}/{run-id}/artifacts/{variant}/{step-name}/
```

Variant auto-detected as first subdir under `artifacts/` not in `ignore_artifact_dirs`.
