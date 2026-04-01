# prow-status

A CLI tool that provides a single-pane view of OpenShift CI (Prow) periodic job results stored in GCS. It shows which test steps exist for each job run in a color-coded terminal grid.

## Features

- Cobra subcommands: local display (default), `fetch` (GCS), `pull` (re-fetch by run ID suffix)
- SQLite-backed local database for offline slicing and ad-hoc SQL queries
- Two renderers: ANSI emoji grid (default) and lipgloss table (`-t`)
- Platform grouping (`-g`): separates AWS, ROSA, ROSA HCP, vSphere, Metal, etc.
- Two-level grouping for loaded-upgrade and metal jobs (by platform/sub-config)
- Steps ordered by CI execution sequence; gather steps pushed to bottom
- Concurrent GCS API calls with progress logging and call counter (`-v`)

## Usage

```bash
# Build
make build

# Fetch new runs from GCS and display
./prow-status fetch

# Fetch with filters
./prow-status fetch -j "control-plane-120nodes" -d 10

# Re-fetch all (including previously seen)
./prow-status fetch --all

# Re-fetch a specific run by ID suffix
./prow-status pull 2038435361289408512

# Display from local database
./prow-status -j "aws-4.22" -l 5

# Group by platform, use table rendering
./prow-status -g -t

# Show most recent N runs
./prow-status -n 10

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
| `max_runs_per_job` | Default max runs per job for local display |
| `concurrency` | Max parallel GCS API calls |

## Grid output

- **Rows**: step directories ordered by CI execution sequence
- **Columns**: emoji-labeled job:run pairs (legend printed above the grid)
- **Cells**: `✅` = step exists, `❌` = step missing (expected), `..` = optional/gather step, `──` = not applicable for this job type

## Prerequisites

- Go 1.21+
- GCP application default credentials (`gcloud auth application-default login`)
- Read access to the target GCS bucket
