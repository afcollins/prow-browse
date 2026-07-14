package main

import "testing"

func TestNormalizeGCSPath(t *testing.T) {
	defaultBucket := "test-platform-results"

	tests := []struct {
		name       string
		raw        string
		wantBucket string
		wantPath   string
	}{
		{
			name:       "prow URL",
			raw:        "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/78517/rehearse-78517-periodic-ci/2054008345718689792",
			wantBucket: "test-platform-results",
			wantPath:   "pr-logs/pull/openshift_release/78517/rehearse-78517-periodic-ci/2054008345718689792",
		},
		{
			name:       "prow URL different bucket",
			raw:        "https://prow.ci.openshift.org/view/gs/kubernetes-ci-logs/logs/ci-test/123",
			wantBucket: "kubernetes-ci-logs",
			wantPath:   "logs/ci-test/123",
		},
		{
			name:       "openshift gcsweb URL",
			raw:        gcsWebBaseURL + "test-platform-results/logs/periodic-ci-test/123/",
			wantBucket: "test-platform-results",
			wantPath:   "logs/periodic-ci-test/123",
		},
		{
			name:       "k8s gcsweb URL",
			raw:        "https://gcsweb.k8s.io/gcs/kubernetes-ci-logs/logs/ci-kubernetes-e2e-test/456/",
			wantBucket: "kubernetes-ci-logs",
			wantPath:   "logs/ci-kubernetes-e2e-test/456",
		},
		{
			name:       "gs:// URL same bucket",
			raw:        "gs://test-platform-results/logs/periodic-ci-test/456/",
			wantBucket: "test-platform-results",
			wantPath:   "logs/periodic-ci-test/456",
		},
		{
			name:       "gs:// URL different bucket",
			raw:        "gs://kubernetes-ci-logs/logs/ci-test/789/",
			wantBucket: "kubernetes-ci-logs",
			wantPath:   "logs/ci-test/789",
		},
		{
			name:       "bucket-relative path",
			raw:        "test-platform-results/logs/periodic-ci-test/789",
			wantBucket: "test-platform-results",
			wantPath:   "logs/periodic-ci-test/789",
		},
		{
			name:       "bare path without bucket",
			raw:        "logs/periodic-ci-test/101112",
			wantBucket: "test-platform-results",
			wantPath:   "logs/periodic-ci-test/101112",
		},
		{
			name:       "trailing slash stripped",
			raw:        "logs/periodic-ci-test/131415/",
			wantBucket: "test-platform-results",
			wantPath:   "logs/periodic-ci-test/131415",
		},
		{
			name:       "prow URL with trailing slash",
			raw:        "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/run/",
			wantBucket: "test-platform-results",
			wantPath:   "logs/job/run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBucket, gotPath := normalizeGCSPath(tt.raw, defaultBucket)
			if gotBucket != tt.wantBucket {
				t.Errorf("normalizeGCSPath(%q) bucket = %q, want %q", tt.raw, gotBucket, tt.wantBucket)
			}
			if gotPath != tt.wantPath {
				t.Errorf("normalizeGCSPath(%q) path = %q, want %q", tt.raw, gotPath, tt.wantPath)
			}
		})
	}
}

func TestVersionDefault(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty, expected default value")
	}
}
