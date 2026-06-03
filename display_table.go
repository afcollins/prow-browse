package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

var (
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

func renderTablePage(pd pageData, cfg *Config, groupByPlatform bool) {
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
		fmt.Printf("\n%s\n\n", styleHeader.Render("── "+label+" ──"))
	}

	// Print legend
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

	fmt.Println(styleHeader.Render("Legend:"))
	for i, r := range pd.results {
		shortName := shortJobName(r.Job, cfg)
		line := fmt.Sprintf("  %s %-*s  %-*s", pd.emojis[i], maxRunIDLen, r.RunID, maxShortNameLen, shortName)
		if r.VariantID != "" {
			line += fmt.Sprintf("  %s", styleDim.Render("("+r.VariantID+")"))
		}
		fmt.Println(line)
		if pd.showURLs {
			fmt.Printf("      %s\n", styleDim.Render(runURL(cfg, r)))
		}
	}
	fmt.Println()

	// Filter steps with no values on this page
	var visibleSteps []string
	for _, step := range pd.stepNames {
		for _, r := range pd.results {
			if _, exists := r.Steps[step]; exists {
				visibleSteps = append(visibleSteps, step)
				break
			}
		}
	}

	// Build headers: step name column + one emoji per run
	headers := make([]string, 0, len(pd.emojis)+1)
	headers = append(headers, "Step")
	headers = append(headers, pd.emojis...)

	// Build rows and track cell types for styling
	type cellType int
	const (
		cellNormal cellType = iota
		cellGreen
		cellRed
		cellDim
		cellStep
	)

	cellTypes := make([][]cellType, len(visibleSteps))
	rows := make([][]string, len(visibleSteps))

	for ri, step := range visibleSteps {
		row := make([]string, 0, len(pd.results)+1)
		types := make([]cellType, 0, len(pd.results)+1)

		row = append(row, step)
		types = append(types, cellStep)

		isOptional := pd.optionalSet[step]
		for _, r := range pd.results {
			if !r.Pulled {
				row = append(row, "PULL")
				types = append(types, cellDim)
			} else if result, exists := r.Steps[step]; exists {
				switch result {
				case StepSuccess:
					row = append(row, "OK")
					types = append(types, cellGreen)
				case StepFailure:
					row = append(row, "FAIL")
					types = append(types, cellRed)
				default:
					row = append(row, "UNWN")
					types = append(types, cellDim)
				}
			} else if isOptional {
				row = append(row, "..")
				types = append(types, cellDim)
			} else if isStepExpectedForJob(step, pd.groupResults) {
				row = append(row, "FAIL")
				types = append(types, cellRed)
			} else {
				row = append(row, "--")
				types = append(types, cellDim)
			}
		}
		rows[ri] = row
		cellTypes[ri] = types
	}

	// Convert rows to [][]string for table.Rows
	rowSlices := make([][]string, len(rows))
	copy(rowSlices, rows)

	// Build border style
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle).
		BorderHeader(true).
		BorderColumn(true).
		BorderRow(false).
		Headers(headers...).
		Rows(rowSlices...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)

			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("6")).Align(lipgloss.Center)
			}

			if col == 0 {
				return base
			}

			// Data cells — center-aligned
			base = base.Align(lipgloss.Center)
			if row >= 0 && row < len(cellTypes) && col < len(cellTypes[row]) {
				switch cellTypes[row][col] {
				case cellGreen:
					return base.Foreground(lipgloss.Color("2"))
				case cellRed:
					return base.Foreground(lipgloss.Color("1")).Bold(true)
				case cellDim:
					return base.Faint(true)
				}
			}
			return base
		})

	fmt.Println(t)
	fmt.Println()

	// Print summary line
	total := len(pd.results)
	steps := len(visibleSteps)
	fmt.Println(styleDim.Render(fmt.Sprintf("  %d columns x %d steps", total, steps)))

	// Print key
	fmt.Println(styleDim.Render(strings.Join([]string{
		"  ",
		styleGreen.Render("OK") + "=success  ",
		styleRed.Bold(true).Render("FAIL") + "=failure  ",
		styleDim.Render("UNWN") + "=unknown  ",
		styleDim.Render("..") + "=optional  ",
		styleDim.Render("--") + "=n/a  ",
		styleDim.Render("PULL") + "=not pulled",
	}, "")))

	fmt.Println()
}
