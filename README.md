# prow-status

A CLI tool that provides a single-pane view of OpenShift CI (Prow) periodic job results stored in GCS. It shows which test steps exist for each job run in a color-coded terminal grid.

## Features

- Lists periodic perfscale jobs from a GCS bucket
- Tracks seen runs in a local state file, only showing new runs by default
- Displays a grid of step directories (rows) vs job:run (columns) with green/red status
- Handles "no-recurse" steps (e.g., `gather-extra`) by listing immediate children without descending
- Uses the Go GCS SDK for efficient parallel API calls

## Usage

```bash
# Build
go build -o prow-status .

# Show only new runs since last check
./prow-status

# Show all recent runs (ignores state)
./prow-status --all

# Filter to specific job patterns
./prow-status --jobs "control-plane-120nodes"
./prow-status --all --jobs "data-path"
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.json` | Path to config file |
| `--state` | `state.json` | Path to state file (tracks seen runs) |
| `--all` | `false` | Show all recent runs, not just new ones |
| `--jobs` | `""` | Additional substring filter for job names |

## Configuration

Edit `config.json`:

```json
{
    "bucket": "test-platform-results",
    "prefix": "logs",
    "job_pattern": "periodic-ci-openshift-eng-ocp-qe-perfscale",
    "no_recurse_steps": ["gather-extra", "gather-must-gather", "gather-audit-logs", "gather-aws-console"],
    "ignore_artifact_dirs": ["build-resources", "release"],
    "max_runs_per_job": 3,
    "concurrency": 20
}
```

| Field | Description |
|-------|-------------|
| `bucket` | GCS bucket name (without `gs://`) |
| `prefix` | Path prefix within the bucket |
| `job_pattern` | Prefix to match job directory names |
| `no_recurse_steps` | Step directories to list but not recurse into |
| `ignore_artifact_dirs` | Directories under `artifacts/` to skip (not variant dirs) |
| `max_runs_per_job` | How many recent runs to check per job |
| `concurrency` | Max parallel GCS API calls |

## Grid output

- **Rows**: step directories found under `artifacts/{variant}/`
- **Columns**: numbered job:run pairs (legend printed above the grid)
- **Cells**: `✅` = step exists, `❌` = step missing (expected), `──` = not applicable for this job type

## Prerequisites

- Go 1.21+
- GCP application default credentials (`gcloud auth application-default login`)
- Read access to the target GCS bucket
