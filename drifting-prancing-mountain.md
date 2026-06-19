# Plan: Three Independent Tasks

Ordered by dependency. Each is a standalone commit/PR.

---

## Task 1: Folder Download in Browse (independent)

**Files:** `browse.go`, `browse_gcs.go`

### Visual
Dirs show both expand arrow AND checkbox:
```
▾ [x] dirname/    (expanded, checked)
▸ [ ] dirname/    (collapsed, unchecked)
  [x] file.json   (file, checked)
```

### Cascade behavior
- Check dir → all children recursively checked
- Uncheck dir → all children recursively unchecked
- Expand/collapse (Enter/Right/Left) unchanged, independent of check state

### Download
- `d` key: for checked dirs, recursively list all GCS objects under prefix (non-delimiter listing), download each
- Status bar: "Downloading 12/47 files..."
- Already-downloaded files skipped via existing `downloaded` map

### Changes
- `browse.go` View (~line 414): dir rendering adds checkbox between arrow and name
- `browse.go` Update (~line 283): remove `!node.isDir` guard on space/x; cascade toggle walks children
- `browse.go` checkedFiles (~line 463): return dir nodes too (download handles recursive listing)
- `browse.go` downloadFiles (~line 498): detect dir items, call `downloadDir()`
- `browse_gcs.go`: new `downloadDir()` — non-delimiter GCS listing → concurrent `downloadObject()` calls

### Verify
- Browse a pulled run, check a dir, press `d` — all files under it download
- Check/uncheck cascades to visible children
- Dirs still expand/collapse normally

---

## Task 2: Job Sources (foundational config + GCS + DB)

**Files:** `config.go`, `gcs.go`, `db.go`, `main.go`

### Config (`config.go`)
```go
type JobSource struct {
    Name string `json:"name"`
    Path string `json:"path"` // bucket-relative, e.g. "logs/periodic-ci-openshift-eng-..."
}
```
- Add `Sources []JobSource` to Config
- Keep `Prefix`/`JobPattern` for backward compat
- `loadConfig()`: if Sources empty but old fields set, synthesize single source

### GCS (`gcs.go`)
- `listJobs()`: iterate sources, each source's `Path` = GCS list prefix
- `listRuns()`, `listSteps()`: take `sourcePrefix string` param instead of reading `g.cfg.Prefix`

### DB (`db.go`)
- Add `source TEXT DEFAULT ''` column to `runs` table (migration)
- `StoreRuns`/`StoreResults`: accept and store source name
- Queries: expose source field so `pull` can reconstruct GCS paths

### main.go
- `fetch`: loop sources, list jobs per source, tag runs
- `pull`: look up run's source from DB to build correct GCS prefix
- `findRunByID`: search across all sources

### Verify
- Existing config with `prefix`/`job_pattern` still works
- Add source manually to config.json, `pb fetch` discovers jobs from both sources
- `pb pull` pulls from correct paths per source
- `make test && make lint` pass

---

## Task 3: CLI Config (depends on Task 2)

**Files:** `config_cmd.go` (new), `main.go`

### Commands
```
pb config list                          # pretty-print full config as JSON
pb config get <key>                     # print scalar value
pb config set <key> <value>             # set scalar value
pb config sources                       # list sources as table
pb config sources add NAME PATH         # add source (accepts gs:// URLs)
pb config sources remove NAME           # remove source by name
```

### Implementation — new `config_cmd.go`
- `list`: loadConfig → json.MarshalIndent → print
- `get`/`set`: switch on key for scalars: `bucket`, `concurrency`, `max_runs_per_job`, `columns_per_page`, `download_dir`, `emoji_palette`
- `sources`: tabwriter table (NAME | PATH)
- `sources add`: parse gs:// URL or raw path, append, write config
- `sources remove`: filter by name, write config
- Write-back: read raw JSON → unmarshal to `map[string]any` → modify → marshal. Preserves unknown fields.
- Wire `configCmd` into cobra root in `main.go`

### Verify
- `pb config list` shows current config
- `pb config sources add etcd-pr "pr-logs/pull/openshift_release/78517/rehearse-78517-..."` adds source
- `pb config sources` shows table
- `pb config set concurrency 10` updates value
- `pb config sources remove etcd-pr` removes it
