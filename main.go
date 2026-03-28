package main

import (
	"context"
	"flag"
	"fmt"
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
	statePath := flag.String("state", "state.json", "State file path")
	showAll := flag.Bool("all", false, "Show all runs, not just new ones")
	jobFilter := flag.String("jobs", "", "Additional filter pattern for job names")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	state, err := loadState(*statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load state (%v), starting fresh\n", err)
		state = &State{SeenRuns: make(map[string][]string)}
	}

	ctx := context.Background()
	client, err := newGCSClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating GCS client: %v\n", err)
		os.Exit(1)
	}
	defer client.close()

	// 1. List jobs
	fmt.Fprintf(os.Stderr, "Listing jobs matching %q...\n", cfg.JobPattern)
	jobs, err := client.listJobs(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing jobs: %v\n", err)
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

	fmt.Fprintf(os.Stderr, "Found %d jobs\n", len(jobs))

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
				fmt.Fprintf(os.Stderr, "  Warning: error listing runs for %s: %v\n", shortJobName(j, cfg), err)
				return
			}

			// Sort runs descending (newest first) and limit
			sort.Sort(sort.Reverse(sort.StringSlice(runs)))
			if len(runs) > cfg.MaxRunsPerJob {
				runs = runs[:cfg.MaxRunsPerJob]
			}

			seenSet := make(map[string]bool)
			for _, s := range state.SeenRuns[j] {
				seenSet[s] = true
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
		fmt.Println("No new runs found.")
		return
	}

	// Sort new runs for consistent display
	sort.Slice(newRuns, func(i, j int) bool {
		if newRuns[i].job != newRuns[j].job {
			return newRuns[i].job < newRuns[j].job
		}
		return newRuns[i].runID > newRuns[j].runID // newest first within same job
	})

	fmt.Fprintf(os.Stderr, "Found %d new run(s), listing steps...\n", len(newRuns))

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
				fmt.Fprintf(os.Stderr, "  Warning: error listing steps for %s/%s: %v\n",
					shortJobName(nr.job, cfg), nr.runID, err)
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

	// 4. Display grid
	displayGrid(results, cfg)

	// 5. Update state
	for _, nr := range newRuns {
		state.SeenRuns[nr.job] = appendUnique(state.SeenRuns[nr.job], nr.runID)
	}
	if err := saveState(*statePath, state); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving state: %v\n", err)
	}
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

func shortJobName(job string, cfg *Config) string {
	// Strip common prefix for display
	prefix := cfg.JobPattern + "-"
	name := strings.TrimPrefix(job, prefix)
	return name
}
