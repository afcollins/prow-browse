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
	fmt.Printf("%s%s%d new run(s) across %d job(s)%s\n\n",
		colorBold, colorCyan, len(results), countUniqueJobs(results), colorReset)

	// Print column legend
	fmt.Printf("%sLegend:%s\n", colorBold, colorReset)
	for i, r := range results {
		shortName := shortJobName(r.Job, cfg)
		shortRun := r.RunID
		if len(shortRun) > 8 {
			shortRun = "…" + shortRun[len(shortRun)-8:]
		}
		fmt.Printf("  %s[%2d]%s %s : %s", colorYellow, i+1, colorReset, shortName, shortRun)
		if r.VariantID != "" {
			fmt.Printf(" %s(%s)%s", colorDim, r.VariantID, colorReset)
		}
		fmt.Println()
	}
	fmt.Println()

	// Print column header row (just numbers)
	fmt.Printf("%-*s", maxStepLen+2, "")
	for i := range results {
		fmt.Printf(" %s[%2d]%s", colorYellow, i+1, colorReset)
	}
	fmt.Println()

	// Separator line
	fmt.Printf("%s%s%s\n", colorDim, strings.Repeat("─", maxStepLen+2+len(results)*colWidth), colorReset)

	// Print each step row
	for _, step := range stepNames {
		fmt.Printf("%-*s", maxStepLen+2, step)
		for _, r := range results {
			if r.Steps[step] {
				fmt.Printf(" %s ✅ %s", colorGreen, colorReset)
			} else {
				// Check if this step appears in ANY result to distinguish
				// "not expected" vs "missing"
				if isStepExpectedForJob(step, results) {
					fmt.Printf(" %s ❌ %s", colorRed, colorReset)
				} else {
					fmt.Printf(" %s ── %s", colorDim, colorReset)
				}
			}
		}
		fmt.Println()
	}

	// Print no-recurse step details
	hasNoRecurseDetails := false
	for _, r := range results {
		if len(r.StepDirs) > 0 {
			hasNoRecurseDetails = true
			break
		}
	}

	if hasNoRecurseDetails {
		fmt.Printf("\n%s%sNo-recurse step contents:%s\n", colorBold, colorCyan, colorReset)
		for i, r := range results {
			if len(r.StepDirs) == 0 {
				continue
			}
			fmt.Printf("\n  %s[%2d]%s %s:%s\n", colorYellow, i+1, colorReset,
				shortJobName(r.Job, cfg), r.RunID)
			for step, children := range r.StepDirs {
				fmt.Printf("    %s%s/%s\n", colorBold, step, colorReset)
				for _, child := range children {
					fmt.Printf("      %s\n", child)
				}
			}
		}
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
