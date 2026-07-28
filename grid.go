package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type switchToBrowseMsg struct {
	result   RunResult
	stepName string
}

type appMode int

const (
	modeGrid appMode = iota
	modeBrowse
)

// appModel wraps gridModel and browseModel, routing messages to the active child.
type appModel struct {
	mode      appMode
	grid      gridModel
	browse    browseModel
	gcsClient *gcsClient
	cfg       *Config
	db        *DB
	outputDir string
}

func (m appModel) Init() tea.Cmd {
	return m.grid.Init()
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.mode == modeGrid {
			gm, cmd := m.grid.Update(msg)
			m.grid = gm.(gridModel)
			return m, cmd
		}
		bm, cmd := m.browse.Update(msg)
		m.browse = bm.(browseModel)
		return m, cmd

	case switchToBrowseMsg:
		result := msg.result
		if !result.Pulled {
			m.grid.status = fmt.Sprintf("pulling step data for %s...", result.RunID)
			return m, m.pullAndBrowse(result)
		}
		browse := newBrowseModel(m.gcsClient, m.cfg, result, m.outputDir)
		browse.embeddedMode = true
		browse.width = m.grid.width
		browse.height = m.grid.height
		m.browse = browse
		m.mode = modeBrowse
		return m, nil

	case pullDoneMsg:
		if msg.err != nil {
			m.grid.status = "pull error: " + msg.err.Error()
			return m, nil
		}
		browse := newBrowseModel(m.gcsClient, m.cfg, msg.result, m.outputDir)
		browse.embeddedMode = true
		browse.width = m.grid.width
		browse.height = m.grid.height
		m.browse = browse
		m.mode = modeBrowse
		return m, nil

	case switchToGridMsg:
		m.mode = modeGrid
		m.grid.status = "returned from browse"
		return m, nil
	}

	if m.mode == modeGrid {
		gm, cmd := m.grid.Update(msg)
		m.grid = gm.(gridModel)
		return m, cmd
	}
	bm, cmd := m.browse.Update(msg)
	m.browse = bm.(browseModel)
	return m, cmd
}

func (m appModel) View() string {
	if m.mode == modeBrowse {
		return m.browse.View()
	}
	return m.grid.View()
}

type pullDoneMsg struct {
	result RunResult
	err    error
}

func (m appModel) pullAndBrowse(result RunResult) tea.Cmd {
	return func() tea.Msg {
		steps, stepDirs, variant, err := m.gcsClient.listSteps(context.Background(), result.Job, result.RunID)
		if err != nil {
			return pullDoneMsg{err: err}
		}
		result.Steps = steps
		result.StepDirs = stepDirs
		result.VariantID = variant
		result.Pulled = true
		if m.db != nil {
			_ = m.db.StoreResults([]RunResult{result})
		}
		return pullDoneMsg{result: result}
	}
}

// gridModel is the interactive grid TUI with 2D cursor and sliding window.
type gridModel struct {
	groups       []groupData
	currentGroup int

	// Sliding window (horizontal — runs)
	windowStart int
	windowSize  int

	// 2D cursor
	cursorRow int
	cursorCol int

	// Vertical scroll (steps)
	stepOffset int

	cfg             *Config
	db              *DB
	gcsClient       *gcsClient
	outputDir       string
	groupByPlatform bool

	width, height int
	status        string
	quitting      bool
}

const gridHeaderLines = 3 // title + blank + panel border
const gridFooterLines = 5 // position + status + panel border + summary + key
const gridLegendLines = 3 // "Legend:" header + blank before + blank after
const gridTableChrome = 6 // table border top/bottom + header row + header border + blank lines

func newGridModel(groups []groupData, cfg *Config, db *DB, gcs *gcsClient, outputDir string, groupByPlatform bool) gridModel {
	m := gridModel{
		groups:          groups,
		cfg:             cfg,
		db:              db,
		gcsClient:       gcs,
		outputDir:       outputDir,
		groupByPlatform: groupByPlatform,
		width:           80,
		height:          24,
		status:          "↑↓←→ navigate  Enter browse  Tab group  q quit",
	}
	m.computeWindowSize()
	// Start at newest run (rightmost)
	g := m.currentGroupData()
	if g != nil && len(g.results) > 0 {
		m.windowStart = len(g.results) - m.windowSize
		if m.windowStart < 0 {
			m.windowStart = 0
		}
		m.cursorCol = m.windowSize - 1
	}
	return m
}

func (m *gridModel) currentGroupData() *groupData {
	if m.currentGroup >= len(m.groups) {
		return nil
	}
	return &m.groups[m.currentGroup]
}

func (m *gridModel) computeWindowSize() {
	g := m.currentGroupData()
	if g == nil {
		return
	}

	stepColWidth := m.maxStepNameLen() + 4
	colWidth := 8
	avail := m.width - stepColWidth - 4 // borders/padding
	if avail < colWidth {
		avail = colWidth
	}
	m.windowSize = avail / colWidth
	if m.windowSize < 1 {
		m.windowSize = 1
	}
	// Cap columns so legend doesn't consume all vertical space
	maxCols := (m.height - gridHeaderLines - gridFooterLines - gridTableChrome - gridLegendLines) / 2
	if maxCols < 3 {
		maxCols = 3
	}
	if m.windowSize > maxCols {
		m.windowSize = maxCols
	}
	if m.windowSize > len(g.results) {
		m.windowSize = len(g.results)
	}

	m.clampWindow()
}

func (m *gridModel) visibleStepCount() int {
	g := m.currentGroupData()
	if g == nil {
		return 0
	}
	legendRows := len(m.windowResults()) + gridLegendLines
	chrome := gridHeaderLines + gridFooterLines + legendRows + gridTableChrome
	avail := m.height - chrome
	if avail < 1 {
		avail = 1
	}
	total := len(g.stepNames)
	if avail > total {
		avail = total
	}
	return avail
}

func (m *gridModel) maxStepNameLen() int {
	g := m.currentGroupData()
	if g == nil {
		return 0
	}
	max := 0
	for _, s := range g.stepNames {
		if len(s) > max {
			max = len(s)
		}
	}
	return max
}

func (m *gridModel) windowResults() []RunResult {
	g := m.currentGroupData()
	if g == nil {
		return nil
	}
	end := m.windowStart + m.windowSize
	if end > len(g.results) {
		end = len(g.results)
	}
	return g.results[m.windowStart:end]
}

func (m *gridModel) windowEmojis() []string {
	g := m.currentGroupData()
	if g == nil {
		return nil
	}
	end := m.windowStart + m.windowSize
	if end > len(g.emojis) {
		end = len(g.emojis)
	}
	return g.emojis[m.windowStart:end]
}

func (m *gridModel) clampWindow() {
	g := m.currentGroupData()
	if g == nil {
		return
	}
	maxStart := len(g.results) - m.windowSize
	if maxStart < 0 {
		maxStart = 0
	}
	if m.windowStart > maxStart {
		m.windowStart = maxStart
	}
	if m.windowStart < 0 {
		m.windowStart = 0
	}

	maxCol := m.windowSize - 1
	if maxCol < 0 {
		maxCol = 0
	}
	if m.cursorCol > maxCol {
		m.cursorCol = maxCol
	}

	totalSteps := len(g.stepNames)
	if m.cursorRow >= totalSteps {
		m.cursorRow = totalSteps - 1
	}
	if m.cursorRow < 0 {
		m.cursorRow = 0
	}

	vis := m.visibleStepCount()
	if m.stepOffset+vis > totalSteps {
		m.stepOffset = totalSteps - vis
	}
	if m.stepOffset < 0 {
		m.stepOffset = 0
	}
}

func (m *gridModel) scrollToCursor() {
	vis := m.visibleStepCount()
	if m.cursorRow < m.stepOffset {
		m.stepOffset = m.cursorRow
	}
	if m.cursorRow >= m.stepOffset+vis {
		m.stepOffset = m.cursorRow - vis + 1
	}
	if m.stepOffset < 0 {
		m.stepOffset = 0
	}
}

func (m gridModel) Init() tea.Cmd {
	return nil
}

func (m gridModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.computeWindowSize()
		return m, nil

	case tea.KeyMsg:
		g := m.currentGroupData()
		if g == nil {
			return m, nil
		}

		switch msg.String() {
		case "q", "esc":
			m.quitting = true
			return m, tea.Quit

		case "down", "j":
			if m.cursorRow < len(g.stepNames)-1 {
				m.cursorRow++
				m.scrollToCursor()
			}

		case "up", "k":
			if m.cursorRow > 0 {
				m.cursorRow--
				m.scrollToCursor()
			}

		case "right", "l":
			if m.cursorCol < m.windowSize-1 {
				m.cursorCol++
			} else if m.windowStart+m.windowSize < len(g.results) {
				m.windowStart++
			}

		case "left", "h":
			if m.cursorCol > 0 {
				m.cursorCol--
			} else if m.windowStart > 0 {
				m.windowStart--
			}

		case "pgdown", "ctrl+d":
			vis := m.visibleStepCount()
			m.cursorRow += vis
			if m.cursorRow >= len(g.stepNames) {
				m.cursorRow = len(g.stepNames) - 1
			}
			m.scrollToCursor()

		case "pgup", "ctrl+u":
			vis := m.visibleStepCount()
			m.cursorRow -= vis
			if m.cursorRow < 0 {
				m.cursorRow = 0
			}
			m.scrollToCursor()

		case "g", "home":
			m.cursorRow = 0
			m.scrollToCursor()

		case "G", "end":
			m.cursorRow = len(g.stepNames) - 1
			m.scrollToCursor()

		case "0":
			m.cursorCol = 0
			m.windowStart = 0

		case "$":
			m.windowStart = len(g.results) - m.windowSize
			if m.windowStart < 0 {
				m.windowStart = 0
			}
			m.cursorCol = m.windowSize - 1
			actualEnd := len(g.results) - m.windowStart
			if m.cursorCol >= actualEnd {
				m.cursorCol = actualEnd - 1
			}

		case "tab":
			if len(m.groups) > 1 {
				m.currentGroup = (m.currentGroup + 1) % len(m.groups)
				m.cursorRow = 0
				m.cursorCol = 0
				m.windowStart = 0
				m.stepOffset = 0
				m.computeWindowSize()
				m.status = fmt.Sprintf("group: %s", displayGroupName(m.groups[m.currentGroup].platform))
			}

		case "shift+tab":
			if len(m.groups) > 1 {
				m.currentGroup = (m.currentGroup - 1 + len(m.groups)) % len(m.groups)
				m.cursorRow = 0
				m.cursorCol = 0
				m.windowStart = 0
				m.stepOffset = 0
				m.computeWindowSize()
				m.status = fmt.Sprintf("group: %s", displayGroupName(m.groups[m.currentGroup].platform))
			}

		case "enter":
			results := m.windowResults()
			if m.cursorCol < len(results) {
				stepName := ""
				if m.cursorRow < len(g.stepNames) {
					stepName = g.stepNames[m.cursorRow]
				}
				return m, func() tea.Msg {
					return switchToBrowseMsg{
						result:   results[m.cursorCol],
						stepName: stepName,
					}
				}
			}
		}
	}

	return m, nil
}

func (m gridModel) View() string {
	if m.quitting {
		return ""
	}

	g := m.currentGroupData()
	if g == nil {
		return "no data"
	}

	var b strings.Builder

	// Header
	title := fmt.Sprintf("%d run(s)", len(g.results))
	if m.groupByPlatform {
		title = displayGroupName(g.platform) + " — " + title
	}
	if len(m.groups) > 1 {
		title += fmt.Sprintf("  [group %d/%d]", m.currentGroup+1, len(m.groups))
	}
	b.WriteString(browseTitleStyle.Render(title))
	b.WriteString("\n\n")

	// Build pageData for visible window
	results := m.windowResults()
	emojis := m.windowEmojis()

	vis := m.visibleStepCount()
	stepEnd := m.stepOffset + vis
	if stepEnd > len(g.stepNames) {
		stepEnd = len(g.stepNames)
	}
	visibleSteps := g.stepNames[m.stepOffset:stepEnd]

	pd := pageData{
		platform:     g.platform,
		pageNum:      1,
		totalPages:   1,
		results:      results,
		emojis:       emojis,
		stepNames:    visibleSteps,
		groupResults: g.results,
		optionalSet:  g.optionalSet,
		showURLs:     false,
	}

	highlightRow := m.cursorRow - m.stepOffset
	highlightCol := m.cursorCol

	tableStr := renderTablePageString(pd, m.cfg, false, highlightRow, highlightCol)
	b.WriteString(tableStr)

	// Position indicator
	runPos := fmt.Sprintf("run %d/%d", m.windowStart+m.cursorCol+1, len(g.results))
	stepPos := fmt.Sprintf("step %d/%d", m.cursorRow+1, len(g.stepNames))
	posLine := fmt.Sprintf("  [%s  %s]", runPos, stepPos)
	b.WriteString(browseStatusStyle.Render(posLine))
	b.WriteString("\n")

	// Status / help
	b.WriteString(browseStatusStyle.Render("  " + m.status))
	b.WriteString("\n")

	panelW := m.width - 2
	if panelW < 40 {
		panelW = 40
	}
	panelH := m.height - 2
	if panelH < 10 {
		panelH = 10
	}
	return browsePanelStyle.Width(panelW).Height(panelH).Render(b.String())
}

func runInteractiveGrid(results []RunResult, cfg *Config, db *DB, groupByPlatform bool, showURLs bool) error {
	groups := buildGroupedResults(results, cfg, groupByPlatform, showURLs)
	if len(groups) == 0 {
		fmt.Println("no data to display")
		return nil
	}

	ctx := context.Background()
	gcs, err := newGCSClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer gcs.close()

	outputDir := cfg.DownloadDir

	grid := newGridModel(groups, cfg, db, gcs, outputDir, groupByPlatform)

	app := appModel{
		mode:      modeGrid,
		grid:      grid,
		gcsClient: gcs,
		cfg:       cfg,
		db:        db,
		outputDir: outputDir,
	}

	p := tea.NewProgram(app)
	_, err = p.Run()
	return err
}
