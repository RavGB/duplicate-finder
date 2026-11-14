package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// countingFinder returns a finder whose hash function counts calls,
// so tests can prove exactly which files were (and were not) hashed.
func countingFinder(workers int, minSize int64, includeEmpty bool, calls *atomic.Int64) *finder {
	fd := newFinder(workers, minSize, includeEmpty)
	fd.hashFn = func(path string) ([sha256.Size]byte, error) {
		calls.Add(1)
		return sha256File(path)
	}
	return fd
}

func TestFindGroupsIdenticalContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello world")
	writeFile(t, filepath.Join(root, "nested", "b.txt"), "hello world")
	writeFile(t, filepath.Join(root, "nested", "deep", "c.txt"), "hello world")
	writeFile(t, filepath.Join(root, "unique.bin"), "unique-size!") // 12 bytes, size seen once
	writeFile(t, filepath.Join(root, "same1.bin"), "AAAA")
	writeFile(t, filepath.Join(root, "same2.bin"), "BBBB")

	var calls atomic.Int64
	report, err := countingFinder(4, 1, false, &calls).find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.FilesScanned != 6 {
		t.Errorf("FilesScanned = %d, want 6", report.FilesScanned)
	}
	// Only the size-collision candidates are hashed: the three
	// 11-byte copies and the two 4-byte files. The 12-byte file has a
	// unique size and must never be hashed.
	if calls.Load() != 5 {
		t.Errorf("hash calls = %d, want 5", calls.Load())
	}
	if report.CandidatesHashed != 5 {
		t.Errorf("CandidatesHashed = %d, want 5", report.CandidatesHashed)
	}
	if report.GroupCount != 1 {
		t.Fatalf("GroupCount = %d, want 1", report.GroupCount)
	}
	g := report.Groups[0]
	if g.Copies != 3 || g.Size != 11 || g.WastedBytes != 22 {
		t.Errorf("group = %+v, want 3 copies of 11 bytes wasting 22", g)
	}
	wantPaths := []string{
		filepath.Join(root, "a.txt"),
		filepath.Join(root, "nested", "b.txt"),
		filepath.Join(root, "nested", "deep", "c.txt"),
	}
	if len(g.Paths) != len(wantPaths) {
		t.Fatalf("group paths = %v, want %v", g.Paths, wantPaths)
	}
	for i, p := range wantPaths {
		if g.Paths[i] != p {
			t.Errorf("group path %d = %q, want %q (sorted)", i, g.Paths[i], p)
		}
	}
	if report.TotalWastedBytes != 22 {
		t.Errorf("TotalWastedBytes = %d, want 22", report.TotalWastedBytes)
	}
}

func TestFindSameSizeDifferentContentNotGrouped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "one.bin"), "AAAA")
	writeFile(t, filepath.Join(root, "two.bin"), "BBBB")

	report, err := newFinder(2, 1, false).find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.GroupCount != 0 {
		t.Errorf("GroupCount = %d, want 0 (same size, different content)", report.GroupCount)
	}
	if report.CandidatesHashed != 2 {
		t.Errorf("CandidatesHashed = %d, want 2 (both were worth hashing)", report.CandidatesHashed)
	}
}

func TestFindEmptySkippedByDefault(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "empty1"), "")
	writeFile(t, filepath.Join(root, "empty2"), "")

	var calls atomic.Int64
	report, err := countingFinder(2, 1, false, &calls).find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.SkippedEmpty != 2 {
		t.Errorf("SkippedEmpty = %d, want 2", report.SkippedEmpty)
	}
	if report.GroupCount != 0 {
		t.Errorf("GroupCount = %d, want 0 (empty files skipped by default)", report.GroupCount)
	}
	if calls.Load() != 0 {
		t.Errorf("hash calls = %d, want 0", calls.Load())
	}
}

func TestFindEmptyIncluded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "empty1"), "")
	writeFile(t, filepath.Join(root, "empty2"), "")

	report, err := newFinder(2, 0, true).find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.SkippedEmpty != 0 {
		t.Errorf("SkippedEmpty = %d, want 0 with --include-empty", report.SkippedEmpty)
	}
	if report.GroupCount != 1 {
		t.Fatalf("GroupCount = %d, want 1", report.GroupCount)
	}
	g := report.Groups[0]
	if g.Copies != 2 || g.Size != 0 || g.WastedBytes != 0 {
		t.Errorf("group = %+v, want 2 copies of 0 bytes wasting 0", g)
	}
}

func TestFindMinSizeFilter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.bin"), "AAAA")
	writeFile(t, filepath.Join(root, "b.bin"), "AAAA")

	var calls atomic.Int64
	report, err := countingFinder(2, 100, false, &calls).find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.GroupCount != 0 {
		t.Errorf("GroupCount = %d, want 0 (both files below --min-size)", report.GroupCount)
	}
	if calls.Load() != 0 {
		t.Errorf("hash calls = %d, want 0", calls.Load())
	}
}

func TestFindSymlinksSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real1.txt"), "hello world")
	writeFile(t, filepath.Join(root, "real2.txt"), "hello world")
	writeFile(t, filepath.Join(root, "sub", "real3.txt"), "hello world")

	linkFile := filepath.Join(root, "link.txt")
	if err := os.Symlink(filepath.Join(root, "real1.txt"), linkFile); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	// A symlinked directory holding a fourth copy must not be
	// followed, so the group stays at the three real copies.
	linkDir := filepath.Join(root, "linkdir")
	if err := os.Symlink(filepath.Join(root, "sub"), linkDir); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	report, err := newFinder(4, 1, false).find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.SkippedSymlinks != 2 {
		t.Errorf("SkippedSymlinks = %d, want 2 (file link and dir link)", report.SkippedSymlinks)
	}
	if report.GroupCount != 1 {
		t.Fatalf("GroupCount = %d, want 1", report.GroupCount)
	}
	if report.Groups[0].Copies != 3 {
		t.Errorf("copies = %d, want 3 (symlinked dir not followed)", report.Groups[0].Copies)
	}
	for _, p := range report.Groups[0].Paths {
		if p == linkFile {
			t.Errorf("symlink %q appears in the group", p)
		}
	}
}

func TestFindChangedFileDropped(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "moving.txt")
	writeFile(t, target, "hello world")
	writeFile(t, filepath.Join(root, "steady.txt"), "hello world")

	fd := newFinder(2, 1, false)
	var grown atomic.Bool
	fd.hashFn = func(path string) ([sha256.Size]byte, error) {
		if path == target && grown.CompareAndSwap(false, true) {
			// Simulate the file changing while the scan runs: it
			// grows by one byte after the walk recorded its size.
			f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return [sha256.Size]byte{}, err
			}
			if _, err := f.WriteString("!"); err != nil {
				f.Close()
				return [sha256.Size]byte{}, err
			}
			f.Close()
		}
		return sha256File(path)
	}

	report, err := fd.find(root)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.ChangedFiles != 1 {
		t.Errorf("ChangedFiles = %d, want 1", report.ChangedFiles)
	}
	if report.GroupCount != 0 {
		t.Errorf("GroupCount = %d, want 0 (the changed file must not be grouped)", report.GroupCount)
	}
}

func TestFindRootUnusable(t *testing.T) {
	_, err := newFinder(2, 1, false).find(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("find on a missing root: expected an error, got nil")
	}
}
