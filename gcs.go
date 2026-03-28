package main

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type gcsClient struct {
	client *storage.Client
	cfg    *Config
}

func newGCSClient(ctx context.Context, cfg *Config) (*gcsClient, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating storage client: %w", err)
	}
	return &gcsClient{client: client, cfg: cfg}, nil
}

func (g *gcsClient) close() {
	g.client.Close()
}

// listJobs returns job directory names matching the configured pattern.
func (g *gcsClient) listJobs(ctx context.Context) ([]string, error) {
	prefix := g.cfg.Prefix + "/" + g.cfg.JobPattern
	bucket := g.client.Bucket(g.cfg.Bucket)

	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	}

	var jobs []string
	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing jobs: %w", err)
		}
		if attrs.Prefix != "" {
			// Extract job name from prefix like "logs/periodic-ci-.../""
			name := strings.TrimPrefix(attrs.Prefix, g.cfg.Prefix+"/")
			name = strings.TrimSuffix(name, "/")
			jobs = append(jobs, name)
		}
	}
	return jobs, nil
}

// listRuns returns run IDs for a given job.
func (g *gcsClient) listRuns(ctx context.Context, job string) ([]string, error) {
	prefix := g.cfg.Prefix + "/" + job + "/"
	bucket := g.client.Bucket(g.cfg.Bucket)

	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	}

	var runs []string
	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing runs for %s: %w", job, err)
		}
		if attrs.Prefix != "" {
			runID := strings.TrimPrefix(attrs.Prefix, prefix)
			runID = strings.TrimSuffix(runID, "/")
			if runID != "" {
				runs = append(runs, runID)
			}
		}
	}
	return runs, nil
}

// listSteps returns a map of step names to existence, and for no-recurse steps,
// a map of step names to their immediate children.
// Also returns the variant directory name.
func (g *gcsClient) listSteps(ctx context.Context, job, runID string) (map[string]bool, map[string][]string, string, error) {
	// First, find the variant directory under artifacts/
	artifactsPrefix := g.cfg.Prefix + "/" + job + "/" + runID + "/artifacts/"
	bucket := g.client.Bucket(g.cfg.Bucket)

	query := &storage.Query{
		Prefix:    artifactsPrefix,
		Delimiter: "/",
	}

	ignoreSet := make(map[string]bool)
	for _, d := range g.cfg.IgnoreArtifactDirs {
		ignoreSet[d] = true
	}

	var variantDir string
	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, nil, "", fmt.Errorf("listing artifacts for %s/%s: %w", job, runID, err)
		}
		if attrs.Prefix != "" {
			dirName := strings.TrimPrefix(attrs.Prefix, artifactsPrefix)
			dirName = strings.TrimSuffix(dirName, "/")
			if !ignoreSet[dirName] {
				variantDir = dirName
				break // Use the first non-ignored directory as the variant
			}
		}
	}

	if variantDir == "" {
		return make(map[string]bool), make(map[string][]string), "", nil
	}

	// List step directories under the variant
	stepsPrefix := artifactsPrefix + variantDir + "/"
	query = &storage.Query{
		Prefix:    stepsPrefix,
		Delimiter: "/",
	}

	steps := make(map[string]bool)
	stepDirs := make(map[string][]string)
	noRecurseSet := make(map[string]bool)
	for _, s := range g.cfg.NoRecurseSteps {
		noRecurseSet[s] = true
	}

	it = bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, nil, "", fmt.Errorf("listing steps for %s/%s: %w", job, runID, err)
		}
		if attrs.Prefix != "" {
			stepName := strings.TrimPrefix(attrs.Prefix, stepsPrefix)
			stepName = strings.TrimSuffix(stepName, "/")
			steps[stepName] = true
		}
	}

	// For no-recurse steps, list their immediate children
	for stepName := range steps {
		if noRecurseSet[stepName] {
			children, err := g.listImmediateChildren(ctx, stepsPrefix+stepName+"/")
			if err != nil {
				fmt.Printf("  Warning: error listing children of %s: %v\n", stepName, err)
				continue
			}
			stepDirs[stepName] = children
		}
	}

	return steps, stepDirs, variantDir, nil
}

// listImmediateChildren lists files and directories at the given prefix (one level).
func (g *gcsClient) listImmediateChildren(ctx context.Context, prefix string) ([]string, error) {
	bucket := g.client.Bucket(g.cfg.Bucket)
	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	}

	var children []string
	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			name := strings.TrimPrefix(attrs.Prefix, prefix)
			name = strings.TrimSuffix(name, "/")
			children = append(children, name+"/")
		} else if attrs.Name != "" {
			name := strings.TrimPrefix(attrs.Name, prefix)
			children = append(children, name)
		}
	}
	return children, nil
}
