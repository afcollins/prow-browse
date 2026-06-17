package main

import "testing"

func TestCollectFiles(t *testing.T) {
	tree := []*treeNode{
		{name: "a.txt", gcsPath: "a.txt"},
		{name: "dir", isDir: true, children: []*treeNode{
			{name: "b.txt", gcsPath: "dir/b.txt"},
			{name: "sub", isDir: true, children: []*treeNode{
				{name: "c.txt", gcsPath: "dir/sub/c.txt"},
			}},
		}},
		{name: "d.txt", gcsPath: "d.txt"},
	}

	files := collectFiles(tree)
	want := []string{"a.txt", "dir/b.txt", "dir/sub/c.txt", "d.txt"}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d", len(files), len(want))
	}
	for i, f := range files {
		if f.gcsPath != want[i] {
			t.Errorf("files[%d] = %s, want %s", i, f.gcsPath, want[i])
		}
	}
}

func TestCollectFilesEmpty(t *testing.T) {
	files := collectFiles(nil)
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestSetCheckedRecursive(t *testing.T) {
	nodes := []*treeNode{
		{name: "a.txt"},
		{name: "dir", isDir: true, children: []*treeNode{
			{name: "b.txt"},
			{name: "sub", isDir: true, children: []*treeNode{
				{name: "c.txt"},
			}},
		}},
	}

	setCheckedRecursive(nodes, true)
	if !nodes[0].checked || !nodes[1].checked || !nodes[1].children[0].checked || !nodes[1].children[1].children[0].checked {
		t.Error("not all nodes checked after setCheckedRecursive(true)")
	}

	setCheckedRecursive(nodes, false)
	if nodes[0].checked || nodes[1].checked || nodes[1].children[0].checked || nodes[1].children[1].children[0].checked {
		t.Error("some nodes still checked after setCheckedRecursive(false)")
	}
}

func TestCheckedItemsSkipsChildrenOfCheckedDir(t *testing.T) {
	m := browseModel{
		root: []*treeNode{
			{name: "dir", isDir: true, checked: true, children: []*treeNode{
				{name: "a.txt", checked: true},
				{name: "b.txt", checked: true},
			}},
			{name: "c.txt", checked: true},
			{name: "d.txt", checked: false},
		},
	}

	items := m.checkedItems()
	// Should return: dir (not its children) + c.txt = 2
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].name != "dir" {
		t.Errorf("items[0] = %s, want dir", items[0].name)
	}
	if items[1].name != "c.txt" {
		t.Errorf("items[1] = %s, want c.txt", items[1].name)
	}
}
