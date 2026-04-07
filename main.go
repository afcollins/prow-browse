package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// StepResult represents the outcome of a single step.
type StepResult int

const (
	StepMissing StepResult = 0 // step directory not present
	StepSuccess StepResult = 1 // finished.json result=SUCCESS
	StepFailure StepResult = 2 // finished.json result=FAILURE
	StepUnknown StepResult = 3 // step dir exists but no finished.json or unreadable
)

type RunResult struct {
	Job       string
	RunID     string
	Steps     map[string]StepResult // step name -> result
	StepDirs  map[string][]string   // step name -> immediate children (for no-recurse steps)
	VariantID string                // the variant directory name (e.g., "control-plane-120nodes")
	Pulled    bool                  // true if step data has been fetched from GCS
}

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	var configPath, dbPath string
	var verbose bool

	rootCmd := &cobra.Command{
		Use:   "prow-status",
		Short: "Display Prow CI job status grid from local database",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose {
				logrus.SetLevel(logrus.DebugLevel)
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, err := openConfigAndDB(configPath, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			if statsFlag, _ := cmd.Flags().GetBool("stats"); statsFlag {
				printStats(db)
				return nil
			}
			if q, _ := cmd.Flags().GetString("query"); q != "" {
				runQuery(db, q)
				return nil
			}

			jobFilter, _ := cmd.Flags().GetString("jobs")
			numRuns, _ := cmd.Flags().GetInt("number")
			group, _ := cmd.Flags().GetBool("group")
			useTable, _ := cmd.Flags().GetBool("table")

			runLocal(db, cfg, jobFilter, numRuns, group, useTable)
			return nil
		},
	}
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "config.json", "Config file path")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "prow-status.db", "SQLite database path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable debug logging")
	rootCmd.Flags().StringP("jobs", "j",  "", "Filter job names by substring")
	rootCmd.Flags().IntP("number", "n", 0, "Max runs to display, most recent first (0 = all)")
	rootCmd.Flags().Bool("stats", false, "Show database statistics")
	rootCmd.Flags().String("query", "", "Run a SQL query against the local database")
	rootCmd.Flags().BoolP("group", "g", false, "Group columns by platform (AWS, ROSA, etc.)")
	rootCmd.Flags().BoolP("table", "t", false, "Use lipgloss table rendering")

	fetchCmd := &cobra.Command{
		Use:   "fetch",
		Short: "Discover run IDs from GCS and store in local database (no artifact traversal)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, err := openConfigAndDB(configPath, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			jobFilter, _ := cmd.Flags().GetString("jobs")
			showAll, _ := cmd.Flags().GetBool("all")
			numRuns, _ := cmd.Flags().GetInt("number")

			runFetch(db, cfg, jobFilter, showAll, numRuns)
			return nil
		},
	}
	fetchCmd.Flags().StringP("jobs", "j", "", "Filter job names by substring")
	fetchCmd.Flags().Bool("all", false, "Re-fetch runs already in the database")
	fetchCmd.Flags().IntP("number", "n", 5, "Runs per job to look back in GCS")

	pullCmd := &cobra.Command{
		Use:   "pull [run-id-suffix ...]",
		Short: "Fetch step data for runs (latest unpulled via -n, or specific run IDs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, err := openConfigAndDB(configPath, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			jobFilter, _ := cmd.Flags().GetString("jobs")
			numRuns, _ := cmd.Flags().GetInt("number")
			group, _ := cmd.Flags().GetBool("group")
			useTable, _ := cmd.Flags().GetBool("table")

			runPull(db, cfg, args, jobFilter, numRuns, group, useTable)
			return nil
		},
	}
	pullCmd.Flags().StringP("jobs", "j", "", "Filter job names by substring")
	pullCmd.Flags().IntP("number", "n", 0, "Max runs to pull (latest unpulled, 0 = all unpulled)")
	pullCmd.Flags().BoolP("group", "g", false, "Group columns by platform (AWS, ROSA, etc.)")
	pullCmd.Flags().BoolP("table", "t", false, "Use lipgloss table rendering")

	rootCmd.AddCommand(fetchCmd, pullCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func openConfigAndDB(configPath, dbPath string) (*Config, *DB, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	db, err := openDB(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}
	return cfg, db, nil
}

func runLocal(db *DB, cfg *Config, jobFilter string, numRuns int, group bool, useTable bool) {
	results, err := db.QueryResults(jobFilter)
	if err != nil {
		logrus.WithError(err).Fatal("failed to query database")
	}
	if len(results) == 0 {
		logrus.Info("no matching runs in local database; run 'prow-status fetch' to populate")
		return
	}

	// Apply global limit: most recent N runs across all jobs
	if numRuns > 0 {
		sort.Slice(results, func(i, j int) bool {
			return results[i].RunID > results[j].RunID
		})
		if len(results) > numRuns {
			results = results[:numRuns]
		}
	}

	logrus.WithField("count", len(results)).Info("loaded runs from local database")
	displayGrid(results, cfg, group, useTable)
}

func runFetch(db *DB, cfg *Config, jobFilter string, showAll bool, depth int) {
	ctx := context.Background()
	client, err := newGCSClient(ctx, cfg)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create GCS client")
	}
	defer client.close()

	logrus.WithField("pattern", cfg.JobPattern).Info("listing jobs")
	jobs, err := client.listJobs(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("failed to list jobs")
	}

	if jobFilter != "" {
		var filtered []string
		for _, j := range jobs {
			if strings.Contains(j, jobFilter) {
				filtered = append(filtered, j)
			}
		}
		jobs = filtered
	}

	logrus.WithField("count", len(jobs)).Info("found jobs")

	type jobRun struct {
		job   string
		runID string
	}

	var newRuns []jobRun
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	logrus.WithField("concurrency", cfg.Concurrency).Info("listing runs for each job")
	var completedJobs int64
	var totalNewRuns int64
	var totalSeenRuns int64

	for _, job := range jobs {
		wg.Add(1)
		go func(j string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			runs, err := client.listRuns(ctx, j)
			if err != nil {
				logrus.WithError(err).WithField("job", shortJobName(j, cfg)).Warn("failed to list runs")
				return
			}

			logrus.WithFields(logrus.Fields{"job": shortJobName(j, cfg), "runs": len(runs)}).Debug("listed runs")

			sort.Sort(sort.Reverse(sort.StringSlice(runs)))
			if depth > 0 && len(runs) > depth {
				runs = runs[:depth]
			}

			seenSet, err := db.SeenRuns(j)
			if err != nil {
				logrus.WithError(err).WithField("job", shortJobName(j, cfg)).Warn("failed to check seen runs")
				seenSet = make(map[string]bool)
			}

			mu.Lock()
			for _, r := range runs {
				if showAll || !seenSet[r] {
					newRuns = append(newRuns, jobRun{j, r})
					totalNewRuns++
				} else {
					totalSeenRuns++
				}
			}
			completedJobs++
			if completedJobs%50 == 0 {
				logrus.WithFields(logrus.Fields{
					"completed": completedJobs,
					"total":     len(jobs),
					"new":       totalNewRuns,
					"seen":      totalSeenRuns,
				}).Info("listing runs progress")
			}
			mu.Unlock()
		}(job)
	}
	wg.Wait()

	logrus.WithFields(logrus.Fields{
		"jobs":      completedJobs,
		"new_runs":  totalNewRuns,
		"seen_runs": totalSeenRuns,
		"gcs_calls": client.CallCount(),
	}).Info("finished listing runs")

	if len(newRuns) == 0 {
		fmt.Println("no new runs found; use --all to re-fetch already-seen runs")
		return
	}

	// Sort by job name, then most recent first
	sort.Slice(newRuns, func(i, j int) bool {
		if newRuns[i].job != newRuns[j].job {
			return newRuns[i].job < newRuns[j].job
		}
		return newRuns[i].runID > newRuns[j].runID
	})

	// Store run entries (no step data)
	results := make([]RunResult, len(newRuns))
	for i, nr := range newRuns {
		results[i] = RunResult{Job: nr.job, RunID: nr.runID}
	}
	if err := db.StoreRuns(results); err != nil {
		logrus.WithError(err).Error("failed to store runs")
	}

	// Print run list grouped by job
	currentJob := ""
	for _, nr := range newRuns {
		if nr.job != currentJob {
			currentJob = nr.job
			fmt.Printf("\n%s\n", shortJobName(nr.job, cfg))
		}
		fmt.Printf("  %s\n", nr.runID)
	}
	fmt.Printf("\n%d new runs stored. Use 'prow-status pull -n <N>' to fetch step data.\n", len(newRuns))
}

func runPull(db *DB, cfg *Config, suffixes []string, jobFilter string, numRuns int, group bool, useTable bool) {
	type jobRun struct {
		job   string
		runID string
	}

	ctx := context.Background()
	var client *gcsClient

	var targets []jobRun

	// Mode 1: explicit run IDs (always force re-traverse)
	for _, suffix := range suffixes {
		job, runID, err := db.ResolveRunID(suffix)
		if err != nil {
			// Fallback: search GCS
			logrus.WithField("suffix", suffix).Info("not found in DB, searching GCS")
			if client == nil {
				var cerr error
				client, cerr = newGCSClient(ctx, cfg)
				if cerr != nil {
					logrus.WithError(cerr).Fatal("failed to create GCS client")
				}
			}
			job, runID, err = client.findRunByID(ctx, suffix)
			if err != nil {
				logrus.WithError(err).WithField("suffix", suffix).Fatal("failed to find run ID")
			}
		}
		targets = append(targets, jobRun{job, runID})
		logrus.WithFields(logrus.Fields{
			"suffix": suffix,
			"job":    shortJobName(job, cfg),
			"run_id": runID,
		}).Info("resolved run (force re-pull)")
	}

	// Mode 2: latest N unpulled runs from DB
	if numRuns > 0 || (len(suffixes) == 0) {
		limit := numRuns
		if limit == 0 {
			limit = cfg.MaxRunsPerJob
		}
		unpulled, err := db.QueryRunsWithoutSteps(jobFilter, limit)
		if err != nil {
			logrus.WithError(err).Fatal("failed to query unpulled runs")
		}
		for _, r := range unpulled {
			targets = append(targets, jobRun{r.Job, r.RunID})
		}
		if len(unpulled) > 0 {
			logrus.WithField("count", len(unpulled)).Info("found unpulled runs in DB")
		}
	}

	if len(targets) == 0 {
		fmt.Println("no runs to pull; use 'prow-status fetch' to discover new runs")
		return
	}

	if client == nil {
		var err error
		client, err = newGCSClient(ctx, cfg)
		if err != nil {
			logrus.WithError(err).Fatal("failed to create GCS client")
		}
	}
	defer client.close()

	// Fetch steps concurrently
	results := make([]RunResult, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
	var completedSteps int64

	for i, t := range targets {
		wg.Add(1)
		go func(idx int, nr jobRun) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			steps, stepDirs, variant, err := client.listSteps(ctx, nr.job, nr.runID)
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"job": shortJobName(nr.job, cfg),
					"run": nr.runID,
				}).Warn("failed to list steps")
				steps = make(map[string]StepResult)
				stepDirs = make(map[string][]string)
			}
			results[idx] = RunResult{
				Job:       nr.job,
				RunID:     nr.runID,
				Steps:     steps,
				StepDirs:  stepDirs,
				VariantID: variant,
			}
			n := atomic.AddInt64(&completedSteps, 1)
			if n%20 == 0 || n == int64(len(targets)) {
				logrus.WithFields(logrus.Fields{
					"completed": n,
					"total":     len(targets),
					"gcs_calls": client.CallCount(),
				}).Info("pulling steps progress")
			}
		}(i, t)
	}
	wg.Wait()

	logrus.WithField("total_gcs_calls", client.CallCount()).Info("GCS API calls complete")

	if err := db.StoreResults(results); err != nil {
		logrus.WithError(err).Error("failed to store results")
	} else {
		logrus.WithField("count", len(results)).Info("updated runs in local database")
	}

	// Display from DB so Pulled flag is set correctly
	runLocal(db, cfg, jobFilter, len(targets), group, useTable)
}

func shortJobName(job string, cfg *Config) string {
	prefix := cfg.JobPattern + "-"
	if strings.HasPrefix(job, prefix) {
		return "-j " + strings.TrimPrefix(job, prefix)
	}
	return job
}

func printStats(db *DB) {
	jobs, runs, steps, err := db.Stats()
	if err != nil {
		logrus.WithError(err).Fatal("failed to get stats")
	}
	logrus.WithFields(logrus.Fields{"jobs": jobs, "runs": runs, "steps": steps}).Info("database statistics")

	dbJobs, err := db.ListJobs("")
	if err != nil {
		return
	}
	for _, j := range dbJobs {
		logrus.WithField("name", j).Info("stored job")
	}
}

func runQuery(db *DB, query string) {
	rows, cols, err := db.RunSQL(query)
	if err != nil {
		logrus.WithError(err).Fatal("query failed")
	}

	if len(rows) == 0 {
		logrus.Info("query returned no results")
		return
	}

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

	for _, row := range rows {
		for i, v := range row {
			fmt.Printf("%-*s  ", widths[i], v)
		}
		fmt.Println()
	}
	fmt.Printf("\n(%d rows)\n", len(rows))
}
