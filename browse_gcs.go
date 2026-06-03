package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type dirEntry struct {
	Name  string
	IsDir bool
}

// listDir lists immediate children (dirs and files) under a GCS prefix.
func (g *gcsClient) listDir(ctx context.Context, prefix string) ([]dirEntry, error) {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	bucket := g.client.Bucket(g.cfg.Bucket)
	query := &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	}

	t0 := time.Now()
	var entries []dirEntry
	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if attrs.Prefix != "" {
			name := strings.TrimPrefix(attrs.Prefix, prefix)
			name = strings.TrimSuffix(name, "/")
			entries = append(entries, dirEntry{Name: name, IsDir: true})
		} else if attrs.Name != prefix {
			name := strings.TrimPrefix(attrs.Name, prefix)
			if name != "" {
				entries = append(entries, dirEntry{Name: name, IsDir: false})
			}
		}
	}
	g.logCall("listDir", prefix, time.Since(t0))
	return entries, nil
}

// downloadObject streams a GCS object to a local file, creating parent dirs.
func (g *gcsClient) downloadObject(ctx context.Context, objectPath, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	t0 := time.Now()
	reader, err := g.client.Bucket(g.cfg.Bucket).Object(objectPath).NewReader(ctx)
	if err != nil {
		g.logCall("download:err", objectPath, time.Since(t0))
		return fmt.Errorf("opening %s: %w", objectPath, err)
	}
	defer func() { _ = reader.Close() }()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", localPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, reader); err != nil {
		return fmt.Errorf("writing %s: %w", localPath, err)
	}
	g.logCall("download", objectPath, time.Since(t0))
	return nil
}
