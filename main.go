package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
)

type RunResult struct {
	Job       string
	RunID     string
	Steps     map[string]bool     // step name -> exists
	StepDirs  map[string][]string // step name -> immediate children (for no-recurse steps)
	VariantID string              // the variant directory name (e.g., "control-plane-120nodes")
}

func main() {
	configPath := flag.String("config", "config.json", "Config file path")
	dbPath := flag.String("db", "prow-status.db", "SQLite database path")
	showAll := flag.Bool("all", false, "Show all runs, not just new ones")
	jobFilter := flag.String("jobs", "", "Filter job names by substring")
	local := flag.Bool("local", false, "Display from local database only, no GCS fetch")
	limit := flag.Int("limit", 0, "Max runs per job to display (0 = use config max_runs_per_job)")
	query := flag.String("query", "", "Run a SQL query against the local database")
	stats := flag.Bool("stats", false, "Show database statistics")
	group := flag.Bool("group", false, "Group columns by platform (AWS, ROSA, etc.)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg, err := loadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	db, err := openDB(*dbPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Handle --stats
	if *stats {
		printStats(db)
		return
	}

	// Handle --query
	if *query != "" {
		runQuery(db, *query)
		return
	}

	displayLimit := cfg.MaxRunsPerJob
	if *limit > 0 {
		displayLimit = *limit
	}

	// Handle --local: display from database only
	if *local {
		results, err := db.QueryResults(*jobFilter, displayLimit)
		if err != nil {
			slog.Error("failed to query database", "error", err)
			os.Exit(1)
		}
		if len(results) == 0 {
			slog.Info("no matching runs in local database")
			return
		}
		slog.Info("loaded runs from local database", "count", len(results))
		displayGrid(results, cfg, *group)
		return
	}

	// Online mode: fetch from GCS
	ctx := context.Background()
	client, err := newGCSClient(ctx, cfg)
	if err != nil {
		slog.Error("failed to create GCS client", "error", err)
		os.Exit(1)
	}
	defer client.close()

	// 1. List jobs
	slog.Info("listing jobs", "pattern", cfg.JobPattern)
	jobs, err := client.listJobs(ctx)
	if err != nil {
		slog.Error("failed to list jobs", "error", err)
		os.Exit(1)
	}

	if *jobFilter != "" {
		var filtered []string
		for _, j := range jobs {
			if strings.Contains(j, *jobFilter) {
				filtered = append(filtered, j)
			}
		}
		jobs = filtered
	}

	slog.Info("found jobs", "count", len(jobs))

	// 2. For each job, list runs and find new ones
	type jobRun struct {
		job   string
		runID string
	}

	var newRuns []jobRun
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for _, job := range jobs {
		wg.Add(1)
		go func(j string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			runs, err := client.listRuns(ctx, j)
			if err != nil {
				slog.Warn("failed to list runs", "job", shortJobName(j, cfg), "error", err)
				return
			}

			// Sort runs descending (newest first) and limit
			sort.Sort(sort.Reverse(sort.StringSlice(runs)))
			if len(runs) > cfg.MaxRunsPerJob {
				runs = runs[:cfg.MaxRunsPerJob]
			}

			// Check which runs we've already seen in the database
			seenSet, err := db.SeenRuns(j)
			if err != nil {
				slog.Warn("failed to check seen runs", "job", shortJobName(j, cfg), "error", err)
				seenSet = make(map[string]bool)
			}

			mu.Lock()
			for _, r := range runs {
				if *showAll || !seenSet[r] {
					newRuns = append(newRuns, jobRun{j, r})
				}
			}
			mu.Unlock()
		}(job)
	}
	wg.Wait()

	if len(newRuns) == 0 {
		slog.Info("no new runs found, use --local to view cached data or --all to re-fetch")
		return
	}

	// Sort new runs for consistent display
	sort.Slice(newRuns, func(i, j int) bool {
		if newRuns[i].job != newRuns[j].job {
			return newRuns[i].job < newRuns[j].job
		}
		return newRuns[i].runID > newRuns[j].runID // newest first within same job
	})

	slog.Info("found new runs, listing steps", "count", len(newRuns))

	// 3. For each new run, list steps
	results := make([]RunResult, len(newRuns))
	for i := range newRuns {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			nr := newRuns[idx]
			steps, stepDirs, variant, err := client.listSteps(ctx, nr.job, nr.runID)
			if err != nil {
				slog.Warn("failed to list steps", "job", shortJobName(nr.job, cfg), "run", nr.runID, "error", err)
				steps = make(map[string]bool)
				stepDirs = make(map[string][]string)
			}
			results[idx] = RunResult{
				Job:       nr.job,
				RunID:     nr.runID,
				Steps:     steps,
				StepDirs:  stepDirs,
				VariantID: variant,
			}
		}(i)
	}
	wg.Wait()

	// 4. Store results in database
	if err := db.StoreResults(results); err != nil {
		slog.Error("failed to store results", "error", err)
	} else {
		slog.Info("stored runs in local database", "count", len(results))
	}

	// 5. Display grid
	displayGrid(results, cfg, *group)
}

func shortJobName(job string, cfg *Config) string {
	prefix := cfg.JobPattern + "-"
	if strings.HasPrefix(job, prefix) {
		return "..." + strings.TrimPrefix(job, prefix)
	}
	return job
}

func printStats(db *DB) {
	jobs, runs, steps, err := db.Stats()
	if err != nil {
		slog.Error("failed to get stats", "error", err)
		os.Exit(1)
	}
	slog.Info("database statistics", "jobs", jobs, "runs", runs, "steps", steps)

	dbJobs, err := db.ListJobs("")
	if err != nil {
		return
	}
	for _, j := range dbJobs {
		slog.Info("stored job", "name", j)
	}
}

func runQuery(db *DB, query string) {
	rows, cols, err := db.RunSQL(query)
	if err != nil {
		slog.Error("query failed", "error", err)
		os.Exit(1)
	}

	if len(rows) == 0 {
		slog.Info("query returned no results")
		return
	}

	// Compute column widths
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	for _, row := range rows {
		for i, v := range row {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}

	// Print header
	for i, c := range cols {
		fmt.Printf("%-*s  ", widths[i], c)
	}
	fmt.Println()
	for i := range cols {
		for j := 0; j < widths[i]; j++ {
			fmt.Print("─")
		}
		fmt.Print("  ")
	}
	fmt.Println()

	// Print rows
	for _, row := range rows {
		for i, v := range row {
			fmt.Printf("%-*s  ", widths[i], v)
		}
		fmt.Println()
	}
	fmt.Printf("\n(%d rows)\n", len(rows))
}
