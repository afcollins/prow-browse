package main

import "testing"

func TestFilterDownloaded(t *testing.T) {
	mkNode := func(path string) *treeNode {
		return &treeNode{name: path, gcsPath: path}
	}

	tests := []struct {
		name         string
		files        []*treeNode
		downloaded   map[string]bool
		wantDownload int
		wantSkipped  int
	}{
		{
			name:         "all new",
			files:        []*treeNode{mkNode("a.txt"), mkNode("b.txt")},
			downloaded:   map[string]bool{},
			wantDownload: 2,
			wantSkipped:  0,
		},
		{
			name:         "all already downloaded",
			files:        []*treeNode{mkNode("a.txt"), mkNode("b.txt")},
			downloaded:   map[string]bool{"a.txt": true, "b.txt": true},
			wantDownload: 0,
			wantSkipped:  2,
		},
		{
			name:         "mix of new and downloaded",
			files:        []*treeNode{mkNode("a.txt"), mkNode("b.txt"), mkNode("c.txt")},
			downloaded:   map[string]bool{"b.txt": true},
			wantDownload: 2,
			wantSkipped:  1,
		},
		{
			name:         "empty input",
			files:        nil,
			downloaded:   map[string]bool{},
			wantDownload: 0,
			wantSkipped:  0,
		},
		{
			name:         "nil downloaded map",
			files:        []*treeNode{mkNode("a.txt")},
			downloaded:   nil,
			wantDownload: 1,
			wantSkipped:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toDownload, skipped := filterDownloaded(tt.files, tt.downloaded)
			if len(toDownload) != tt.wantDownload {
				t.Errorf("toDownload: got %d, want %d", len(toDownload), tt.wantDownload)
			}
			if len(skipped) != tt.wantSkipped {
				t.Errorf("skipped: got %d, want %d", len(skipped), tt.wantSkipped)
			}
		})
	}
}

func TestFilterDownloadedPreservesOrder(t *testing.T) {
	files := []*treeNode{
		{gcsPath: "a"}, {gcsPath: "b"}, {gcsPath: "c"}, {gcsPath: "d"},
	}
	downloaded := map[string]bool{"b": true, "d": true}

	toDownload, skipped := filterDownloaded(files, downloaded)

	if len(toDownload) != 2 || toDownload[0].gcsPath != "a" || toDownload[1].gcsPath != "c" {
		t.Errorf("toDownload order wrong: %v", toDownload)
	}
	if len(skipped) != 2 || skipped[0].gcsPath != "b" || skipped[1].gcsPath != "d" {
		t.Errorf("skipped order wrong: %v", skipped)
	}
}
