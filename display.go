package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

func displayGrid(results []RunResult, cfg *Config) {
	if len(results) == 0 {
		return
	}

	// Sort results by run ID (chronological order)
	sort.Slice(results, func(i, j int) bool {
		return results[i].RunID < results[j].RunID
	})

	// Build set of gather/no-recurse step prefixes
	gatherSet := make(map[string]bool)
	for _, s := range cfg.NoRecurseSteps {
		gatherSet[s] = true
	}

	// Collect all unique step names across all results
	allSteps := make(map[string]bool)
	for _, r := range results {
		for step := range r.Steps {
			allSteps[step] = true
		}
	}

	// Sort step names for consistent display
	var stepNames []string
	for s := range allSteps {
		stepNames = append(stepNames, s)
	}
	sort.Strings(stepNames)

	// Find the longest step name for padding
	maxStepLen := 0
	for _, s := range stepNames {
		if len(s) > maxStepLen {
			maxStepLen = len(s)
		}
	}

	// Column width for each result
	colWidth := 5 // enough for " ✅ " or " ❌ " or " ── "

	// Print header
	fmt.Println()
	fmt.Printf("%s%s%d run(s) across %d job(s)%s\n\n",
		colorBold, colorCyan, len(results), countUniqueJobs(results), colorReset)

	// Print column legend
	fmt.Printf("%sLegend:%s\n", colorBold, colorReset)
	for i, r := range results {
		shortName := shortJobName(r.Job, cfg)
		fmt.Printf("  %s[%d]%s %s : %s", colorYellow, i+1, colorReset, shortName, r.RunID)
		if r.VariantID != "" {
			fmt.Printf(" %s(%s)%s", colorDim, r.VariantID, colorReset)
		}
		fmt.Println()
	}
	fmt.Println()

	// Print column header row (just numbers)
	fmt.Printf("%-*s", maxStepLen+2, "")
	for i := range results {
		fmt.Printf(" %s[%d]%s", colorYellow, i+1, colorReset)
	}
	fmt.Println()

	// Separator line
	fmt.Printf("%s%s%s\n", colorDim, strings.Repeat("─", maxStepLen+2+len(results)*colWidth), colorReset)

	// Print each step row
	for _, step := range stepNames {
		fmt.Printf("%-*s", maxStepLen+2, step)
		isGatherStep := gatherSet[step]
		for _, r := range results {
			if r.Steps[step] {
				fmt.Printf(" %s ✅ %s", colorGreen, colorReset)
			} else if isGatherStep {
				fmt.Printf(" %s .. %s", colorDim, colorReset)
			} else if isStepExpectedForJob(step, results) {
				fmt.Printf(" %s ❌ %s", colorRed, colorReset)
			} else {
				fmt.Printf(" %s ── %s", colorDim, colorReset)
			}
		}
		fmt.Println()
	}

	fmt.Println()
}

// isStepExpectedForJob checks if a step appears in at least some percentage of results,
// suggesting it's a common step. Steps that appear in very few results are likely
// job-specific and marked as "not applicable" for jobs that don't have them.
func isStepExpectedForJob(step string, results []RunResult) bool {
	count := 0
	for _, r := range results {
		if r.Steps[step] {
			count++
		}
	}
	// If a step appears in less than 30% of results, it's likely job-specific
	threshold := len(results) * 30 / 100
	if threshold < 1 {
		threshold = 1
	}
	return count >= threshold
}

func countUniqueJobs(results []RunResult) int {
	jobs := make(map[string]bool)
	for _, r := range results {
		jobs[r.Job] = true
	}
	return len(jobs)
}
