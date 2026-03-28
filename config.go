package main

import (
	"encoding/json"
	"os"
)

type Config struct {
	Bucket            string   `json:"bucket"`
	Prefix            string   `json:"prefix"`
	JobPattern        string   `json:"job_pattern"`
	NoRecurseSteps    []string `json:"no_recurse_steps"`
	IgnoreArtifactDirs []string `json:"ignore_artifact_dirs"`
	StepOrder         []string `json:"step_order"`
	EmojiPalette      string   `json:"emoji_palette"`
	MaxRunsPerJob     int      `json:"max_runs_per_job"`
	Concurrency       int      `json:"concurrency"`
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

	return &cfg, nil
}
