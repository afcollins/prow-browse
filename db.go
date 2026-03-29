package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

func openDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			job       TEXT NOT NULL,
			run_id    TEXT NOT NULL,
			variant   TEXT NOT NULL DEFAULT '',
			fetched_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job, run_id)
		);

		CREATE TABLE IF NOT EXISTS steps (
			job       TEXT NOT NULL,
			run_id    TEXT NOT NULL,
			step_name TEXT NOT NULL,
			PRIMARY KEY (job, run_id, step_name),
			FOREIGN KEY (job, run_id) REFERENCES runs(job, run_id)
		);

		CREATE TABLE IF NOT EXISTS step_children (
			job        TEXT NOT NULL,
			run_id     TEXT NOT NULL,
			step_name  TEXT NOT NULL,
			child_name TEXT NOT NULL,
			PRIMARY KEY (job, run_id, step_name, child_name),
			FOREIGN KEY (job, run_id, step_name) REFERENCES steps(job, run_id, step_name)
		);

		CREATE INDEX IF NOT EXISTS idx_runs_job ON runs(job);
		CREATE INDEX IF NOT EXISTS idx_steps_run ON steps(job, run_id);
	`)
	return err
}

// StoreResults saves a batch of RunResults to the database.
func (d *DB) StoreResults(results []RunResult) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	runStmt, err := tx.Prepare(`INSERT OR REPLACE INTO runs (job, run_id, variant) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer runStmt.Close()

	stepStmt, err := tx.Prepare(`INSERT OR REPLACE INTO steps (job, run_id, step_name) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stepStmt.Close()

	childStmt, err := tx.Prepare(`INSERT OR REPLACE INTO step_children (job, run_id, step_name, child_name) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer childStmt.Close()

	for _, r := range results {
		if _, err := runStmt.Exec(r.Job, r.RunID, r.VariantID); err != nil {
			return fmt.Errorf("inserting run %s/%s: %w", r.Job, r.RunID, err)
		}
		for stepName := range r.Steps {
			if _, err := stepStmt.Exec(r.Job, r.RunID, stepName); err != nil {
				return fmt.Errorf("inserting step %s: %w", stepName, err)
			}
			if children, ok := r.StepDirs[stepName]; ok {
				for _, child := range children {
					if _, err := childStmt.Exec(r.Job, r.RunID, stepName, child); err != nil {
						return fmt.Errorf("inserting child %s/%s: %w", stepName, child, err)
					}
				}
			}
		}
	}

	return tx.Commit()
}

// SeenRuns returns the set of run IDs already stored for a given job.
func (d *DB) SeenRuns(job string) (map[string]bool, error) {
	rows, err := d.db.Query(`SELECT run_id FROM runs WHERE job = ?`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, err
		}
		seen[runID] = true
	}
	return seen, rows.Err()
}

// QueryResults loads RunResults from the database, filtered by an optional job substring.
// If limit > 0, only the N most recent runs per job are returned.
func (d *DB) QueryResults(jobFilter string, limit int) ([]RunResult, error) {
	// Build the query for runs
	query := `SELECT job, run_id, variant FROM runs`
	var args []interface{}

	if jobFilter != "" {
		query += ` WHERE job LIKE ?`
		args = append(args, "%"+jobFilter+"%")
	}

	query += ` ORDER BY job ASC, run_id DESC`

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect runs, applying per-job limit
	type runInfo struct {
		job, runID, variant string
	}
	var allRuns []runInfo
	jobRunCount := make(map[string]int)

	for rows.Next() {
		var ri runInfo
		if err := rows.Scan(&ri.job, &ri.runID, &ri.variant); err != nil {
			return nil, err
		}
		if limit > 0 {
			jobRunCount[ri.job]++
			if jobRunCount[ri.job] > limit {
				continue
			}
		}
		allRuns = append(allRuns, ri)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load steps and children for each run
	results := make([]RunResult, 0, len(allRuns))
	for _, ri := range allRuns {
		r := RunResult{
			Job:       ri.job,
			RunID:     ri.runID,
			VariantID: ri.variant,
			Steps:     make(map[string]bool),
			StepDirs:  make(map[string][]string),
		}

		stepRows, err := d.db.Query(
			`SELECT step_name FROM steps WHERE job = ? AND run_id = ?`,
			ri.job, ri.runID)
		if err != nil {
			return nil, err
		}
		for stepRows.Next() {
			var name string
			if err := stepRows.Scan(&name); err != nil {
				stepRows.Close()
				return nil, err
			}
			r.Steps[name] = true
		}
		stepRows.Close()

		childRows, err := d.db.Query(
			`SELECT step_name, child_name FROM step_children WHERE job = ? AND run_id = ?`,
			ri.job, ri.runID)
		if err != nil {
			return nil, err
		}
		for childRows.Next() {
			var stepName, childName string
			if err := childRows.Scan(&stepName, &childName); err != nil {
				childRows.Close()
				return nil, err
			}
			r.StepDirs[stepName] = append(r.StepDirs[stepName], childName)
		}
		childRows.Close()

		results = append(results, r)
	}

	return results, nil
}

// ResolveRunID resolves a partial run ID suffix (like a git short hash) to a unique (job, runID).
// Returns an error if the suffix matches zero or more than one run.
func (d *DB) ResolveRunID(suffix string) (job, runID string, err error) {
	rows, err := d.db.Query(`SELECT job, run_id FROM runs WHERE run_id LIKE '%' || ?`, suffix)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	var matches []struct{ job, runID string }
	for rows.Next() {
		var j, r string
		if err := rows.Scan(&j, &r); err != nil {
			return "", "", err
		}
		matches = append(matches, struct{ job, runID string }{j, r})
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}

	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("no run found matching suffix %q", suffix)
	case 1:
		return matches[0].job, matches[0].runID, nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.runID)
		}
		return "", "", fmt.Errorf("suffix %q is ambiguous, matches: %s", suffix, strings.Join(ids, ", "))
	}
}

// ListJobs returns distinct job names stored in the database, optionally filtered.
func (d *DB) ListJobs(jobFilter string) ([]string, error) {
	query := `SELECT DISTINCT job FROM runs`
	var args []interface{}
	if jobFilter != "" {
		query += ` WHERE job LIKE ?`
		args = append(args, "%"+jobFilter+"%")
	}
	query += ` ORDER BY job`

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []string
	for rows.Next() {
		var job string
		if err := rows.Scan(&job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// Stats returns summary counts.
func (d *DB) Stats() (jobs, runs, steps int, err error) {
	err = d.db.QueryRow(`SELECT COUNT(DISTINCT job) FROM runs`).Scan(&jobs)
	if err != nil {
		return
	}
	err = d.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs)
	if err != nil {
		return
	}
	err = d.db.QueryRow(`SELECT COUNT(*) FROM steps`).Scan(&steps)
	return
}

// RunSQL executes an arbitrary read-only query and prints the results as a table.
// Only SELECT statements are allowed.
func (d *DB) RunSQL(query string) ([][]string, []string, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(trimmed, "SELECT") {
		return nil, nil, fmt.Errorf("only SELECT queries are allowed")
	}

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	var results [][]string
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = fmt.Sprintf("%v", v)
		}
		results = append(results, row)
	}

	return results, cols, rows.Err()
}
