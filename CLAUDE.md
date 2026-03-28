# CLAUDE.md

## Project overview

CLI tool (Go) that queries a GCS bucket containing OpenShift CI (Prow) periodic job results and displays a status grid showing which test steps exist for each job run.

## Build and run

```bash
go build -o prow-status .
./prow-status --all --jobs "control-plane-120nodes"
```

## Architecture

Single-binary Go CLI, all source in the root package:

- `main.go` - Entry point, CLI flags, orchestration (list jobs -> list runs -> list steps -> display)
- `gcs.go` - GCS operations via `cloud.google.com/go/storage` SDK. Uses delimiter-based listing for efficient non-recursive directory enumeration.
- `state.go` - JSON state file persistence (`state.json`). Tracks seen run IDs per job.
- `display.go` - Terminal grid rendering with ANSI color codes. Legend + numbered columns.
- `config.go` - JSON config loading from `config.json`.

## Key design decisions

- Uses Go GCS SDK (not `gcloud` CLI) for performance with hundreds of concurrent listings
- Concurrency controlled by semaphore channel (`cfg.Concurrency`)
- "Variant" directory auto-detected: first subdir under `artifacts/` not in `ignore_artifact_dirs`
- No-recurse steps: listed one level deep only, shown in detail section below the grid
- Step applicability heuristic: steps in <30% of results shown as `──` (not applicable) rather than `❌` (missing)

## GCS bucket structure

```
gs://{bucket}/{prefix}/{job-name}/{run-id}/
  artifacts/
    {variant}/           # e.g., "control-plane-120nodes"
      {step-name}/       # e.g., "ipi-install-install"
        build-log.txt
        finished.json
        artifacts/       # step-specific artifacts
```
