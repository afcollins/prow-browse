package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const appName = "prow-browse"

type JobSource struct {
	Name string `json:"name"`
	Path string `json:"path"` // bucket-relative GCS prefix, e.g. "logs/periodic-ci-openshift-eng-ocp-qe-perfscale"
}

// Base returns the directory prefix for GCS path construction.
// e.g. "logs/periodic-ci-foo" → "logs/", "pr-logs/pull/org/repo/123/rehearse-" → "pr-logs/pull/org/repo/123/"
func (s JobSource) Base() string {
	if i := strings.LastIndex(s.Path, "/"); i >= 0 {
		return s.Path[:i+1]
	}
	return ""
}

type Config struct {
	Bucket             string      `json:"bucket"`
	Sources            []JobSource `json:"sources,omitempty"`
	Prefix             string      `json:"prefix,omitempty"`
	JobPattern         string      `json:"job_pattern,omitempty"`
	NoRecurseSteps     []string    `json:"no_recurse_steps"`
	OptionalSteps      []string `json:"optional_steps"`
	IgnoreArtifactDirs []string `json:"ignore_artifact_dirs"`
	StepOrder          []string `json:"step_order"`
	EmojiPalette       string   `json:"emoji_palette"`
	MaxRunsPerJob      int      `json:"max_runs_per_job"`
	Concurrency        int      `json:"concurrency"`
	ColumnsPerPage     int      `json:"columns_per_page"`
	DownloadDir        string   `json:"download_dir"`
}

// defaultConfigPath returns the config path, checking ./config.json first,
// then ~/.config/prow-browse/config.json.
func defaultConfigPath() string {
	local := "config.json"
	if _, err := os.Stat(local); err == nil {
		return local
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, appName, "config.json")
}

func defaultDataDir() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, appName)
}

func defaultDBPath() string {
	return filepath.Join(defaultDataDir(), appName+".db")
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Defaults
	if cfg.MaxRunsPerJob == 0 {
		cfg.MaxRunsPerJob = 3
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 20
	}
	if cfg.ColumnsPerPage == 0 {
		cfg.ColumnsPerPage = 50
	}
	if cfg.DownloadDir == "" {
		home, _ := os.UserHomeDir()
		cfg.DownloadDir = filepath.Join(home, "Downloads", "prow")
	}

	if len(cfg.Sources) == 0 && cfg.Prefix != "" && cfg.JobPattern != "" {
		cfg.Sources = []JobSource{{
			Name: "default",
			Path: cfg.Prefix + "/" + cfg.JobPattern,
		}}
	}

	return &cfg, nil
}

// SourceBase returns the GCS base prefix for a source by name.
// Falls back to legacy Prefix for empty/unknown source names.
func (c *Config) SourceBase(name string) string {
	for _, s := range c.Sources {
		if s.Name == name {
			return s.Base()
		}
	}
	if c.Prefix != "" {
		return c.Prefix + "/"
	}
	return ""
}
