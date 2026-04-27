package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const appName = "prow-status"

type Config struct {
	Bucket            string   `json:"bucket"`
	Prefix            string   `json:"prefix"`
	JobPattern        string   `json:"job_pattern"`
	NoRecurseSteps    []string `json:"no_recurse_steps"`
	OptionalSteps     []string `json:"optional_steps"`
	IgnoreArtifactDirs []string `json:"ignore_artifact_dirs"`
	StepOrder         []string `json:"step_order"`
	EmojiPalette      string   `json:"emoji_palette"`
	MaxRunsPerJob     int      `json:"max_runs_per_job"`
	Concurrency       int      `json:"concurrency"`
	ColumnsPerPage    int      `json:"columns_per_page"`
}

// defaultConfigPath returns the config path, checking ./config.json first,
// then ~/.config/prow-status/config.json.
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

// defaultDBPath returns ~/.local/share/prow-status/prow-status.db.
func defaultDBPath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, appName, appName+".db")
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

	return &cfg, nil
}
