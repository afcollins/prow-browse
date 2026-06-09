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

type objectIterator interface {
	Next() (*storage.ObjectAttrs, error)
}

type bucketLister interface {
	Objects(ctx context.Context, q *storage.Query) objectIterator
}

type gcsBucketHandle struct {
	bkt *storage.BucketHandle
}

func (h *gcsBucketHandle) Objects(ctx context.Context, q *storage.Query) objectIterator {
	return h.bkt.Objects(ctx, q)
}

type gcsClient struct {
	client    *storage.Client
	cfg       *Config
	apiCalls  int64
	fileLog   *logrus.Logger
	logFile   *os.File
	startTime time.Time
}

func (g *gcsClient) bucket() bucketLister {
	return &gcsBucketHandle{bkt: g.client.Bucket(g.cfg.Bucket)}
}

func (g *gcsClient) logTrace(msg string, fields logrus.Fields) {
	if g.fileLog != nil {
		g.fileLog.WithFields(fields).Trace(msg)
	}
}

func (g *gcsClient) logCall(op, path string, d time.Duration) {
	atomic.AddInt64(&g.apiCalls, 1)
	g.logTrace("gcs_call", logrus.Fields{"op": op, "path": path, "ms": d.Milliseconds()})
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
		_ = g.logFile.Close()
	}
	_ = g.client.Close()
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
	query := &storage.Query{
		Prefix:    artifactsPrefix,
		Delimiter: "/",
	}

	ignoreSet := make(map[string]bool)
	for _, d := range g.cfg.IgnoreArtifactDirs {
		ignoreSet[d] = true
	}

	bucket := g.bucket()
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
		name := attrs.Prefix
		if name == "" {
			name = attrs.Name
		}
		g.logTrace("listArtifacts: iterating", logrus.Fields{"entry": name, "ms": time.Since(t0).Milliseconds()})
		if attrs.Prefix != "" {
			dirName := strings.TrimPrefix(attrs.Prefix, artifactsPrefix)
			dirName = strings.TrimSuffix(dirName, "/")
			elapsed := time.Since(t0)
			if ignoreSet[dirName] {
				g.logTrace("listArtifacts: skipping excluded dir", logrus.Fields{"dir": dirName, "ms": elapsed.Milliseconds()})
			} else {
				variantDir = dirName
				g.logTrace("listArtifacts: selected variant", logrus.Fields{"dir": dirName, "ms": elapsed.Milliseconds()})
				break
			}
		}
	}
	g.logCall("listArtifacts", artifactsPrefix, time.Since(t0))

	if variantDir == "" {
		logrus.WithFields(logrus.Fields{"job": job, "run": runID, "api_calls": g.CallCount()}).Debug("listSteps: no variant found")
		return make(map[string]StepResult), make(map[string][]string), "", nil
	}

	stepsPrefix := artifactsPrefix + variantDir + "/"
	steps, stepDirs, finishedPaths, err := g.scanStepObjects(ctx, stepsPrefix)
	if err != nil {
		return nil, nil, "", fmt.Errorf("scanning steps for %s/%s: %w", job, runID, err)
	}

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

// scanStepObjects discovers step names and no-recurse children using delimiter-based
// GCS listing, avoiding a full recursive object enumeration.
func (g *gcsClient) scanStepObjects(ctx context.Context, stepsPrefix string) (
	map[string]StepResult, map[string][]string, []string, error,
) {
	return g.scanStepObjectsWithBucket(ctx, g.bucket(), stepsPrefix)
}

func (g *gcsClient) scanStepObjectsWithBucket(ctx context.Context, bucket bucketLister, stepsPrefix string) (
	map[string]StepResult, map[string][]string, []string, error,
) {

	t0 := time.Now()
	query := &storage.Query{
		Prefix:    stepsPrefix,
		Delimiter: "/",
	}

	steps := make(map[string]StepResult)
	var stepNames []string
	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("listing steps under %s: %w", stepsPrefix, err)
		}
		if attrs.Prefix == "" {
			continue
		}
		stepName := strings.TrimPrefix(attrs.Prefix, stepsPrefix)
		stepName = strings.TrimSuffix(stepName, "/")
		steps[stepName] = StepUnknown
		stepNames = append(stepNames, stepName)
	}
	g.logTrace("scanStepObjects: listed steps", logrus.Fields{
		"steps": len(stepNames), "ms": time.Since(t0).Milliseconds(),
	})
	g.logCall("scanSteps", stepsPrefix, time.Since(t0))

	noRecurseSet := make(map[string]bool)
	for _, s := range g.cfg.NoRecurseSteps {
		noRecurseSet[s] = true
	}

	finishedPaths := make([]string, 0, len(stepNames))
	for _, name := range stepNames {
		finishedPaths = append(finishedPaths, stepsPrefix+name+"/finished.json")
	}

	stepDirs := make(map[string][]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, g.cfg.Concurrency)

	for _, name := range stepNames {
		if !noRecurseSet[name] {
			continue
		}
		wg.Add(1)
		go func(stepName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			children := g.listStepChildrenWithBucket(ctx, bucket, stepsPrefix+stepName+"/")
			mu.Lock()
			stepDirs[stepName] = children
			mu.Unlock()
		}(name)
	}
	wg.Wait()

	return steps, stepDirs, finishedPaths, nil
}

// listStepChildren returns immediate children of a step directory via delimiter-based listing.
func (g *gcsClient) listStepChildren(ctx context.Context, prefix string) []string {
	return g.listStepChildrenWithBucket(ctx, g.bucket(), prefix)
}

func (g *gcsClient) listStepChildrenWithBucket(ctx context.Context, bucket bucketLister, prefix string) []string {
	t0 := time.Now()
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
			logrus.WithError(err).WithField("prefix", prefix).Debug("failed to list step children")
			break
		}
		if attrs.Prefix != "" {
			children = append(children, strings.TrimPrefix(attrs.Prefix, prefix))
		} else if attrs.Name != "" {
			children = append(children, strings.TrimPrefix(attrs.Name, prefix))
		}
	}
	g.logTrace("listStepChildren", logrus.Fields{
		"prefix": prefix, "children": len(children), "ms": time.Since(t0).Milliseconds(),
	})
	g.logCall("listStepChildren", prefix, time.Since(t0))
	return children
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
	defer func() { _ = reader.Close() }()

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
