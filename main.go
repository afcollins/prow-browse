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

type RunResult struct {
	Job       string
	RunID     string
	Steps     map[string]bool     // step name -> exists
	StepDirs  map[string][]string // step name -> immediate children (for no-recurse steps)
	VariantID string              // the variant directory name (e.g., "control-plane-120nodes")
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
			limit, _ := cmd.Flags().GetInt("limit")
			numRuns, _ := cmd.Flags().GetInt("n")
			group, _ := cmd.Flags().GetBool("group")
			useTable, _ := cmd.Flags().GetBool("table")

			displayLimit := cfg.MaxRunsPerJob
			if limit > 0 {
				displayLimit = limit
			}
			runLocal(db, cfg, jobFilter, displayLimit, numRuns, group, useTable)
			return nil
		},
	}
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "config.json", "Config file path")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "prow-status.db", "SQLite database path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable debug logging")
	rootCmd.Flags().StringP("jobs", "j",  "", "Filter job names by substring")
	rootCmd.Flags().IntP("limit", "l",  0, "Max runs per job (0 = config default)")
	rootCmd.Flags().IntP("n", "n", 0, "Max total runs, most recent first (0 = all)")
	rootCmd.Flags().Bool("stats", false, "Show database statistics")
	rootCmd.Flags().String("query", "", "Run a SQL query against the local database")
	rootCmd.Flags().BoolP("group", "g", false, "Group columns by platform (AWS, ROSA, etc.)")
	rootCmd.Flags().BoolP("table", "t", false, "Use lipgloss table rendering")

	fetchCmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch new runs from GCS and store in local database",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, err := openConfigAndDB(configPath, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			jobFilter, _ := cmd.Flags().GetString("jobs")
			showAll, _ := cmd.Flags().GetBool("all")
			numRuns, _ := cmd.Flags().GetInt("n")
			depth, _ := cmd.Flags().GetInt("depth")
			group, _ := cmd.Flags().GetBool("group")
			useTable, _ := cmd.Flags().GetBool("table")

			runFetch(db, cfg, jobFilter, showAll, numRuns, depth, group, useTable)
			return nil
		},
	}
	fetchCmd.Flags().StringP("jobs", "j", "", "Filter job names by substring")
	fetchCmd.Flags().Bool("all", false, "Re-fetch runs already in the database")
	fetchCmd.Flags().IntP("number", "n", 0, "Max total runs to fetch, most recent first (0 = all)")
	fetchCmd.Flags().IntP("depth", "d", 5, "Runs per job to look back in GCS")
	fetchCmd.Flags().BoolP("group", "g", false, "Group columns by platform (AWS, ROSA, etc.)")
	fetchCmd.Flags().BoolP("table", "t", false, "Use lipgloss table rendering")

	pullCmd := &cobra.Command{
		Use:   "pull <run-id-suffix> [<run-id-suffix> ...]",
		Short: "Re-fetch specific runs by ID suffix (matched right-to-left, like git short hashes)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, err := openConfigAndDB(configPath, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			group, _ := cmd.Flags().GetBool("group")
			useTable, _ := cmd.Flags().GetBool("table")

			runPull(db, cfg, args, group, useTable)
			return nil
		},
	}
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

func runLocal(db *DB, cfg *Config, jobFilter string, limit int, numRuns int, group bool, useTable bool) {
	results, err := db.QueryResults(jobFilter, limit)
	if err != nil {
		logrus.WithError(err).Fatal("failed to query database")
	}
	if len(results) == 0 {
		logrus.Info("no matching runs in local database; run 'prow-status fetch' to populate")
		return
	}

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

func runFetch(db *DB, cfg *Config, jobFilter string, showAll bool, numRuns int, depth int, group bool, useTable bool) {
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
			if len(runs) > depth {
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
	}).Info("finished listing runs")

	if len(newRuns) == 0 {
		logrus.Info("no new runs found; run 'prow-status fetch --all' to re-fetch already-seen runs")
		return
	}

	if numRuns > 0 {
		sort.Slice(newRuns, func(i, j int) bool {
			return newRuns[i].runID > newRuns[j].runID
		})
		if len(newRuns) > numRuns {
			newRuns = newRuns[:numRuns]
		}
		logrus.WithField("count", len(newRuns)).Info("limited to most recent runs")
	}

	sort.Slice(newRuns, func(i, j int) bool {
		if newRuns[i].job != newRuns[j].job {
			return newRuns[i].job < newRuns[j].job
		}
		return newRuns[i].runID > newRuns[j].runID
	})

	logrus.WithField("count", len(newRuns)).Info("listing steps for new runs")

	var completedSteps int64
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
				logrus.WithError(err).WithFields(logrus.Fields{
					"job": shortJobName(nr.job, cfg),
					"run": nr.runID,
				}).Warn("failed to list steps")
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
			n := atomic.AddInt64(&completedSteps, 1)
			if n%20 == 0 || n == int64(len(newRuns)) {
				logrus.WithFields(logrus.Fields{
					"completed":  n,
					"total":      len(newRuns),
					"gcs_calls":  client.CallCount(),
				}).Info("listing steps progress")
			}
		}(i)
	}
	wg.Wait()

	logrus.WithField("total_gcs_calls", client.CallCount()).Info("GCS API calls complete")

	if err := db.StoreResults(results); err != nil {
		logrus.WithError(err).Error("failed to store results")
	} else {
		logrus.WithField("count", len(results)).Info("stored runs in local database")
	}

	displayGrid(results, cfg, group, useTable)
}

func runPull(db *DB, cfg *Config, suffixes []string, group bool, useTable bool) {
	type jobRun struct {
		job   string
		runID string
	}
	var targets []jobRun
	for _, suffix := range suffixes {
		job, runID, err := db.ResolveRunID(suffix)
		if err != nil {
			logrus.WithError(err).WithField("suffix", suffix).Fatal("failed to resolve run ID")
		}
		targets = append(targets, jobRun{job, runID})
		logrus.WithFields(logrus.Fields{
			"suffix": suffix,
			"job":    shortJobName(job, cfg),
			"run_id": runID,
		}).Info("resolved run")
	}

	ctx := context.Background()
	client, err := newGCSClient(ctx, cfg)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create GCS client")
	}
	defer client.close()

	results := make([]RunResult, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
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
		}(i, t)
	}
	wg.Wait()

	logrus.WithField("total_gcs_calls", client.CallCount()).Info("GCS API calls complete")

	if err := db.StoreResults(results); err != nil {
		logrus.WithError(err).Error("failed to store results")
	} else {
		logrus.WithField("count", len(results)).Info("updated runs in local database")
	}

	displayGrid(results, cfg, group, useTable)
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
