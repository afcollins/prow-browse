package main

import "testing"

func TestNormalizeGCSPath(t *testing.T) {
	bucket := "test-platform-results"

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "prow URL",
			raw:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/78517/rehearse-78517-periodic-ci/2054008345718689792",
			want: "pr-logs/pull/openshift_release/78517/rehearse-78517-periodic-ci/2054008345718689792",
		},
		{
			name: "gcsweb URL",
			raw:  gcsWebBaseURL + "test-platform-results/logs/periodic-ci-test/123/",
			want: "logs/periodic-ci-test/123",
		},
		{
			name: "gs:// URL",
			raw:  "gs://test-platform-results/logs/periodic-ci-test/456/",
			want: "logs/periodic-ci-test/456",
		},
		{
			name: "bucket-relative path",
			raw:  "test-platform-results/logs/periodic-ci-test/789",
			want: "logs/periodic-ci-test/789",
		},
		{
			name: "bare path without bucket",
			raw:  "logs/periodic-ci-test/101112",
			want: "logs/periodic-ci-test/101112",
		},
		{
			name: "trailing slash stripped",
			raw:  "logs/periodic-ci-test/131415/",
			want: "logs/periodic-ci-test/131415",
		},
		{
			name: "prow URL with trailing slash",
			raw:  "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/run/",
			want: "logs/job/run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeGCSPath(tt.raw, bucket)
			if got != tt.want {
				t.Errorf("normalizeGCSPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestVersionDefault(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty, expected default value")
	}
}
