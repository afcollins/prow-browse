# prow-browse

A CLI tool that provides a single-pane view of OpenShift CI (Prow) periodic job results stored in GCS. It shows step results (SUCCESS/FAILURE) for each job run in a color-coded terminal grid.

## Features

- Two-phase workflow: `fetch` (discover run IDs) → `pull` (fetch step data from `finished.json`)
- SQLite-backed local database for offline slicing and ad-hoc SQL queries
- Two renderers: ANSI emoji grid (default) and lipgloss table (`-t`)
- Platform grouping (`-g`): separates AWS, ROSA, ROSA HCP, vSphere, Metal, etc.
- Two-level grouping for loaded-upgrade and metal jobs (by platform/sub-config)
- Steps ordered by CI execution sequence; gather steps pushed to bottom
- GCS web URLs in legend (`-u`) for quick access to run artifacts
- Interactive artifact browser (`browse`) with bubbletea TUI — lazy GCS expansion, search, batch download
- Browse arbitrary GCS paths (`browse --path`) — supports PR logs, gcsweb URLs, gs:// URLs
- Concurrent GCS API calls with progress logging and call counter (`-v`)
- GCS call logging to `~/.local/share/prow-browse/gcs.log` with per-call timing

## Usage

```bash
# Build and test
make build
make test

# Discover run IDs from GCS (lightweight, no artifact traversal)
./pb fetch
./pb fetch -j "control-plane-120nodes" -n 10
./pb fetch --all

# Pull step data (reads finished.json for each step)
./pb pull -n 5                  # latest 5 unpulled runs
./pb pull -n 3 -j "aws"         # latest 3 unpulled aws runs
./pb pull 2038435361289408512   # force re-pull specific run (GCS fallback if not in DB)

# Display from local database
./pb -j "aws-4.22" -n 5
./pb -g -t                      # group by platform, table rendering
./pb -j "aws" -n 3 -u           # show gcsweb URLs in legend

# Database introspection
./pb --stats
./pb --query "SELECT job, count(*) FROM runs GROUP BY job"

# Interactive artifact browser
./pb browse 9408512             # browse by run ID (auto-pulls if needed)
./pb browse -p pr-logs/pull/... # browse arbitrary GCS path
./pb browse -p https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/pr-logs/...

# Debug logging
./pb fetch -v

# GCS call log (written automatically on any GCS operation)
cat ~/.local/share/prow-browse/gcs.log
```

### Browse keys

| Key | Action |
|-----|--------|
| ↑/↓, j/k | Navigate |
| →/Enter | Expand directory (lazy GCS listing) |
| ← | Collapse directory (or jump to parent) |
| Space | Toggle file checkbox |
| / | Search by name |
| n/N | Next/previous search match |
| d | Download checked files |
| PgUp/PgDn | Scroll page |
| g/G | Jump to top/bottom |
| Ctrl+Z | Suspend to shell (`fg` to resume) |
| q/Esc | Quit |

Files download to `~/Downloads/prow/` mirroring the full GCS bucket path. Already-downloaded files are skipped on subsequent downloads. Override with `-o`/`--output`.

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
| `download_dir` | Default download directory for `browse` (default: `~/Downloads/prow`) |

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
