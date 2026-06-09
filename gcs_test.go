package main

import (
	"context"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type fakeBucketLister struct {
	queries []*storage.Query
	results map[string][]*storage.ObjectAttrs
}

func (f *fakeBucketLister) Objects(_ context.Context, q *storage.Query) objectIterator {
	f.queries = append(f.queries, q)
	attrs := f.results[q.Prefix+"|"+q.Delimiter]
	return &fakeIterator{attrs: attrs}
}

type fakeIterator struct {
	attrs []*storage.ObjectAttrs
	pos   int
}

func (f *fakeIterator) Next() (*storage.ObjectAttrs, error) {
	if f.pos >= len(f.attrs) {
		return nil, iterator.Done
	}
	a := f.attrs[f.pos]
	f.pos++
	return a, nil
}

func newTestGCSClient(noRecurse []string) *gcsClient {
	cfg := &Config{
		Concurrency: 4,
	}
	cfg.NoRecurseSteps = noRecurse
	return &gcsClient{cfg: cfg}
}

func TestScanStepObjects(t *testing.T) {
	prefix := "logs/job/123/artifacts/variant/"

	fake := &fakeBucketLister{
		results: map[string][]*storage.ObjectAttrs{
			prefix + "|/": {
				{Prefix: prefix + "step-a/"},
				{Prefix: prefix + "step-b/"},
				{Prefix: prefix + "step-c/"},
			},
		},
	}

	gc := newTestGCSClient(nil)
	steps, stepDirs, finishedPaths, err := gc.scanStepObjectsWithBucket(context.Background(), fake, prefix)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(steps) != 3 {
		t.Errorf("got %d steps, want 3", len(steps))
	}
	for _, name := range []string{"step-a", "step-b", "step-c"} {
		if _, ok := steps[name]; !ok {
			t.Errorf("missing step %q", name)
		}
		if steps[name] != StepUnknown {
			t.Errorf("step %q = %d, want StepUnknown", name, steps[name])
		}
	}

	if len(finishedPaths) != 3 {
		t.Errorf("got %d finishedPaths, want 3", len(finishedPaths))
	}
	for i, name := range []string{"step-a", "step-b", "step-c"} {
		want := prefix + name + "/finished.json"
		if finishedPaths[i] != want {
			t.Errorf("finishedPaths[%d] = %q, want %q", i, finishedPaths[i], want)
		}
	}

	if len(stepDirs) != 0 {
		t.Errorf("got %d stepDirs, want 0 (no no-recurse steps)", len(stepDirs))
	}
}

func TestScanStepObjectsWithNoRecurse(t *testing.T) {
	prefix := "logs/job/123/artifacts/variant/"

	fake := &fakeBucketLister{
		results: map[string][]*storage.ObjectAttrs{
			prefix + "|/": {
				{Prefix: prefix + "step-a/"},
				{Prefix: prefix + "gather-extra/"},
			},
			prefix + "gather-extra/|/": {
				{Prefix: prefix + "gather-extra/artifacts/"},
				{Name: prefix + "gather-extra/finished.json"},
				{Prefix: prefix + "gather-extra/logs/"},
			},
		},
	}

	gc := newTestGCSClient([]string{"gather-extra"})
	steps, stepDirs, finishedPaths, err := gc.scanStepObjectsWithBucket(context.Background(), fake, prefix)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(steps) != 2 {
		t.Errorf("got %d steps, want 2", len(steps))
	}

	if len(finishedPaths) != 2 {
		t.Errorf("got %d finishedPaths, want 2", len(finishedPaths))
	}

	children, ok := stepDirs["gather-extra"]
	if !ok {
		t.Fatal("missing stepDirs for gather-extra")
	}
	if len(children) != 3 {
		t.Errorf("got %d children for gather-extra, want 3", len(children))
	}

	wantChildren := map[string]bool{"artifacts/": true, "finished.json": true, "logs/": true}
	for _, c := range children {
		if !wantChildren[c] {
			t.Errorf("unexpected child %q", c)
		}
	}
}

func TestScanStepObjectsEmpty(t *testing.T) {
	prefix := "logs/job/123/artifacts/variant/"

	fake := &fakeBucketLister{
		results: map[string][]*storage.ObjectAttrs{
			prefix + "|/": {},
		},
	}

	gc := newTestGCSClient(nil)
	steps, stepDirs, finishedPaths, err := gc.scanStepObjectsWithBucket(context.Background(), fake, prefix)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("got %d steps, want 0", len(steps))
	}
	if len(stepDirs) != 0 {
		t.Errorf("got %d stepDirs, want 0", len(stepDirs))
	}
	if len(finishedPaths) != 0 {
		t.Errorf("got %d finishedPaths, want 0", len(finishedPaths))
	}
}

func TestListStepChildren(t *testing.T) {
	prefix := "logs/job/123/artifacts/variant/gather-extra/"

	fake := &fakeBucketLister{
		results: map[string][]*storage.ObjectAttrs{
			prefix + "|/": {
				{Prefix: prefix + "artifacts/"},
				{Prefix: prefix + "logs/"},
				{Name: prefix + "finished.json"},
			},
		},
	}

	gc := newTestGCSClient(nil)
	children := gc.listStepChildrenWithBucket(context.Background(), fake, prefix)

	if len(children) != 3 {
		t.Fatalf("got %d children, want 3", len(children))
	}

	want := map[string]bool{"artifacts/": true, "logs/": true, "finished.json": true}
	for _, c := range children {
		if !want[c] {
			t.Errorf("unexpected child %q", c)
		}
	}
}

func TestListStepChildrenEmpty(t *testing.T) {
	prefix := "logs/job/123/artifacts/variant/empty-step/"

	fake := &fakeBucketLister{
		results: map[string][]*storage.ObjectAttrs{
			prefix + "|/": {},
		},
	}

	gc := newTestGCSClient(nil)
	children := gc.listStepChildrenWithBucket(context.Background(), fake, prefix)

	if len(children) != 0 {
		t.Errorf("got %d children, want 0", len(children))
	}
}

func TestScanStepObjectsDelimiterQueries(t *testing.T) {
	prefix := "logs/job/123/artifacts/variant/"

	fake := &fakeBucketLister{
		results: map[string][]*storage.ObjectAttrs{
			prefix + "|/": {
				{Prefix: prefix + "step-a/"},
				{Prefix: prefix + "gather-extra/"},
			},
			prefix + "gather-extra/|/": {
				{Prefix: prefix + "gather-extra/sub/"},
			},
		},
	}

	gc := newTestGCSClient([]string{"gather-extra"})
	_, _, _, err := gc.scanStepObjectsWithBucket(context.Background(), fake, prefix)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have made exactly 2 queries: one for steps, one for gather-extra children
	if len(fake.queries) != 2 {
		t.Errorf("got %d queries, want 2", len(fake.queries))
	}
	for _, q := range fake.queries {
		if q.Delimiter != "/" {
			t.Errorf("query delimiter = %q, want /", q.Delimiter)
		}
	}
}
