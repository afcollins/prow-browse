package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type treeNode struct {
	name     string
	gcsPath  string
	isDir    bool
	expanded bool
	checked  bool
	children []*treeNode
	loaded   bool
	depth    int
	result   StepResult
}

type browseModel struct {
	root      []*treeNode
	flat      []*treeNode
	cursor    int
	offset    int // viewport scroll offset
	height    int // terminal height
	width     int
	gcs       *gcsClient
	cfg       *Config
	title     string // header display text
	gcsPrefix string // root GCS path for relative download paths
	outputDir string
	status    string
	quitting  bool
	searching bool
	searchBuf string
}

type listDirDoneMsg struct {
	node    *treeNode
	entries []dirEntry
	err     error
}

type downloadDoneMsg struct {
	count int
	err   error
}

const headerLines = 2 // "Browse: ..." + blank line
const footerLines = 2 // blank line + status
const helpText = "↑/↓ navigate  →/Enter expand  ← collapse  Space check  / search  d download  q quit"

func newBrowseModel(gcs *gcsClient, cfg *Config, result RunResult, outputDir string) browseModel {
	var root []*treeNode

	variantPrefix := cfg.Prefix + "/" + result.Job + "/" + result.RunID + "/artifacts/"
	if result.VariantID != "" {
		variantPrefix += result.VariantID + "/"
	}

	buildLogPath := cfg.Prefix + "/" + result.Job + "/" + result.RunID + "/build-log.txt"
	root = append(root, &treeNode{
		name:    "build-log.txt",
		gcsPath: buildLogPath,
		isDir:   false,
		depth:   0,
	})

	var stepNames []string
	for s := range result.Steps {
		stepNames = append(stepNames, s)
	}
	sort.Strings(stepNames)

	for _, step := range stepNames {
		node := &treeNode{
			name:    step,
			gcsPath: variantPrefix + step,
			isDir:   true,
			depth:   0,
			result:  result.Steps[step],
		}
		if children, ok := result.StepDirs[step]; ok && len(children) > 0 {
			for _, c := range children {
				child := &treeNode{
					name:    strings.TrimSuffix(c, "/"),
					gcsPath: variantPrefix + step + "/" + strings.TrimSuffix(c, "/"),
					isDir:   strings.HasSuffix(c, "/"),
					depth:   1,
				}
				node.children = append(node.children, child)
			}
		}
		root = append(root, node)
	}

	m := browseModel{
		root:      root,
		gcs:       gcs,
		cfg:       cfg,
		title:     shortJobName(result.Job, cfg) + " / " + result.RunID,
		gcsPrefix: cfg.Prefix + "/" + result.Job + "/" + result.RunID,
		outputDir: outputDir,
		height:    24,
		status:    helpText,
	}
	m.rebuildFlat()
	return m
}

func newBrowseModelFromPath(gcs *gcsClient, cfg *Config, gcsPath, outputDir string) (browseModel, error) {
	gcsPath = strings.TrimSuffix(gcsPath, "/")

	entries, err := gcs.listDir(context.Background(), gcsPath)
	if err != nil {
		return browseModel{}, err
	}

	var root []*treeNode
	for _, e := range entries {
		root = append(root, &treeNode{
			name:    e.Name,
			gcsPath: gcsPath + "/" + e.Name,
			isDir:   e.IsDir,
			depth:   0,
		})
	}

	// Use last path segment as title
	parts := strings.Split(gcsPath, "/")
	title := parts[len(parts)-1]

	m := browseModel{
		root:      root,
		gcs:       gcs,
		cfg:       cfg,
		title:     title,
		gcsPrefix: gcsPath,
		outputDir: outputDir,
		height:    24,
		status:    helpText,
	}
	m.rebuildFlat()
	return m, nil
}

func (m *browseModel) rebuildFlat() {
	m.flat = nil
	var walk func(nodes []*treeNode)
	walk = func(nodes []*treeNode) {
		for _, n := range nodes {
			m.flat = append(m.flat, n)
			if n.isDir && n.expanded {
				walk(n.children)
			}
		}
	}
	walk(m.root)
}

func (m *browseModel) visibleRows() int {
	rows := m.height - headerLines - footerLines
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m *browseModel) scrollToCursor() {
	vis := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+vis {
		m.offset = m.cursor - vis + 1
	}
}

func (m *browseModel) findParentIndex(idx int) int {
	if idx >= len(m.flat) {
		return -1
	}
	childDepth := m.flat[idx].depth
	for i := idx - 1; i >= 0; i-- {
		if m.flat[i].isDir && m.flat[i].depth < childDepth {
			return i
		}
	}
	return -1
}

func (m browseModel) Init() tea.Cmd {
	return nil
}

func (m browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		m.scrollToCursor()
		return m, nil

	case tea.KeyMsg:
		if m.searching {
			return m.updateSearch(msg)
		}

		switch msg.String() {
		case "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.scrollToCursor()
			}
		case "down", "j":
			if m.cursor < len(m.flat)-1 {
				m.cursor++
				m.scrollToCursor()
			}
		case "pgup":
			m.cursor -= m.visibleRows()
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.scrollToCursor()
		case "pgdown":
			m.cursor += m.visibleRows()
			if m.cursor >= len(m.flat) {
				m.cursor = len(m.flat) - 1
			}
			m.scrollToCursor()
		case "home", "g":
			m.cursor = 0
			m.scrollToCursor()
		case "end", "G":
			m.cursor = len(m.flat) - 1
			m.scrollToCursor()
		case "right", "enter":
			if m.cursor < len(m.flat) {
				node := m.flat[m.cursor]
				if node.isDir && !node.expanded {
					node.expanded = true
					if !node.loaded {
						m.status = "loading " + node.name + "..."
						return m, m.loadDir(node)
					}
					m.rebuildFlat()
				}
			}
		case "left":
			if m.cursor < len(m.flat) {
				node := m.flat[m.cursor]
				if node.isDir && node.expanded {
					node.expanded = false
					m.rebuildFlat()
					m.scrollToCursor()
				} else if node.depth > 0 {
					if pi := m.findParentIndex(m.cursor); pi >= 0 {
						m.cursor = pi
						m.flat[pi].expanded = false
						m.rebuildFlat()
						m.scrollToCursor()
					}
				}
			}
		case " ":
			if m.cursor < len(m.flat) {
				node := m.flat[m.cursor]
				if !node.isDir {
					node.checked = !node.checked
				}
			}
		case "/":
			m.searching = true
			m.searchBuf = ""
			m.status = "search: "
			return m, nil
		case "n":
			m.searchNext(1)
		case "N":
			m.searchNext(-1)
		case "d":
			checked := m.checkedFiles()
			if len(checked) == 0 {
				m.status = "no files checked"
				return m, nil
			}
			m.status = fmt.Sprintf("downloading %d files...", len(checked))
			return m, m.downloadFiles(checked)
		}

	case listDirDoneMsg:
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			return m, nil
		}
		msg.node.children = nil
		for _, e := range msg.entries {
			child := &treeNode{
				name:    e.Name,
				gcsPath: msg.node.gcsPath + "/" + e.Name,
				isDir:   e.IsDir,
				depth:   msg.node.depth + 1,
			}
			msg.node.children = append(msg.node.children, child)
		}
		msg.node.loaded = true
		m.rebuildFlat()
		m.scrollToCursor()
		m.status = fmt.Sprintf("loaded %d entries in %s/", len(msg.entries), msg.node.name)

	case downloadDoneMsg:
		if msg.err != nil {
			m.status = "download error: " + msg.err.Error()
		} else {
			m.status = fmt.Sprintf("downloaded %d files to %s", msg.count, m.outputDir)
		}
	}

	return m, nil
}

func (m *browseModel) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searching = false
		if m.searchBuf != "" {
			m.searchNext(1)
		} else {
			m.status = helpText
		}
	case "esc":
		m.searching = false
		m.searchBuf = ""
		m.status = helpText
	case "backspace":
		if len(m.searchBuf) > 0 {
			m.searchBuf = m.searchBuf[:len(m.searchBuf)-1]
		}
		m.status = "search: " + m.searchBuf
	default:
		if len(msg.String()) == 1 {
			m.searchBuf += msg.String()
		}
		m.status = "search: " + m.searchBuf
	}
	return m, nil
}

func (m *browseModel) searchNext(dir int) {
	if m.searchBuf == "" {
		return
	}
	query := strings.ToLower(m.searchBuf)
	n := len(m.flat)
	for i := 1; i <= n; i++ {
		idx := (m.cursor + i*dir + n) % n
		if strings.Contains(strings.ToLower(m.flat[idx].name), query) {
			m.cursor = idx
			m.scrollToCursor()
			m.status = fmt.Sprintf("/%s  (n/N next/prev)", m.searchBuf)
			return
		}
	}
	m.status = fmt.Sprintf("/%s  (not found)", m.searchBuf)
}

func (m browseModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	header := fmt.Sprintf(" Browse: %s\n\n", m.title)
	b.WriteString(header)

	vis := m.visibleRows()
	end := m.offset + vis
	if end > len(m.flat) {
		end = len(m.flat)
	}

	for i := m.offset; i < end; i++ {
		node := m.flat[i]
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}

		indent := strings.Repeat("  ", node.depth)

		var icon string
		if node.isDir {
			if node.expanded {
				icon = "▾ "
			} else {
				icon = "▸ "
			}
		} else {
			if node.checked {
				icon = "[x] "
			} else {
				icon = "[ ] "
			}
		}

		line := cursor + indent + icon + node.name
		if node.isDir {
			line += "/"
			if node.result != StepMissing {
				switch node.result {
				case StepSuccess:
					line += "  ✅"
				case StepFailure:
					line += "  ❌"
				case StepUnknown:
					line += "  👻"
				}
			}
		}
		b.WriteString(line + "\n")
	}

	// Scroll indicator
	if len(m.flat) > vis {
		pos := ""
		if m.offset == 0 {
			pos = "(top)"
		} else if end >= len(m.flat) {
			pos = "(end)"
		} else {
			pct := m.offset * 100 / (len(m.flat) - vis)
			pos = fmt.Sprintf("(%d%%)", pct)
		}
		fmt.Fprintf(&b, " -- %d items %s --\n", len(m.flat), pos)
	}

	b.WriteString("\n " + m.status + "\n")
	return b.String()
}

func (m browseModel) checkedFiles() []*treeNode {
	var checked []*treeNode
	var walk func(nodes []*treeNode)
	walk = func(nodes []*treeNode) {
		for _, n := range nodes {
			if !n.isDir && n.checked {
				checked = append(checked, n)
			}
			if n.isDir {
				walk(n.children)
			}
		}
	}
	walk(m.root)
	return checked
}

func (m browseModel) loadDir(node *treeNode) tea.Cmd {
	return func() tea.Msg {
		entries, err := m.gcs.listDir(context.Background(), node.gcsPath)
		return listDirDoneMsg{node: node, entries: entries, err: err}
	}
}

func (m browseModel) downloadFiles(files []*treeNode) tea.Cmd {
	return func() tea.Msg {
		baseDir := m.outputDir

		for _, f := range files {
			relPath := strings.TrimPrefix(f.gcsPath, m.gcsPrefix+"/")
			localPath := baseDir + "/" + relPath
			if err := m.gcs.downloadObject(context.Background(), f.gcsPath, localPath); err != nil {
				return downloadDoneMsg{err: err}
			}
		}
		return downloadDoneMsg{count: len(files)}
	}
}
