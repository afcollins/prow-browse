# CLAUDE.md

## Project overview

CLI tool (Go) that queries a GCS bucket containing OpenShift CI (Prow) periodic job results and displays a status grid showing which test steps exist for each job run.

## Build and run

```bash
go build -o prow-status .
./prow-status --all --jobs "control-plane-120nodes"
./prow-status --local --jobs "aws-4.22" --limit 5
```

## Architecture

Single-binary Go CLI, all source in the root package:

- `main.go` - Entry point, CLI flags, orchestration (list jobs -> list runs -> list steps -> display). Supports online (GCS fetch) and offline (`--local`) modes.
- `gcs.go` - GCS operations via `cloud.google.com/go/storage` SDK. Uses delimiter-based listing for efficient non-recursive directory enumeration.
- `db.go` - SQLite storage via `modernc.org/sqlite` (pure Go). Stores runs, steps, and step children. Replaces the old JSON state file. Supports `--query` for ad-hoc SQL and `--stats`.
- `display.go` - Terminal grid rendering with ANSI color codes and emoji column headers. Randomly assigns emojis from a configurable palette each run.
- `config.go` - JSON config loading from `config.json`.

## Key design decisions

- Uses Go GCS SDK (not `gcloud` CLI) for performance with hundreds of concurrent listings
- Concurrency controlled by semaphore channel (`cfg.Concurrency`)
- SQLite stores all fetched data for offline slicing with `--local` and `--jobs` filters
- "Variant" directory auto-detected: first subdir under `artifacts/` not in `ignore_artifact_dirs`
- No-recurse steps (gather-*): shown as dim `..` when absent since they only run on failure
- Step applicability heuristic: steps in <30% of results shown as `──` (not applicable) rather than `❌` (missing)
- Steps ordered by CI execution sequence via `step_order` in config (derived from build-log.txt)
- Column emojis randomly shuffled each run; palette configurable (`"default"`, `"fruits"`)
- Logging via `log/slog` structured logger to stderr

## Database schema

```sql
runs(job, run_id, variant, fetched_at)
steps(job, run_id, step_name)
step_children(job, run_id, step_name, child_name)
```

## GCS bucket structure

```
gs://{bucket}/{prefix}/{job-name}/{run-id}/
  build-log.txt          # step execution log (has step order)
  artifacts/
    {variant}/           # e.g., "control-plane-120nodes"
      {step-name}/       # e.g., "ipi-install-install"
        build-log.txt
        finished.json
        artifacts/       # step-specific artifacts
```
