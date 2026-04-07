package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

type gcsClient struct {
	client   *storage.Client
	cfg      *Config
	apiCalls int64 // atomic counter of GCS list operations
}

func (g *gcsClient) incCalls() {
	atomic.AddInt64(&g.apiCalls, 1)
}

func (g *gcsClient) CallCount() int64 {
	return atomic.LoadInt64(&g.apiCalls)
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

	g.incCalls()
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
			name := strings.TrimPrefix(attrs.Prefix, g.cfg.Prefix+"/")
			name = strings.TrimSuffix(name, "/")
			jobs = append(jobs, name)
		}
	}
	logrus.WithFields(logrus.Fields{"jobs": len(jobs), "api_calls": g.CallCount()}).Debug("listJobs complete")
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

	g.incCalls()
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
	logrus.WithFields(logrus.Fields{"job": job, "runs": len(runs), "api_calls": g.CallCount()}).Debug("listRuns complete")
	return runs, nil
}

// findRunByID searches all jobs in GCS for a run matching the given ID suffix.
func (g *gcsClient) findRunByID(ctx context.Context, suffix string) (string, string, error) {
	jobs, err := g.listJobs(ctx)
	if err != nil {
		return "", "", err
	}
	logrus.WithField("jobs", len(jobs)).Info("searching GCS for run ID")

	type match struct{ job, runID string }
	var mu sync.Mutex
	var matches []match
	var wg sync.WaitGroup
	sem := make(chan struct{}, g.cfg.Concurrency)

	for _, job := range jobs {
		wg.Add(1)
		go func(j string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			runs, err := g.listRuns(ctx, j)
			if err != nil {
				logrus.WithError(err).WithField("job", j).Warn("failed to list runs")
				return
			}
			for _, r := range runs {
				if strings.HasSuffix(r, suffix) {
					mu.Lock()
					matches = append(matches, match{j, r})
					mu.Unlock()
				}
			}
		}(job)
	}
	wg.Wait()

	if len(matches) == 0 {
		return "", "", fmt.Errorf("no run found matching suffix %q in GCS", suffix)
	}
	if len(matches) > 1 {
		for _, m := range matches {
			logrus.WithFields(logrus.Fields{"job": m.job, "run_id": m.runID}).Warn("ambiguous match")
		}
		return "", "", fmt.Errorf("ambiguous: %d runs match suffix %q", len(matches), suffix)
	}
	return matches[0].job, matches[0].runID, nil
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

	g.incCalls()
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
		logrus.WithFields(logrus.Fields{"job": job, "run": runID, "api_calls": g.CallCount()}).Debug("listSteps: no variant found")
		return make(map[string]bool), make(map[string][]string), "", nil
	}

	// List step directories under the variant
	stepsPrefix := artifactsPrefix + variantDir + "/"
	query = &storage.Query{
		Prefix:    stepsPrefix,
		Delimiter: "/",
	}

	g.incCalls()
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
				logrus.WithError(err).WithField("step", stepName).Warn("failed to list step children")
				continue
			}
			stepDirs[stepName] = children
		}
	}

	logrus.WithFields(logrus.Fields{
		"job": job, "run": runID, "variant": variantDir,
		"steps": len(steps), "no_recurse": len(stepDirs), "api_calls": g.CallCount(),
	}).Debug("listSteps complete")
	return steps, stepDirs, variantDir, nil
}

// listImmediateChildren lists files and directories at the given prefix (one level).
func (g *gcsClient) listImmediateChildren(ctx context.Context, prefix string) ([]string, error) {
	bucket := g.client.Bucket(g.cfg.Bucket)
	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	}

	g.incCalls()
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
