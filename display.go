package main

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

// Emoji palettes for column headers. Each emoji is 2 terminal columns wide.
var emojiPalettes = map[string][]string{
	"fruits": {
		"🍎", "🍊", "🍋", "🍇", "🍉", "🍓", "🫐", "🍑",
		"🍒", "🥝", "🍍", "🥭", "🍌", "🥥", "🍈", "🍐",
	},
	"default": {
		"🔴", "🟠", "🟡", "🟢", "🔵", "🟣", "🟤", "⚫",
		"🔶", "🔷", "💠", "🔮", "💎", "🪨", "⭐", "🌙",
		"🍎", "🍊", "🍋", "🍇", "🍉", "🍓", "🫐", "🍑",
		"🌸", "🌺", "🌻", "🌼", "🌷", "🪷", "🌹", "💐",
		"🐶", "🐱", "🐻", "🐼", "🐨", "🦊", "🐸", "🐧",
		"🎯", "🎲", "🎮", "🎸", "🎺", "🥁", "🎨", "🧩",
		"⚡", "🔥", "💧", "🌊", "🧊", "🌀", "💫", "🌈",
		"🚀", "🛸", "🚤", "🚂", "🚜", "🛩", "🚁", "🛶",
	},
}

func getEmojiPalette(name string) []string {
	if p, ok := emojiPalettes[name]; ok {
		return p
	}
	return emojiPalettes["default"]
}

func displayGrid(results []RunResult, cfg *Config) {
	if len(results) == 0 {
		return
	}

	// Sort results by run ID (chronological order)
	sort.Slice(results, func(i, j int) bool {
		return results[i].RunID < results[j].RunID
	})

	// Assign a random emoji to each column (shuffled, no repeats until palette exhausted)
	palette := getEmojiPalette(cfg.EmojiPalette)
	shuffled := make([]string, len(palette))
	copy(shuffled, palette)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	colEmojis := make([]string, len(results))
	for i := range results {
		colEmojis[i] = shuffled[i%len(shuffled)]
	}

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

	// Order steps: use config step_order, then append any unknown steps alphabetically
	stepNames := orderSteps(allSteps, cfg.StepOrder)

	// Find the longest step name for padding
	maxStepLen := 0
	for _, s := range stepNames {
		if len(s) > maxStepLen {
			maxStepLen = len(s)
		}
	}

	// Each cell is 3 terminal columns: emoji (2 wide) + 1 space
	colWidth := 3

	// Print header
	fmt.Println()
	fmt.Printf("%s%s%d run(s) across %d job(s)%s\n\n",
		colorBold, colorCyan, len(results), countUniqueJobs(results), colorReset)

	// Print column legend
	fmt.Printf("%sLegend:%s\n", colorBold, colorReset)
	for i, r := range results {
		shortName := shortJobName(r.Job, cfg)
		fmt.Printf("  %s %s : %s", colEmojis[i], shortName, r.RunID)
		if r.VariantID != "" {
			fmt.Printf(" %s(%s)%s", colorDim, r.VariantID, colorReset)
		}
		fmt.Println()
	}
	fmt.Println()

	// Print column header row — each emoji is 2 wide + 1 space = 3 cols, matching cell width
	fmt.Printf("%-*s", maxStepLen+2, "")
	for _, e := range colEmojis {
		fmt.Printf("%s ", e)
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
				fmt.Printf("%s✅%s ", colorGreen, colorReset)
			} else if isGatherStep {
				fmt.Printf("%s..%s ", colorDim, colorReset)
			} else if isStepExpectedForJob(step, results) {
				fmt.Printf("%s❌%s ", colorRed, colorReset)
			} else {
				fmt.Printf("%s──%s ", colorDim, colorReset)
			}
		}
		fmt.Println()
	}

	fmt.Println()
}

// orderSteps returns step names ordered by config step_order.
// Steps not in the config order are appended alphabetically at the end.
func orderSteps(allSteps map[string]bool, configOrder []string) []string {
	var ordered []string
	seen := make(map[string]bool)

	// First add steps in config order (only if they exist in results)
	for _, s := range configOrder {
		if allSteps[s] {
			ordered = append(ordered, s)
			seen[s] = true
		}
	}

	// Then add any remaining steps alphabetically
	var remaining []string
	for s := range allSteps {
		if !seen[s] {
			remaining = append(remaining, s)
		}
	}
	sort.Strings(remaining)
	ordered = append(ordered, remaining...)

	return ordered
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
