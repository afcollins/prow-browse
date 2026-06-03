package main

import (
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatalf("failed to open test DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStoreAndQueryResults(t *testing.T) {
	db := newTestDB(t)

	results := []RunResult{
		{
			Job: "job-a", RunID: "100", VariantID: "variant-a",
			Steps:    map[string]StepResult{"step1": StepSuccess, "step2": StepSuccess},
			StepDirs: map[string][]string{"step1": {"child1", "child2"}},
		},
		{
			Job: "job-a", RunID: "200", VariantID: "variant-a",
			Steps:    map[string]StepResult{"step1": StepSuccess},
			StepDirs: map[string][]string{},
		},
		{
			Job: "job-b", RunID: "300", VariantID: "variant-b",
			Steps:    map[string]StepResult{"step3": StepSuccess},
			StepDirs: map[string][]string{},
		},
	}

	if err := db.StoreResults(results); err != nil {
		t.Fatalf("StoreResults: %v", err)
	}

	t.Run("query all", func(t *testing.T) {
		got, err := db.QueryResults("")
		if err != nil {
			t.Fatalf("QueryResults: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d results, want 3", len(got))
		}
	})

	t.Run("query with job filter", func(t *testing.T) {
		got, err := db.QueryResults("job-a")
		if err != nil {
			t.Fatalf("QueryResults: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
	})

	t.Run("steps loaded correctly", func(t *testing.T) {
		got, err := db.QueryResults("job-a")
		if err != nil {
			t.Fatalf("QueryResults: %v", err)
		}
		// Results are ordered run_id DESC, so 200 first
		if _, exists := got[0].Steps["step1"]; !exists {
			t.Error("run 200 should have step1")
		}
		if _, exists := got[0].Steps["step2"]; exists {
			t.Error("run 200 should not have step2")
		}
		if _, e1 := got[1].Steps["step1"]; !e1 {
			t.Error("run 100 should have step1")
		}
		if _, e2 := got[1].Steps["step2"]; !e2 {
			t.Error("run 100 should have step2")
		}
	})

	t.Run("step children loaded", func(t *testing.T) {
		got, err := db.QueryResults("job-a")
		if err != nil {
			t.Fatalf("QueryResults: %v", err)
		}
		// run 100 is second (sorted DESC by run_id)
		children := got[1].StepDirs["step1"]
		if len(children) != 2 {
			t.Errorf("step1 children: got %d, want 2", len(children))
		}
	})
}

func TestStoreResultsReplacesStaleData(t *testing.T) {
	db := newTestDB(t)

	// Store initial data with 2 steps
	initial := []RunResult{{
		Job: "job-a", RunID: "100", VariantID: "v",
		Steps:    map[string]StepResult{"step1": StepSuccess, "step2": StepSuccess},
		StepDirs: map[string][]string{},
	}}
	if err := db.StoreResults(initial); err != nil {
		t.Fatalf("StoreResults initial: %v", err)
	}

	// Re-store with only 1 step (simulating re-fetch with updated data)
	updated := []RunResult{{
		Job: "job-a", RunID: "100", VariantID: "v",
		Steps:    map[string]StepResult{"step1": StepSuccess},
		StepDirs: map[string][]string{},
	}}
	if err := db.StoreResults(updated); err != nil {
		t.Fatalf("StoreResults updated: %v", err)
	}

	got, err := db.QueryResults("job-a")
	if err != nil {
		t.Fatalf("QueryResults: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if _, exists := got[0].Steps["step2"]; exists {
		t.Error("step2 should have been removed on re-store")
	}
	if _, exists := got[0].Steps["step1"]; !exists {
		t.Error("step1 should still exist")
	}
}

func TestQueryResultsSetsPulledFlag(t *testing.T) {
	db := newTestDB(t)

	// Store a run without steps (via StoreRuns) and one with steps (via StoreResults)
	if err := db.StoreRuns([]RunResult{
		{Job: "job-a", RunID: "100"},
		{Job: "job-a", RunID: "200"},
	}); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}
	if err := db.StoreResults([]RunResult{{
		Job: "job-a", RunID: "200", VariantID: "v",
		Steps:    map[string]StepResult{"step1": StepSuccess},
		StepDirs: map[string][]string{},
	}}); err != nil {
		t.Fatalf("StoreResults: %v", err)
	}

	got, err := db.QueryResults("job-a")
	if err != nil {
		t.Fatalf("QueryResults: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}

	// Results are ordered by run_id DESC: 200 first, 100 second
	if !got[0].Pulled {
		t.Error("run 200 (has steps) should have Pulled = true")
	}
	if got[1].Pulled {
		t.Error("run 100 (no steps) should have Pulled = false")
	}
}

func TestStoreRuns(t *testing.T) {
	db := newTestDB(t)

	runs := []RunResult{
		{Job: "job-a", RunID: "100"},
		{Job: "job-a", RunID: "200"},
		{Job: "job-b", RunID: "300"},
	}
	if err := db.StoreRuns(runs); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}

	t.Run("runs stored", func(t *testing.T) {
		seen, err := db.SeenRuns("job-a")
		if err != nil {
			t.Fatalf("SeenRuns: %v", err)
		}
		if !seen["100"] || !seen["200"] {
			t.Error("expected runs 100 and 200 to be seen")
		}
	})

	t.Run("duplicate ignored", func(t *testing.T) {
		dup := []RunResult{{Job: "job-a", RunID: "100"}}
		if err := db.StoreRuns(dup); err != nil {
			t.Fatalf("StoreRuns duplicate: %v", err)
		}
		// Should not error, just ignore
		got, err := db.QueryResults("job-a")
		if err != nil {
			t.Fatalf("QueryResults: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d results, want 2", len(got))
		}
	})
}

func TestQueryRunsWithoutSteps(t *testing.T) {
	db := newTestDB(t)

	// Store some runs without steps (via StoreRuns)
	if err := db.StoreRuns([]RunResult{
		{Job: "job-a", RunID: "100"},
		{Job: "job-a", RunID: "200"},
		{Job: "job-b", RunID: "300"},
	}); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}

	// Store one run with steps (via StoreResults)
	if err := db.StoreResults([]RunResult{{
		Job: "job-a", RunID: "200", VariantID: "v",
		Steps:    map[string]StepResult{"step1": StepSuccess},
		StepDirs: map[string][]string{},
	}}); err != nil {
		t.Fatalf("StoreResults: %v", err)
	}

	t.Run("all unpulled", func(t *testing.T) {
		got, err := db.QueryRunsWithoutSteps("", 0)
		if err != nil {
			t.Fatalf("QueryRunsWithoutSteps: %v", err)
		}
		// 100 and 300 have no steps; 200 has steps
		if len(got) != 2 {
			t.Fatalf("got %d unpulled runs, want 2", len(got))
		}
	})

	t.Run("filtered by job", func(t *testing.T) {
		got, err := db.QueryRunsWithoutSteps("job-a", 0)
		if err != nil {
			t.Fatalf("QueryRunsWithoutSteps: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d unpulled runs, want 1", len(got))
		}
		if got[0].RunID != "100" {
			t.Errorf("expected run 100, got %s", got[0].RunID)
		}
	})

	t.Run("with limit", func(t *testing.T) {
		got, err := db.QueryRunsWithoutSteps("", 1)
		if err != nil {
			t.Fatalf("QueryRunsWithoutSteps: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d unpulled runs, want 1", len(got))
		}
		// Should be most recent first (300 > 100)
		if got[0].RunID != "300" {
			t.Errorf("expected run 300 (most recent), got %s", got[0].RunID)
		}
	})
}

func TestSeenRuns(t *testing.T) {
	db := newTestDB(t)

	if err := db.StoreRuns([]RunResult{
		{Job: "job-a", RunID: "100"},
		{Job: "job-a", RunID: "200"},
	}); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}

	t.Run("known job", func(t *testing.T) {
		seen, err := db.SeenRuns("job-a")
		if err != nil {
			t.Fatalf("SeenRuns: %v", err)
		}
		if len(seen) != 2 {
			t.Fatalf("got %d seen runs, want 2", len(seen))
		}
	})

	t.Run("unknown job", func(t *testing.T) {
		seen, err := db.SeenRuns("job-x")
		if err != nil {
			t.Fatalf("SeenRuns: %v", err)
		}
		if len(seen) != 0 {
			t.Fatalf("got %d seen runs, want 0", len(seen))
		}
	})
}

func TestResolveRunID(t *testing.T) {
	db := newTestDB(t)

	if err := db.StoreRuns([]RunResult{
		{Job: "job-a", RunID: "1234567890"},
		{Job: "job-b", RunID: "9876543210"},
		{Job: "job-c", RunID: "1234999890"},
	}); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}

	t.Run("exact match", func(t *testing.T) {
		job, runID, err := db.ResolveRunID("1234567890")
		if err != nil {
			t.Fatalf("ResolveRunID: %v", err)
		}
		if job != "job-a" || runID != "1234567890" {
			t.Errorf("got (%q, %q), want (job-a, 1234567890)", job, runID)
		}
	})

	t.Run("suffix match", func(t *testing.T) {
		job, runID, err := db.ResolveRunID("543210")
		if err != nil {
			t.Fatalf("ResolveRunID: %v", err)
		}
		if job != "job-b" || runID != "9876543210" {
			t.Errorf("got (%q, %q), want (job-b, 9876543210)", job, runID)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, _, err := db.ResolveRunID("0000000000")
		if err == nil {
			t.Error("expected error for no match")
		}
	})

	t.Run("ambiguous match", func(t *testing.T) {
		// Both 1234567890 and 1234999890 end in "890"
		_, _, err := db.ResolveRunID("890")
		if err == nil {
			t.Error("expected error for ambiguous match")
		}
	})
}

func TestStats(t *testing.T) {
	db := newTestDB(t)

	t.Run("empty db", func(t *testing.T) {
		jobs, runs, steps, err := db.Stats()
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if jobs != 0 || runs != 0 || steps != 0 {
			t.Errorf("empty db stats: got (%d, %d, %d), want (0, 0, 0)", jobs, runs, steps)
		}
	})

	if err := db.StoreResults([]RunResult{
		{Job: "job-a", RunID: "100", Steps: map[string]StepResult{"s1": StepSuccess, "s2": StepSuccess}, StepDirs: map[string][]string{}},
		{Job: "job-a", RunID: "200", Steps: map[string]StepResult{"s1": StepSuccess}, StepDirs: map[string][]string{}},
		{Job: "job-b", RunID: "300", Steps: map[string]StepResult{"s3": StepSuccess}, StepDirs: map[string][]string{}},
	}); err != nil {
		t.Fatalf("StoreResults: %v", err)
	}

	t.Run("populated db", func(t *testing.T) {
		jobs, runs, steps, err := db.Stats()
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if jobs != 2 {
			t.Errorf("jobs = %d, want 2", jobs)
		}
		if runs != 3 {
			t.Errorf("runs = %d, want 3", runs)
		}
		if steps != 4 {
			t.Errorf("steps = %d, want 4", steps)
		}
	})
}

func TestListJobs(t *testing.T) {
	db := newTestDB(t)

	if err := db.StoreRuns([]RunResult{
		{Job: "job-b", RunID: "100"},
		{Job: "job-a", RunID: "200"},
		{Job: "job-a", RunID: "300"},
		{Job: "job-c", RunID: "400"},
	}); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}

	t.Run("all jobs sorted", func(t *testing.T) {
		jobs, err := db.ListJobs("")
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		want := []string{"job-a", "job-b", "job-c"}
		if len(jobs) != len(want) {
			t.Fatalf("got %d jobs, want %d", len(jobs), len(want))
		}
		for i := range want {
			if jobs[i] != want[i] {
				t.Errorf("jobs[%d] = %q, want %q", i, jobs[i], want[i])
			}
		}
	})

	t.Run("filtered", func(t *testing.T) {
		jobs, err := db.ListJobs("job-a")
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		if len(jobs) != 1 || jobs[0] != "job-a" {
			t.Errorf("got %v, want [job-a]", jobs)
		}
	})
}

func TestRunSQL(t *testing.T) {
	db := newTestDB(t)

	t.Run("rejects non-SELECT", func(t *testing.T) {
		_, _, err := db.RunSQL("DELETE FROM runs")
		if err == nil {
			t.Error("expected error for non-SELECT query")
		}
	})

	if err := db.StoreRuns([]RunResult{
		{Job: "job-a", RunID: "100"},
	}); err != nil {
		t.Fatalf("StoreRuns: %v", err)
	}

	t.Run("valid SELECT", func(t *testing.T) {
		rows, cols, err := db.RunSQL("SELECT job, run_id FROM runs")
		if err != nil {
			t.Fatalf("RunSQL: %v", err)
		}
		if len(cols) != 2 {
			t.Fatalf("got %d columns, want 2", len(cols))
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0][0] != "job-a" || rows[0][1] != "100" {
			t.Errorf("got row %v, want [job-a 100]", rows[0])
		}
	})
}
