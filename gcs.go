package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

type gcsClient struct {
	client    *storage.Client
	cfg       *Config
	apiCalls  int64
	fileLog   *logrus.Logger
	logFile   *os.File
	startTime time.Time
}

func (g *gcsClient) logCall(op, path string, d time.Duration) {
	atomic.AddInt64(&g.apiCalls, 1)
	if g.fileLog != nil {
		g.fileLog.WithFields(logrus.Fields{
			"op":   op,
			"path": path,
			"ms":   d.Milliseconds(),
		}).Trace("gcs_call")
	}
}

func (g *gcsClient) CallCount() int64 {
	return atomic.LoadInt64(&g.apiCalls)
}

func newGCSClient(ctx context.Context, cfg *Config) (*gcsClient, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating storage client: %w", err)
	}

	gc := &gcsClient{client: client, cfg: cfg, startTime: time.Now()}

	logDir := defaultDataDir()
	logPath := filepath.Join(logDir, "gcs.log")
	if err := os.MkdirAll(logDir, 0755); err == nil {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			gc.logFile = f
			gc.fileLog = logrus.New()
			gc.fileLog.SetOutput(f)
			gc.fileLog.SetLevel(logrus.TraceLevel)
			gc.fileLog.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
			gc.fileLog.WithFields(logrus.Fields{
				"bucket": cfg.Bucket,
				"prefix": cfg.Prefix,
			}).Info("gcs_session_start")
		} else {
			logrus.WithError(err).Debug("failed to open GCS log file")
		}
	}

	return gc, nil
}

func (g *gcsClient) close() {
	if g.fileLog != nil {
		g.fileLog.WithFields(logrus.Fields{
			"calls":      g.CallCount(),
			"elapsed_ms": time.Since(g.startTime).Milliseconds(),
		}).Info("gcs_session_end")
	}
	if g.logFile != nil {
		g.logFile.Close()
	}
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

	t0 := time.Now()
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
	g.logCall("listJobs", prefix, time.Since(t0))
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

	t0 := time.Now()
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
	g.logCall("listRuns", prefix, time.Since(t0))
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

// listSteps discovers steps and their results by listing all objects under the
// variant directory and reading finished.json for each step.
func (g *gcsClient) listSteps(ctx context.Context, job, runID string) (map[string]StepResult, map[string][]string, string, error) {
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

	t0 := time.Now()
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
				break
			}
		}
	}
	g.logCall("listArtifacts", artifactsPrefix, time.Since(t0))

	if variantDir == "" {
		logrus.WithFields(logrus.Fields{"job": job, "run": runID, "api_calls": g.CallCount()}).Debug("listSteps: no variant found")
		return make(map[string]StepResult), make(map[string][]string), "", nil
	}

	// Recursive list of all objects under variant (no delimiter)
	stepsPrefix := artifactsPrefix + variantDir + "/"
	query = &storage.Query{Prefix: stepsPrefix}

	t1 := time.Now()
	steps := make(map[string]StepResult)
	stepDirs := make(map[string][]string)
	noRecurseSet := make(map[string]bool)
	for _, s := range g.cfg.NoRecurseSteps {
		noRecurseSet[s] = true
	}

	// Track which steps have finished.json and collect no-recurse children
	var finishedPaths []string

	it = bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, nil, "", fmt.Errorf("listing steps for %s/%s: %w", job, runID, err)
		}

		relPath := strings.TrimPrefix(attrs.Name, stepsPrefix)
		if relPath == "" {
			continue
		}

		parts := strings.SplitN(relPath, "/", 2)
		stepName := parts[0]

		// Register step if not seen yet
		if _, exists := steps[stepName]; !exists {
			steps[stepName] = StepUnknown
		}

		if len(parts) == 2 {
			subPath := parts[1]

			// Track finished.json for reading
			if subPath == "finished.json" {
				finishedPaths = append(finishedPaths, attrs.Name)
			}

			// Collect no-recurse step children (immediate level only)
			if noRecurseSet[stepName] {
				childParts := strings.SplitN(subPath, "/", 2)
				childName := childParts[0]
				if len(childParts) == 2 {
					childName += "/"
				}
				// Deduplicate children
				found := false
				for _, c := range stepDirs[stepName] {
					if c == childName {
						found = true
						break
					}
				}
				if !found {
					stepDirs[stepName] = append(stepDirs[stepName], childName)
				}
			}
		}
	}
	g.logCall("listSteps", stepsPrefix, time.Since(t1))

	// Read finished.json files concurrently to get step results
	type stepResultPair struct {
		step   string
		result StepResult
	}
	resultCh := make(chan stepResultPair, len(finishedPaths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, g.cfg.Concurrency)

	for _, objName := range finishedPaths {
		// Extract step name from path
		relPath := strings.TrimPrefix(objName, stepsPrefix)
		stepName := strings.SplitN(relPath, "/", 2)[0]

		wg.Add(1)
		go func(name, step string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := g.readFinishedJSON(ctx, name)
			resultCh <- stepResultPair{step, result}
		}(objName, stepName)
	}
	wg.Wait()
	close(resultCh)

	for pair := range resultCh {
		steps[pair.step] = pair.result
	}

	logrus.WithFields(logrus.Fields{
		"job": job, "run": runID, "variant": variantDir,
		"steps": len(steps), "no_recurse": len(stepDirs), "api_calls": g.CallCount(),
	}).Debug("listSteps complete")
	return steps, stepDirs, variantDir, nil
}

// readFinishedJSON reads a finished.json object and returns the step result.
func (g *gcsClient) readFinishedJSON(ctx context.Context, objectName string) StepResult {
	t0 := time.Now()
	reader, err := g.client.Bucket(g.cfg.Bucket).Object(objectName).NewReader(ctx)
	if err != nil {
		g.logCall("readFinished:err", objectName, time.Since(t0))
		logrus.WithError(err).WithField("object", objectName).Debug("failed to read finished.json")
		return StepUnknown
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	g.logCall("readFinished", objectName, time.Since(t0))
	if err != nil {
		return StepUnknown
	}

	var finished struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(data, &finished); err != nil {
		return StepUnknown
	}

	switch finished.Result {
	case "SUCCESS":
		return StepSuccess
	case "FAILURE":
		return StepFailure
	default:
		return StepUnknown
	}
}

// listImmediateChildren lists files and directories at the given prefix (one level).
func (g *gcsClient) listImmediateChildren(ctx context.Context, prefix string) ([]string, error) {
	bucket := g.client.Bucket(g.cfg.Bucket)
	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	}

	t0 := time.Now()
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
	g.logCall("listChildren", prefix, time.Since(t0))
	return children, nil
}
