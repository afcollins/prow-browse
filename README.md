# prow-status

A CLI tool that provides a single-pane view of OpenShift CI (Prow) periodic job results stored in GCS. It shows step results (SUCCESS/FAILURE) for each job run in a color-coded terminal grid.

## Features

- Two-phase workflow: `fetch` (discover run IDs) → `pull` (fetch step data from `finished.json`)
- SQLite-backed local database for offline slicing and ad-hoc SQL queries
- Two renderers: ANSI emoji grid (default) and lipgloss table (`-t`)
- Platform grouping (`-g`): separates AWS, ROSA, ROSA HCP, vSphere, Metal, etc.
- Two-level grouping for loaded-upgrade and metal jobs (by platform/sub-config)
- Steps ordered by CI execution sequence; gather steps pushed to bottom
- Concurrent GCS API calls with progress logging and call counter (`-v`)

## Usage

```bash
# Build and test
make build
make test

# Discover run IDs from GCS (lightweight, no artifact traversal)
./prow-status fetch
./prow-status fetch -j "control-plane-120nodes" -n 10
./prow-status fetch --all

# Pull step data (reads finished.json for each step)
./prow-status pull -n 5                  # latest 5 unpulled runs
./prow-status pull -n 3 -j "aws"         # latest 3 unpulled aws runs
./prow-status pull 2038435361289408512   # force re-pull specific run (GCS fallback if not in DB)

# Display from local database
./prow-status -j "aws-4.22" -n 5
./prow-status -g -t                      # group by platform, table rendering

# Database introspection
./prow-status --stats
./prow-status --query "SELECT job, count(*) FROM runs GROUP BY job"

# Debug logging
./prow-status fetch -v
```

## Configuration

Edit `config.json`:

| Field | Description |
|-------|-------------|
| `bucket` | GCS bucket name |
| `prefix` | Path prefix within the bucket |
| `job_pattern` | Prefix to match job directory names |
| `no_recurse_steps` | Steps to list but not recurse into (shown as `..` when absent) |
| `optional_steps` | Steps shown as `..` when absent (e.g., ovn-conf) |
| `ignore_artifact_dirs` | Directories under `artifacts/` to skip during variant detection |
| `step_order` | Ordered list of step names matching CI execution sequence |
| `columns_per_page` | Max columns before paginating (default: 50) |
| `max_runs_per_job` | Default max runs for `pull` when `-n` not specified |
| `concurrency` | Max parallel GCS API calls |

## Grid output

- **Rows**: step directories ordered by CI execution sequence
- **Columns**: emoji-labeled job:run pairs (legend printed above the grid)
- **Cells**:
  - `✅` / `OK` = step ran and succeeded
  - `❌` / `FAIL` = step ran and failed (or expected step missing)
  - `👻` / `UNWN` = step exists but no `finished.json`
  - `❔` / `PULL` = run not yet pulled from GCS
  - `..` = optional/gather step not present
  - `──` / `--` = not applicable for this job type

## Prerequisites

- Go 1.21+
- GCP application default credentials (`gcloud auth application-default login`)
- Read access to the target GCS bucket
