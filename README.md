# prow-status

A CLI tool that provides a single-pane view of OpenShift CI (Prow) periodic job results stored in GCS. It shows which test steps exist for each job run in a color-coded terminal grid.

## Features

- Lists periodic perfscale jobs from a GCS bucket
- Stores all fetched data in a local SQLite database for offline slicing
- Displays a grid of step directories (rows) vs job:run (columns) with emoji status indicators
- Steps are ordered by execution sequence (configurable via `step_order`)
- Each column gets a unique randomly-assigned emoji for easy visual tracking
- Gather/optional steps shown as `..` instead of failures
- Handles "no-recurse" steps (e.g., `gather-extra`) by listing immediate children without descending
- Uses the Go GCS SDK for efficient parallel API calls

## Usage

```bash
# Build
go build -o prow-status .

# Fetch new runs from GCS and display
./prow-status

# Show all recent runs (re-fetch even if previously seen)
./prow-status --all

# Filter to specific job patterns
./prow-status --jobs "control-plane-120nodes"

# Display from local database only (no GCS calls)
./prow-status --local --jobs "120nodes"
./prow-status --local --jobs "aws-4.22" --limit 5

# Show database statistics
./prow-status --stats

# Run a SQL query against the local database
./prow-status --query "SELECT job, count(*) FROM runs GROUP BY job"
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.json` | Path to config file |
| `--db` | `prow-status.db` | SQLite database path |
| `--all` | `false` | Show all recent runs, not just new ones |
| `--local` | `false` | Display from local database only, no GCS fetch |
| `--jobs` | `""` | Filter job names by substring |
| `--limit` | `0` | Max runs per job to display (0 = use config default) |
| `--stats` | `false` | Show database statistics |
| `--query` | `""` | Run a SQL query against the local database |

## Configuration

Edit `config.json`:

```json
{
    "bucket": "test-platform-results",
    "prefix": "logs",
    "job_pattern": "periodic-ci-openshift-eng-ocp-qe-perfscale",
    "no_recurse_steps": ["gather-extra", "gather-must-gather", ...],
    "ignore_artifact_dirs": ["build-resources", "release"],
    "step_order": ["ipi-conf", "ipi-conf-telemetry", "ipi-conf-aws", ...],
    "emoji_palette": "default",
    "max_runs_per_job": 3,
    "concurrency": 20
}
```

| Field | Description |
|-------|-------------|
| `bucket` | GCS bucket name (without `gs://`) |
| `prefix` | Path prefix within the bucket |
| `job_pattern` | Prefix to match job directory names |
| `no_recurse_steps` | Step dirs to list but not recurse into (shown as `..` when absent) |
| `ignore_artifact_dirs` | Directories under `artifacts/` to skip (not variant dirs) |
| `step_order` | Ordered list of step names matching CI execution sequence |
| `emoji_palette` | Emoji set for column headers: `"default"` (64 emojis) or `"fruits"` (16) |
| `max_runs_per_job` | How many recent runs to check per job |
| `concurrency` | Max parallel GCS API calls |

## Grid output

- **Rows**: step directories ordered by CI execution sequence
- **Columns**: emoji-labeled job:run pairs (legend printed above the grid)
- **Cells**: `✅` = step exists, `❌` = step missing (expected), `..` = optional/gather step, `──` = not applicable for this job type

## Prerequisites

- Go 1.21+
- GCP application default credentials (`gcloud auth application-default login`)
- Read access to the target GCS bucket
