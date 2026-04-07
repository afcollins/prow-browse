package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Run("defaults applied", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		os.WriteFile(path, []byte(`{"bucket":"test-bucket"}`), 0644)

		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.Bucket != "test-bucket" {
			t.Errorf("bucket = %q, want test-bucket", cfg.Bucket)
		}
		if cfg.MaxRunsPerJob != 3 {
			t.Errorf("max_runs_per_job = %d, want 3", cfg.MaxRunsPerJob)
		}
		if cfg.Concurrency != 20 {
			t.Errorf("concurrency = %d, want 20", cfg.Concurrency)
		}
		if cfg.ColumnsPerPage != 50 {
			t.Errorf("columns_per_page = %d, want 50", cfg.ColumnsPerPage)
		}
	})

	t.Run("explicit values override defaults", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		os.WriteFile(path, []byte(`{"bucket":"b","max_runs_per_job":10,"concurrency":5,"columns_per_page":25}`), 0644)

		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.MaxRunsPerJob != 10 {
			t.Errorf("max_runs_per_job = %d, want 10", cfg.MaxRunsPerJob)
		}
		if cfg.Concurrency != 5 {
			t.Errorf("concurrency = %d, want 5", cfg.Concurrency)
		}
		if cfg.ColumnsPerPage != 25 {
			t.Errorf("columns_per_page = %d, want 25", cfg.ColumnsPerPage)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := loadConfig("/nonexistent/config.json")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		os.WriteFile(path, []byte(`{invalid`), 0644)

		_, err := loadConfig(path)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}
