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
		"🚀", "🛸", "🚤", "🚂", "🚜", "🪁", "🚁", "🛶",
	},
}

func getEmojiPalette(name string) []string {
	if p, ok := emojiPalettes[name]; ok {
		return p
	}
	return emojiPalettes["default"]
}

// Two-level grouping: jobs are first classified by "job type" (loaded-upgrade,
// metal, normal) then by platform (AWS, ROSA, ROSA HCP, vSphere).
// The display group name is "JobType / Platform" for special types, or just
// "Platform" for normal jobs.

// jobTypeGroup identifies a special job type that gets its own section.
// Checked before platform detection. showAllSteps means platform-specific
// step filtering is skipped (these jobs mix platform steps).
type jobTypeGroup struct {
	name         string
	keyword      string
	showAllSteps bool
	subGroups    []string // optional: keywords to match in job name for sub-grouping
}

// Order matters: more specific keywords checked first.
var jobTypeGroups = []jobTypeGroup{
	{name: "Loaded Upgrade", keyword: "loaded-upgrade", showAllSteps: true},
	{name: "Metal", keyword: "metal", showAllSteps: true, subGroups: []string{
		"daily-virt", "weekly-telco-core-cpt", "weekly-eip", "weekly", "udn-bgp",
	}},
}

// platformDef identifies a platform by keywords in job name and step name.
type platformDef struct {
	name         string
	jobKeywords  []string // keywords to match in job name
	stepKeywords []string // keywords that mark a step as belonging to this platform
}

// Order matters: more specific keywords first (rosa_hcp before rosa,
// metal-rhoso and baremetal-multi before metal).
var platforms = []platformDef{
	{name: "ROSA HCP", jobKeywords: []string{"rosa_hcp", "hypershift"}, stepKeywords: []string{"rosa", "osd-ccs"}},
	{name: "ROSA", jobKeywords: []string{"rosa"}, stepKeywords: []string{"rosa", "osd-ccs"}},
	{name: "NetObserv", jobKeywords: []string{"netobserv-perf-tests-netobserv"}},
	{name: "Metal RHOSO", jobKeywords: []string{"metal-rhoso"}},
	{name: "Baremetal Multi", jobKeywords: []string{"baremetal-multi"}},
	{name: "vSphere", jobKeywords: []string{"vsphere"}, stepKeywords: []string{"vsphere", "upi-"}},
	{name: "AWS", jobKeywords: []string{"aws"}, stepKeywords: []string{"aws-", "ipi-"}},
}

// classifyRun returns a group name for a run.
// For special job types: "!JobType / SubGroup" or "!JobType / Platform".
// For normal jobs: "Platform".
func classifyRun(r RunResult) string {
	// Check platforms that should NOT be treated as metal sub-groups
	// (metal-rhoso, baremetal-multi are their own platforms)
	for _, p := range platforms {
		for _, kw := range p.jobKeywords {
			if strings.Contains(r.Job, kw) {
				// If this platform is also a jobType match, skip — let
				// it be handled as its own platform, not a metal sub-group
				isJobType := false
				for _, jt := range jobTypeGroups {
					if strings.Contains(kw, jt.keyword) {
						isJobType = true
					}
				}
				if !isJobType {
					// Not a job type — check if it should be under a job type
					break
				}
				// This is a more specific platform (metal-rhoso, baremetal-multi)
				return p.name
			}
		}
	}

	// Check for special job types (loaded-upgrade, metal)
	for _, jt := range jobTypeGroups {
		if strings.Contains(r.Job, jt.keyword) {
			sub := detectSubGroup(r.Job, jt)
			groupName := jt.name + " / " + sub
			if jt.showAllSteps {
				groupName = "!" + groupName
			}
			return groupName
		}
	}

	return detectPlatform(r.Job)
}

// detectSubGroup finds the sub-group for a job within a job type.
// For loaded-upgrade: sub-group is the platform (AWS, ROSA, etc.)
// For metal: sub-group is the trailing config keyword.
func detectSubGroup(jobName string, jt jobTypeGroup) string {
	// Check explicit sub-group keywords first
	for _, sg := range jt.subGroups {
		if strings.Contains(jobName, sg) {
			return sg
		}
	}
	// Fall back to platform detection
	return detectPlatform(jobName)
}

func detectPlatform(jobName string) string {
	for _, p := range platforms {
		for _, kw := range p.jobKeywords {
			if strings.Contains(jobName, kw) {
				return p.name
			}
		}
	}
	return "other"
}

// isStepForPlatform returns true if a step belongs to the given group or is common.
func isStepForPlatform(step, groupName string) bool {
	// Groups with showAllSteps (marked with "!" prefix) never filter out steps
	if strings.HasPrefix(groupName, "!") {
		return true
	}

	// Find which platforms match this step via their keywords.
	// We use earliest-keyword-wins: once a keyword matches, only platforms
	// that share that same keyword are considered. This means "ipi-conf-vsphere-check"
	// matches "vsphere" first (vSphere is listed before AWS) and stops — it won't
	// also match "ipi-" for AWS. But "rosa-sts-setup" matches "rosa" which is shared
	// by both ROSA HCP and ROSA, so it shows on both pages.
	firstKeyword := ""
	isPlatformSpecific := false
	matchesGroup := false

	for _, p := range platforms {
		for _, kw := range p.stepKeywords {
			if strings.Contains(step, kw) {
				if firstKeyword == "" {
					firstKeyword = kw
				}
				// Only consider matches for the same keyword as the first hit
				if kw == firstKeyword {
					isPlatformSpecific = true
					if p.name == groupName {
						matchesGroup = true
					}
				}
			}
		}
	}

	if !isPlatformSpecific {
		return true // common step — show on all pages
	}
	return matchesGroup
}

// displayGroupName returns the clean name for display (strips internal markers).
func displayGroupName(groupName string) string {
	return strings.TrimPrefix(groupName, "!")
}

// pageData holds everything needed to render one page of the grid.
type pageData struct {
	platform     string
	pageNum      int
	totalPages   int
	results      []RunResult
	emojis       []string
	stepNames    []string
	groupResults []RunResult // all results in this platform group (for expected-step heuristic)
	optionalSet  map[string]bool
}

func displayGrid(results []RunResult, cfg *Config, groupByPlatform bool, useTable bool) {
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

	// Build set of optional/gather steps (shown as ".." when absent)
	optionalSet := make(map[string]bool)
	for _, s := range cfg.NoRecurseSteps {
		optionalSet[s] = true
	}
	for _, s := range cfg.OptionalSteps {
		optionalSet[s] = true
	}

	// Gather steps are a subset of optional — used to push them to bottom of page
	gatherSet := make(map[string]bool)
	for _, s := range cfg.NoRecurseSteps {
		gatherSet[s] = true
	}

	// Print header
	fmt.Println()
	fmt.Printf("%s%s%d run(s) across %d job(s)%s\n\n",
		colorBold, colorCyan, len(results), countUniqueJobs(results), colorReset)

	// Group runs by platform (or treat all as one group if --group not set)
	type indexedResult struct {
		result RunResult
		emoji  string
	}
	groups := make(map[string][]indexedResult)
	var groupOrder []string
	groupSeen := make(map[string]bool)
	for i, r := range results {
		platform := "all"
		if groupByPlatform {
			platform = classifyRun(r)
		}
		if !groupSeen[platform] {
			groupSeen[platform] = true
			groupOrder = append(groupOrder, platform)
		}
		groups[platform] = append(groups[platform], indexedResult{r, colEmojis[i]})
	}

	// Display each platform group
	for _, platform := range groupOrder {
		group := groups[platform]

		// Collect steps for this group's runs
		groupSteps := make(map[string]bool)
		groupResults := make([]RunResult, len(group))
		groupEmojis := make([]string, len(group))
		for i, ir := range group {
			groupResults[i] = ir.result
			groupEmojis[i] = ir.emoji
			for step := range ir.result.Steps {
				groupSteps[step] = true
			}
		}

		// Filter steps: only show steps relevant to this platform when grouping
		// Gather steps are pushed to the bottom of each page.
		allStepNames := orderSteps(groupSteps, cfg.StepOrder)
		var stepNames []string
		var gatherSteps []string
		for _, s := range allStepNames {
			if !groupByPlatform || isStepForPlatform(s, platform) {
				if gatherSet[s] {
					gatherSteps = append(gatherSteps, s)
				} else {
					stepNames = append(stepNames, s)
				}
			}
		}
		stepNames = append(stepNames, gatherSteps...)

		// Paginate within this platform group
		pageSize := cfg.ColumnsPerPage
		totalPages := (len(group) + pageSize - 1) / pageSize
		for pageStart := 0; pageStart < len(group); pageStart += pageSize {
			pageEnd := pageStart + pageSize
			if pageEnd > len(group) {
				pageEnd = len(group)
			}

			pd := pageData{
				platform:     platform,
				pageNum:      pageStart/pageSize + 1,
				totalPages:   totalPages,
				results:      groupResults[pageStart:pageEnd],
				emojis:       groupEmojis[pageStart:pageEnd],
				stepNames:    stepNames,
				groupResults: groupResults,
				optionalSet:  optionalSet,
			}

			if useTable {
				renderTablePage(pd, cfg, groupByPlatform)
			} else {
				renderRawPage(pd, cfg, groupByPlatform)
			}
		}
	}
}

func renderRawPage(pd pageData, cfg *Config, groupByPlatform bool) {
	// Find the longest step name for padding
	maxStepLen := 0
	for _, s := range pd.stepNames {
		if len(s) > maxStepLen {
			maxStepLen = len(s)
		}
	}

	colWidth := 3

	// Print platform/page header
	showHeader := groupByPlatform || pd.totalPages > 1
	if showHeader {
		label := displayGroupName(pd.platform)
		if !groupByPlatform {
			label = ""
		}
		if pd.totalPages > 1 {
			page := fmt.Sprintf("page %d/%d", pd.pageNum, pd.totalPages)
			if label != "" {
				label = fmt.Sprintf("%s  [%s]", label, page)
			} else {
				label = page
			}
		}
		fmt.Printf("%s%s── %s ──%s\n\n", colorBold, colorCyan, label, colorReset)
	}

	// Compute legend column widths for alignment
	maxRunIDLen := 0
	maxShortNameLen := 0
	for _, r := range pd.results {
		if len(r.RunID) > maxRunIDLen {
			maxRunIDLen = len(r.RunID)
		}
		sn := shortJobName(r.Job, cfg)
		if len(sn) > maxShortNameLen {
			maxShortNameLen = len(sn)
		}
	}

	// Print column legend: emoji  run_id  job_name  (variant)
	fmt.Printf("%sLegend:%s\n", colorBold, colorReset)
	for i, r := range pd.results {
		shortName := shortJobName(r.Job, cfg)
		fmt.Printf("  %s %-*s  %-*s", pd.emojis[i], maxRunIDLen, r.RunID, maxShortNameLen, shortName)
		if r.VariantID != "" {
			fmt.Printf("  %s(%s)%s", colorDim, r.VariantID, colorReset)
		}
		fmt.Println()
	}
	fmt.Println()

	// Print column header row
	fmt.Printf("%-*s", maxStepLen+2, "")
	for _, e := range pd.emojis {
		fmt.Printf("%s ", e)
	}
	fmt.Println()

	// Separator line
	fmt.Printf("%s%s%s\n", colorDim, strings.Repeat("─", maxStepLen+2+len(pd.results)*colWidth), colorReset)

	// Print each step row (skip steps with no values on this page)
	for _, step := range pd.stepNames {
		hasValue := false
		for _, r := range pd.results {
			if r.Steps[step] {
				hasValue = true
				break
			}
		}
		if !hasValue {
			continue
		}
		fmt.Printf("%-*s", maxStepLen+2, step)
		isOptional := pd.optionalSet[step]
		for _, r := range pd.results {
			if r.Steps[step] {
				fmt.Printf("%s✅%s ", colorGreen, colorReset)
			} else if isOptional {
				fmt.Printf("%s..%s ", colorDim, colorReset)
			} else if isStepExpectedForJob(step, pd.groupResults) {
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
